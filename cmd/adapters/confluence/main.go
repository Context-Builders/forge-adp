package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/dotrage/forge-adp/pkg/events"
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
	baseURL := os.Getenv("CONFLUENCE_BASE_URL")
	if baseURL == "" {
		log.Fatal("CONFLUENCE_BASE_URL is required")
	}

	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		log.Fatalf("failed to create event bus: %v", err)
	}

	adapter := &ConfluenceAdapter{
		baseURL:  baseURL,
		username: os.Getenv("CONFLUENCE_USERNAME"),
		apiToken: os.Getenv("CONFLUENCE_API_TOKEN"),
		bus:      bus,
		http:     &http.Client{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/webhook", adapter.HandleWebhook)
	mux.HandleFunc("/api/v1/pages", adapter.HandlePages)
	mux.HandleFunc("/api/v1/spaces", adapter.HandleSpaces)

	log.Printf("Confluence adapter listening on :19096")
	http.ListenAndServe(":19096", mux)
}

func (a *ConfluenceAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
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
	})
	a.bus.Publish(ctx, events.Event{
		Type:    events.TaskCreated,
		Payload: payload,
	})
}

func (a *ConfluenceAdapter) handlePageUpdated(ctx context.Context, evt WebhookEvent) {
	// Handle page update events - e.g., spec or doc changes that trigger agent tasks.
}

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
