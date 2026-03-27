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

const cypressAPIBase = "https://api.cypress.io/v1"

type CypressAdapter struct {
	apiKey     string
	projectID  string
	bus        events.Bus
	httpClient *http.Client
}

type cypressRunWebhook struct {
	Event string `json:"event"` // "RUN_COMPLETED"
	Run   struct {
		ID        string `json:"id"`
		Status    string `json:"status"` // "passed" | "failed" | "errored" | "cancelled" | "noTests"
		TotalFailed  int    `json:"totalFailed"`
		TotalPassed  int    `json:"totalPassed"`
		TotalPending int    `json:"totalPending"`
		TotalSkipped int    `json:"totalSkipped"`
		ProjectID    string `json:"projectId"`
		Branch       string `json:"branch"`
		CommitSha    string `json:"commitSha"`
	} `json:"run"`
}

func main() {
	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		log.Fatalf("failed to create event bus: %v", err)
	}
adapter := &CypressAdapter{
	apiKey:    os.Getenv("CYPRESS_API_KEY"),
	projectID: os.Getenv("CYPRESS_PROJECT_ID"),
	bus:       bus,
	httpClient: &http.Client{},
}
go adapter.subscribeToEvents()
mux := http.NewServeMux()
mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})
mux.HandleFunc("/webhook", adapter.HandleWebhook)
mux.HandleFunc("/api/v1/runs", adapter.HandleRuns)
mux.HandleFunc("/api/v1/instances", adapter.HandleInstances)
log.Printf("Cypress Cloud adapter listening on :19134")
http.ListenAndServe(":19134", mux)
}

func (a *CypressAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
// Verify shared secret if configured
if secret := os.Getenv("CYPRESS_WEBHOOK_SECRET"); secret != "" {
	if r.Header.Get("X-Cypress-Secret") != secret {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
}

var payload cypressRunWebhook
if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}
if payload.Event != "RUN_COMPLETED" {
	w.WriteHeader(http.StatusOK)
	return
}
switch payload.Run.Status {
	case "passed":
	ep, _ := json.Marshal(map[string]interface{}{
		"run_id":       payload.Run.ID,
		"project_id":   payload.Run.ProjectID,
		"branch":       payload.Run.Branch,
		"commit_sha":   payload.Run.CommitSha,
		"total_passed": payload.Run.TotalPassed,
		"source":       "cypress",
})
a.bus.Publish(r.Context(), events.Event{Type: events.TaskCompleted, Payload: ep})
case "failed", "errored":
ep, _ := json.Marshal(map[string]interface{}{
	"run_id":        payload.Run.ID,
	"project_id":    payload.Run.ProjectID,
	"branch":        payload.Run.Branch,
	"commit_sha":    payload.Run.CommitSha,
	"total_failed":  payload.Run.TotalFailed,
	"total_passed":  payload.Run.TotalPassed,
	"source":        "cypress",
})
a.bus.Publish(r.Context(), events.Event{Type: events.TaskBlocked, Payload: ep})
}
w.WriteHeader(http.StatusOK)
}

func (a *CypressAdapter) HandleRuns(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project_id")
	if projectID == "" {
		projectID = a.projectID
	}
switch r.Method {
	case http.MethodGet:

var result interface{}
if err := a.cyRequest(r.Context(), http.MethodGet, fmt.Sprintf("/projects/%s/runs", projectID), nil, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
default:
http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
}

func (a *CypressAdapter) HandleInstances(w http.ResponseWriter, r *http.Request) {
	instanceID := r.URL.Query().Get("instance_id")
	if instanceID == "" {
		http.Error(w, "instance_id query parameter is required", http.StatusBadRequest)
		return
	}
switch r.Method {
	case http.MethodGet:

var result interface{}
if err := a.cyRequest(r.Context(), http.MethodGet, fmt.Sprintf("/instances/%s", instanceID), nil, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
default:
http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
}

func (a *CypressAdapter) subscribeToEvents() {
	ctx := context.Background()
	a.bus.Subscribe(ctx, []events.EventType{events.ReviewApproved}, func(e events.Event) error {
		return nil
})
}

func (a *CypressAdapter) cyRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {

var bodyReader io.Reader
if body != nil {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
bodyReader = strings.NewReader(string(b))
}
req, err := http.NewRequestWithContext(ctx, method, cypressAPIBase+path, bodyReader)
if err != nil {
	return fmt.Errorf("create request: %w", err)
}
req.Header.Set("Authorization", "Bearer "+a.apiKey)
req.Header.Set("Content-Type", "application/json")
resp, err := a.httpClient.Do(req)
if err != nil {
	return fmt.Errorf("execute request: %w", err)
}
defer resp.Body.Close()
if resp.StatusCode >= 300 {
	b, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("cypress API error %d: %s", resp.StatusCode, string(b))
}
if out != nil {
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
}
return nil
}
