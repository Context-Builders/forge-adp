package main

import (
	"log"
	"net/http"
	"os"
	"strings"
	"github.com/dotrage/forge-adp/pkg/events"
	"context"
	"encoding/json"
	"fmt"
	"io"
)

const argoCDAPIBase = "/api/v1"

type ArgoCDAdapter struct {
	baseURL    string
	token      string
	bus        events.Bus
	httpClient *http.Client
}

type argoCDAppStatus struct {
	Phase   string `json:"phase"`
	Message string `json:"message"`
}

type argoCDApp struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
Status struct {
	OperationState *argoCDAppStatus `json:"operationState"`
	Sync           struct {
		Status string `json:"status"`
	} `json:"sync"`
Health struct {
	Status string `json:"status"`
} `json:"health"`
} `json:"status"`
}

type argoCDWebhookPayload struct {
	Application argoCDApp `json:"application"`
}

func main() {
	baseURL := os.Getenv("ARGOCD_URL")
	token := os.Getenv("ARGOCD_TOKEN")
	if baseURL == "" || token == "" {
		log.Fatal("ARGOCD_URL and ARGOCD_TOKEN are required")
	}
bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
if err != nil {
	log.Fatalf("failed to create event bus: %v", err)
}
adapter := &ArgoCDAdapter{
	baseURL:    strings.TrimRight(baseURL, "/"),
	token:      token,
	bus:        bus,
	httpClient: &http.Client{},
}
go adapter.subscribeToEvents()
mux := http.NewServeMux()
mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})
mux.HandleFunc("/webhook", adapter.HandleWebhook)
mux.HandleFunc("/api/v1/apps", adapter.HandleApps)
mux.HandleFunc("/api/v1/sync", adapter.HandleSync)
log.Printf("ArgoCD adapter listening on :19113")
http.ListenAndServe(":19113", mux)
}

func (a *ArgoCDAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

var payload argoCDWebhookPayload
if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}
app := payload.Application
if app.Status.OperationState != nil {
	switch app.Status.OperationState.Phase {
		case "Succeeded":
		a.handleSyncSucceeded(r.Context(), app)
		case "Failed", "Error":
		a.handleSyncFailed(r.Context(), app)
		case "Running":
		a.handleSyncRunning(r.Context(), app)
	}
}
w.WriteHeader(http.StatusOK)
}

func (a *ArgoCDAdapter) handleSyncSucceeded(ctx context.Context, app argoCDApp) {
	ep, _ := json.Marshal(map[string]interface{}{
		"app_name":    app.Metadata.Name,
		"sync_status": app.Status.Sync.Status,
		"health":      app.Status.Health.Status,
		"source":      "argocd",
})
if err := a.bus.Publish(ctx, events.Event{Type: events.DeploymentApproved, Payload: ep}); err != nil {
	log.Printf("failed to publish deployment approved event: %v", err)
}
}

func (a *ArgoCDAdapter) handleSyncFailed(ctx context.Context, app argoCDApp) {
	ep, _ := json.Marshal(map[string]interface{}{
		"app_name": app.Metadata.Name,
		"message":  app.Status.OperationState.Message,
		"source":   "argocd",
})
if err := a.bus.Publish(ctx, events.Event{Type: events.TaskFailed, Payload: ep}); err != nil {
	log.Printf("failed to publish task failed event: %v", err)
}
}

func (a *ArgoCDAdapter) handleSyncRunning(ctx context.Context, app argoCDApp) {
	ep, _ := json.Marshal(map[string]interface{}{
		"app_name": app.Metadata.Name,
		"source":   "argocd",
})
if err := a.bus.Publish(ctx, events.Event{Type: events.TaskStarted, Payload: ep}); err != nil {
	log.Printf("failed to publish task started event: %v", err)
}
}

func (a *ArgoCDAdapter) HandleApps(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
		case http.MethodGet:

var result map[string]interface{}
if err := a.argoRequest(r.Context(), http.MethodGet, "/applications", nil, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
default:
http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
}

func (a *ArgoCDAdapter) HandleSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
appName := r.URL.Query().Get("app")
if appName == "" {
	http.Error(w, "app query parameter is required", http.StatusBadRequest)
	return
}

var result map[string]interface{}
if err := a.argoRequest(r.Context(), http.MethodPost, fmt.Sprintf("/applications/%s/sync", appName), map[string]interface{}{}, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
}

func (a *ArgoCDAdapter) subscribeToEvents() {
	ctx := context.Background()
	a.bus.Subscribe(ctx, []events.EventType{events.DeploymentRequested}, func(e events.Event) error {
		return nil
})
}

func (a *ArgoCDAdapter) argoRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {

var bodyReader io.Reader
if body != nil {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
bodyReader = strings.NewReader(string(b))
}
req, err := http.NewRequestWithContext(ctx, method, a.baseURL+argoCDAPIBase+path, bodyReader)
if err != nil {
	return fmt.Errorf("create request: %w", err)
}
req.Header.Set("Authorization", "Bearer "+a.token)
req.Header.Set("Content-Type", "application/json")
resp, err := a.httpClient.Do(req)
if err != nil {
	return fmt.Errorf("execute request: %w", err)
}
defer resp.Body.Close()
if resp.StatusCode >= 300 {
	b, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("argocd API error %d: %s", resp.StatusCode, string(b))
}
if out != nil {
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
}
return nil
}
