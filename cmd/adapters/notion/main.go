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

	"github.com/dotrage/forge-adp/pkg/events"
	"github.com/dotrage/forge-adp/pkg/logger"
)

const notionAPIBase = "https://api.notion.com/v1"
const notionVersion = "2022-06-28"

type NotionAdapter struct {
	token          string
	databaseID     string
	forgeLabelProp string
	bus            events.Bus
	httpClient     *http.Client
}

type notionPage struct {
	ID         string                 `json:"id"`
	URL        string                 `json:"url"`
	Properties map[string]interface{} `json:"properties"`
}

func main() {
	logger.Init("notion-adapter")

	token := os.Getenv("NOTION_API_TOKEN")
	if token == "" {
		slog.Error("NOTION_API_TOKEN is required")
		os.Exit(1)
	}

	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		slog.Error("failed to create event bus", slog.Any("error", err))
		os.Exit(1)
	}

	adapter := &NotionAdapter{
		token:          token,
		databaseID:     os.Getenv("NOTION_DATABASE_ID"),
		forgeLabelProp: os.Getenv("NOTION_FORGE_LABEL_PROP"),
		bus:            bus,
		httpClient:     &http.Client{},
	}

	go adapter.subscribeToEvents()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/webhook", adapter.HandleWebhook)
	mux.HandleFunc("/api/v1/pages", adapter.HandlePages)
	mux.HandleFunc("/api/v1/databases", adapter.HandleDatabases)

	slog.Info("Notion adapter listening", slog.String("addr", ":19126"))
	http.ListenAndServe(":19126", logger.HTTPMiddleware("notion-adapter", mux))
}

// HandleWebhook processes Notion webhook events (Notion's webhook integration is in beta).
func (a *NotionAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	eventType, _ := payload["type"].(string)
	switch eventType {
	case "page_created":
		if page, ok := payload["entity"].(map[string]interface{}); ok {
			ep, _ := json.Marshal(map[string]interface{}{
				"page_id": page["id"],
				"url":     page["url"],
				"source":  "notion",
			})
			if err := a.bus.Publish(r.Context(), events.Event{Type: events.TaskCreated, Payload: ep}); err != nil {
				slog.Error("failed to publish task created event", slog.Any("error", err))
			}
		}
	case "page_updated":
		if page, ok := payload["entity"].(map[string]interface{}); ok {
			ep, _ := json.Marshal(map[string]interface{}{
				"page_id": page["id"],
				"source":  "notion",
			})
			if err := a.bus.Publish(r.Context(), events.Event{Type: events.TaskCompleted, Payload: ep}); err != nil {
				slog.Error("failed to publish task completed event", slog.Any("error", err))
			}
		}
	}
	w.WriteHeader(http.StatusOK)
}

// HandlePages retrieves, creates, or updates Notion pages.
//
//	GET   /api/v1/pages?id=<id>
//	POST  /api/v1/pages
//	PATCH /api/v1/pages?id=<id>
func (a *NotionAdapter) HandlePages(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		id := r.URL.Query().Get("id")
		if id == "" {
			http.Error(w, "id query parameter is required", http.StatusBadRequest)
			return
		}
		var result notionPage
		if err := a.notionRequest(r.Context(), http.MethodGet, "/pages/"+id, nil, &result); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	case http.MethodPost:
		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var result notionPage
		if err := a.notionRequest(r.Context(), http.MethodPost, "/pages", req, &result); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(result)
	case http.MethodPatch:
		id := r.URL.Query().Get("id")
		if id == "" {
			http.Error(w, "id query parameter is required", http.StatusBadRequest)
			return
		}
		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var result notionPage
		if err := a.notionRequest(r.Context(), http.MethodPatch, "/pages/"+id, req, &result); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// HandleDatabases queries a Notion database.
//
//	GET  /api/v1/databases[?id=<id>]
//	POST /api/v1/databases[?id=<id>]  (query)
func (a *NotionAdapter) HandleDatabases(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" && a.databaseID != "" {
		id = a.databaseID
	}

	switch r.Method {
	case http.MethodGet:
		if id == "" {
			http.Error(w, "id query parameter is required", http.StatusBadRequest)
			return
		}
		var result map[string]interface{}
		if err := a.notionRequest(r.Context(), http.MethodGet, "/databases/"+id, nil, &result); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	case http.MethodPost:
		if id == "" {
			http.Error(w, "id query parameter is required", http.StatusBadRequest)
			return
		}
		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var result map[string]interface{}
		if err := a.notionRequest(r.Context(), http.MethodPost, fmt.Sprintf("/databases/%s/query", id), req, &result); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// subscribeToEvents listens for TaskCompleted and TaskFailed events and updates
// the corresponding Notion page if notion_page_id is present in the payload.
func (a *NotionAdapter) subscribeToEvents() {
	ctx := context.Background()
	if err := a.bus.Subscribe(ctx, []events.EventType{
		events.TaskCompleted,
		events.TaskFailed,
	}, func(e events.Event) error {
		var payload struct {
			PageID string `json:"notion_page_id"`
			Source string `json:"source"`
		}
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			return fmt.Errorf("unmarshal payload: %w", err)
		}
		if payload.PageID == "" || payload.Source == "notion" {
			return nil
		}

		statusText := "Done"
		if e.Type == events.TaskFailed {
			statusText = "Failed"
		}

		slog.Info("updating Notion page for completed task",
			slog.String("page_id", payload.PageID),
			slog.String("status", statusText),
			slog.String("task_id", e.TaskID))

		propName := a.forgeLabelProp
		if propName == "" {
			propName = "Status"
		}
		update := map[string]interface{}{
			"properties": map[string]interface{}{
				propName: map[string]interface{}{
					"select": map[string]interface{}{"name": statusText},
				},
			},
		}
		if err := a.notionRequest(ctx, http.MethodPatch, "/pages/"+payload.PageID, update, nil); err != nil {
			slog.Warn("failed to update Notion page",
				slog.String("page_id", payload.PageID),
				slog.Any("error", err))
		}
		return nil
	}); err != nil {
		slog.Error("failed to subscribe to events", slog.Any("error", err))
	}
}

func (a *NotionAdapter) notionRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, notionAPIBase+path, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Notion-Version", notionVersion)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("notion API error %d: %s", resp.StatusCode, string(b))
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
