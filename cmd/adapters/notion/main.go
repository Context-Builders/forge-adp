package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"github.com/dotrage/forge-adp/pkg/events"
)

const notionAPIBase = "https://api.notion.com/v1"
const notionVersion = "2022-06-28"

type NotionAdapter struct {
	token         string
	databaseID    string
	forgeLabelProp string
	bus           events.Bus
	httpClient    *http.Client
}

type notionPage struct {
	ID         string `json:"id"`
	URL        string `json:"url"`
	Properties map[string]interface{} `json:"properties"`
}

func main() {
	token := os.Getenv("NOTION_API_TOKEN")
	if token == "" {
		log.Fatal("NOTION_API_TOKEN is required")
	}
bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
if err != nil {
	log.Fatalf("failed to create event bus: %v", err)
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
log.Printf("Notion adapter listening on :19126")
http.ListenAndServe(":19126", mux)
}
// integration webhook — currently in beta; falls back to polling pattern).
// HandleWebhook processes Notion webhook events (requires Notion's internal

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
	log.Printf("failed to publish task created event: %v", err)
}
}
case "page_updated":
if page, ok := payload["entity"].(map[string]interface{}); ok {
	ep, _ := json.Marshal(map[string]interface{}{
		"page_id": page["id"],
		"source":  "notion",
})
if err := a.bus.Publish(r.Context(), events.Event{Type: events.TaskCompleted, Payload: ep}); err != nil {
	log.Printf("failed to publish task completed event: %v", err)
}
}
}
w.WriteHeader(http.StatusOK)
}

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

func (a *NotionAdapter) HandleDatabases(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
		case http.MethodGet:
		id := r.URL.Query().Get("id")
		if id == "" && a.databaseID != "" {
			id = a.databaseID
		}
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
id := r.URL.Query().Get("id")
if id == "" && a.databaseID != "" {
	id = a.databaseID
}
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

func (a *NotionAdapter) subscribeToEvents() {
	ctx := context.Background()
	a.bus.Subscribe(ctx, []events.EventType{
		events.TaskCompleted,
		events.TaskFailed,
	}, func(e events.Event) error {
	return nil
})
}

func (a *NotionAdapter) notionRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {

var bodyReader io.Reader
if body != nil {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
bodyReader = strings.NewReader(string(b))
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
