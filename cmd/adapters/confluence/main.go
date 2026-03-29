package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/dotrage/forge-adp/pkg/events"
	"github.com/dotrage/forge-adp/pkg/logger"
)

// ConfluenceAdapter handles bidirectional communication with Confluence via
// the Confluence REST API v2 (inbound webhooks + outbound API calls).
type ConfluenceAdapter struct {
	baseURL  string
	username string
	apiToken string
	bus      events.Bus
	http     *http.Client
}

// Page represents a Confluence page.
type Page struct {
	ID      string      `json:"id,omitempty"`
	Title   string      `json:"title"`
	SpaceID string      `json:"spaceId,omitempty"`
	Status  string      `json:"status,omitempty"`
	Body    PageBody    `json:"body"`
	Version PageVersion `json:"version,omitempty"`
}

type PageBody struct {
	Representation string `json:"representation"`
	Value          string `json:"value"`
}

type PageVersion struct {
	Number int `json:"number"`
}

// WebhookEvent is the structure Confluence sends for page events.
type WebhookEvent struct {
	EventType string                 `json:"eventType"`
	Page      map[string]interface{} `json:"page,omitempty"`
	Space     map[string]interface{} `json:"space,omitempty"`
	Actor     map[string]interface{} `json:"actor,omitempty"`
}

func main() {
	logger.Init("confluence-adapter")

	baseURL := os.Getenv("CONFLUENCE_BASE_URL")
	if baseURL == "" {
		slog.Error("CONFLUENCE_BASE_URL is required")
		os.Exit(1)
	}

	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		slog.Error("failed to create event bus", slog.Any("error", err))
		os.Exit(1)
	}

	adapter := &ConfluenceAdapter{
		baseURL:  strings.TrimRight(baseURL, "/"),
		username: os.Getenv("CONFLUENCE_USERNAME"),
		apiToken: os.Getenv("CONFLUENCE_API_TOKEN"),
		bus:      bus,
		http:     &http.Client{},
	}

	go adapter.subscribeToEvents()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/webhook", adapter.HandleWebhook)
	mux.HandleFunc("/api/v1/pages", adapter.HandlePages)
	mux.HandleFunc("/api/v1/spaces", adapter.HandleSpaces)

	slog.Info("Confluence adapter listening", slog.String("addr", ":19096"))
	http.ListenAndServe(":19096", logger.HTTPMiddleware("confluence-adapter", mux))
}

func (a *ConfluenceAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var evt WebhookEvent
	if err := json.NewDecoder(r.Body).Decode(&evt); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	switch evt.EventType {
	case "page_created":
		a.handlePageCreated(r.Context(), evt)
	case "page_updated":
		a.handlePageUpdated(r.Context(), evt)
	}
	w.WriteHeader(http.StatusOK)
}

func (a *ConfluenceAdapter) handlePageCreated(ctx context.Context, evt WebhookEvent) {
	page := evt.Page
	labels, _ := page["labels"].([]interface{})
	forgeEligible := false
	for _, l := range labels {
		if lm, ok := l.(map[string]interface{}); ok {
			if lm["name"] == "forge" {
				forgeEligible = true
				break
			}
		}
	}
	if !forgeEligible {
		return
	}

	payload, _ := json.Marshal(map[string]interface{}{
		"page_id": page["id"],
		"title":   page["title"],
		"url":     page["_links"],
		"space":   evt.Space,
		"source":  "confluence",
	})
	if err := a.bus.Publish(ctx, events.Event{Type: events.TaskCreated, Payload: payload}); err != nil {
		slog.Error("failed to publish task created event",
			slog.Any("page_id", page["id"]),
			slog.Any("error", err))
	}
}

// handlePageUpdated publishes a ReviewRequested event when a page tagged
// "forge-review" is updated — signalling that agents should review the change.
func (a *ConfluenceAdapter) handlePageUpdated(ctx context.Context, evt WebhookEvent) {
	page := evt.Page
	labels, _ := page["labels"].([]interface{})
	for _, l := range labels {
		if lm, ok := l.(map[string]interface{}); ok {
			if lm["name"] == "forge-review" {
				payload, _ := json.Marshal(map[string]interface{}{
					"page_id": page["id"],
					"title":   page["title"],
					"url":     page["_links"],
					"space":   evt.Space,
					"source":  "confluence",
				})
				if err := a.bus.Publish(ctx, events.Event{Type: events.ReviewRequested, Payload: payload}); err != nil {
					slog.Error("failed to publish review requested event",
						slog.Any("page_id", page["id"]),
						slog.Any("error", err))
				}
				return
			}
		}
	}
}

