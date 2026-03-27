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

// scaffolder task completion events via a custom webhook.
// Software Catalog and TechDocs APIs. It also supports receiving Backstage
// Backstage adapter exposes a REST bridge between Forge and Backstage's)

type BackstageAdapter struct {
	baseURL    string
	token      string
	bus        events.Bus
	httpClient *http.Client
}

type backstageScaffolderEvent struct {
	Task struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Spec   struct {
			TemplateName string `json:"templateName"`
			Values       map[string]interface{} `json:"values"`
		} `json:"spec"`
} `json:"task"`
}

func main() {
	baseURL := os.Getenv("BACKSTAGE_URL")
	if baseURL == "" {
		log.Fatal("BACKSTAGE_URL is required")
	}
bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
if err != nil {
	log.Fatalf("failed to create event bus: %v", err)
}
adapter := &BackstageAdapter{
	baseURL:    strings.TrimRight(baseURL, "/"),
	token:      os.Getenv("BACKSTAGE_TOKEN"),
	bus:        bus,
	httpClient: &http.Client{},
}
go adapter.subscribeToEvents()
mux := http.NewServeMux()
mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})
mux.HandleFunc("/webhook/scaffolder", adapter.HandleScaffolderWebhook)
mux.HandleFunc("/api/v1/entities", adapter.HandleEntities)
mux.HandleFunc("/api/v1/components", adapter.HandleComponents)
log.Printf("Backstage adapter listening on :19129")
http.ListenAndServe(":19129", mux)
}

func (a *BackstageAdapter) HandleScaffolderWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

var payload backstageScaffolderEvent
if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}
switch payload.Task.Status {
	case "completed":
	ep, _ := json.Marshal(map[string]interface{}{
		"task_id":  payload.Task.ID,
		"template": payload.Task.Spec.TemplateName,
		"values":   payload.Task.Spec.Values,
		"source":   "backstage",
})
if err := a.bus.Publish(r.Context(), events.Event{Type: events.TaskCompleted, Payload: ep}); err != nil {
	log.Printf("failed to publish task completed event: %v", err)
}
case "failed":
ep, _ := json.Marshal(map[string]interface{}{
	"task_id":  payload.Task.ID,
	"template": payload.Task.Spec.TemplateName,
	"source":   "backstage",
})
if err := a.bus.Publish(r.Context(), events.Event{Type: events.TaskFailed, Payload: ep}); err != nil {
	log.Printf("failed to publish task failed event: %v", err)
}
}
w.WriteHeader(http.StatusOK)
}

func (a *BackstageAdapter) HandleEntities(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
		case http.MethodGet:
		kind := r.URL.Query().Get("kind")
		path := "/api/catalog/entities"
		if kind != "" {
			path += "?filter=kind=" + kind
		}

var result interface{}
if err := a.bsRequest(r.Context(), http.MethodGet, path, nil, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
default:
http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
}

func (a *BackstageAdapter) HandleComponents(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
		case http.MethodGet:
		name := r.URL.Query().Get("name")
		namespace := r.URL.Query().Get("namespace")
		if namespace == "" {
			namespace = "default"
		}
	path := fmt.Sprintf("/api/catalog/entities/by-name/component/%s/%s", namespace, name)
	if name == "" {
		path = "/api/catalog/entities?filter=kind=Component"
	}

var result interface{}
if err := a.bsRequest(r.Context(), http.MethodGet, path, nil, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
default:
http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
}

func (a *BackstageAdapter) subscribeToEvents() {
	ctx := context.Background()
	a.bus.Subscribe(ctx, []events.EventType{events.TaskCreated}, func(e events.Event) error {
		return nil
})
}

func (a *BackstageAdapter) bsRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {

var bodyReader io.Reader
if body != nil {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
bodyReader = strings.NewReader(string(b))
}
req, err := http.NewRequestWithContext(ctx, method, a.baseURL+path, bodyReader)
if err != nil {
	return fmt.Errorf("create request: %w", err)
}
if a.token != "" {
	req.Header.Set("Authorization", "Bearer "+a.token)
}
req.Header.Set("Content-Type", "application/json")
resp, err := a.httpClient.Do(req)
if err != nil {
	return fmt.Errorf("execute request: %w", err)
}
defer resp.Body.Close()
if resp.StatusCode >= 300 {
	b, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("backstage API error %d: %s", resp.StatusCode, string(b))
}
if out != nil {
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
}
return nil
}