// HandlePages fetches, creates, or updates a Confluence page.
//
//	GET /api/v1/pages?id=<id>
//	POST /api/v1/pages  (Page body)
//	PUT  /api/v1/pages?id=<id>  (Page body)
func (a *ConfluenceAdapter) HandlePages(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		pageID := r.URL.Query().Get("id")
		if pageID == "" {
			http.Error(w, "id query param required", http.StatusBadRequest)
			return
		}
		resp, err := a.apiGet(r.Context(), fmt.Sprintf("/wiki/api/v2/pages/%s?body-format=storage", pageID))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		io.Copy(w, resp.Body)

	case http.MethodPost:
		var page Page
		if err := json.NewDecoder(r.Body).Decode(&page); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp, err := a.apiPost(r.Context(), "/wiki/api/v2/pages", page)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		io.Copy(w, resp.Body)

	case http.MethodPut:
		pageID := r.URL.Query().Get("id")
		if pageID == "" {
			http.Error(w, "id query param required", http.StatusBadRequest)
			return
		}
		var page Page
		if err := json.NewDecoder(r.Body).Decode(&page); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp, err := a.apiPut(r.Context(), fmt.Sprintf("/wiki/api/v2/pages/%s", pageID), page)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		io.Copy(w, resp.Body)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// HandleSpaces lists Confluence spaces.
//
//	GET /api/v1/spaces
func (a *ConfluenceAdapter) HandleSpaces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	resp, err := a.apiGet(r.Context(), "/wiki/api/v2/spaces")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	io.Copy(w, resp.Body)
}

// subscribeToEvents listens for TaskCompleted events and appends a completion
// note to the Confluence page if confluence_page_id is present in the payload.
func (a *ConfluenceAdapter) subscribeToEvents() {
	ctx := context.Background()
	if err := a.bus.Subscribe(ctx, []events.EventType{events.TaskCompleted}, func(e events.Event) error {
		var payload struct {
			ConfluencePageID string `json:"confluence_page_id"`
			TaskID           string `json:"task_id"`
			JiraKey          string `json:"jira_key"`
		}
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			return fmt.Errorf("unmarshal task completed payload: %w", err)
		}
		if payload.ConfluencePageID == "" {
			return nil // no Confluence page to update
		}

		slog.Info("appending completion note to Confluence page",
			slog.String("page_id", payload.ConfluencePageID),
			slog.String("task_id", e.TaskID))

		// Fetch current page to get version number (required for updates).
		resp, err := a.apiGet(ctx, fmt.Sprintf("/wiki/api/v2/pages/%s", payload.ConfluencePageID))
		if err != nil {
			slog.Warn("failed to fetch Confluence page for update",
				slog.String("page_id", payload.ConfluencePageID),
				slog.Any("error", err))
			return nil
		}
		defer resp.Body.Close()
		var current struct {
			Title   string      `json:"title"`
			Version PageVersion `json:"version"`
			Body    struct {
				Storage PageBody `json:"storage"`
			} `json:"body"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&current); err != nil {
			return nil // non-fatal
		}

		note := fmt.Sprintf("\n<p><em>Forge task %s completed</em></p>", payload.TaskID)
		if payload.JiraKey != "" {
			note = fmt.Sprintf("\n<p><em>Forge task %s (%s) completed</em></p>", payload.JiraKey, payload.TaskID)
		}
		update := Page{
			Title:   current.Title,
			Version: PageVersion{Number: current.Version.Number + 1},
			Body: PageBody{
				Representation: "storage",
				Value:          current.Body.Storage.Value + note,
			},
		}
		updateResp, err := a.apiPut(ctx, fmt.Sprintf("/wiki/api/v2/pages/%s", payload.ConfluencePageID), update)
		if err != nil {
			slog.Warn("failed to update Confluence page",
				slog.String("page_id", payload.ConfluencePageID),
				slog.Any("error", err))
			return nil
		}
		updateResp.Body.Close()
		return nil
	}); err != nil {
		slog.Error("failed to subscribe to task completed events", slog.Any("error", err))
	}
}

func (a *ConfluenceAdapter) apiGet(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(a.username, a.apiToken)
	req.Header.Set("Accept", "application/json")
	return a.http.Do(req)
}

func (a *ConfluenceAdapter) apiPost(ctx context.Context, path string, body interface{}) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(a.username, a.apiToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	return a.http.Do(req)
}

func (a *ConfluenceAdapter) apiPut(ctx context.Context, path string, body interface{}) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, a.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(a.username, a.apiToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	return a.http.Do(req)
}
