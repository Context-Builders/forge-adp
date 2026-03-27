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

const databricksAPIPath = "/api/2.0"

type DatabricksAdapter struct {
	host       string
	token      string
	bus        events.Bus
	httpClient *http.Client
}

type databricksJobRun struct {
	RunID       int64  `json:"run_id"`
	State       struct {
		LifeCycleState  string `json:"life_cycle_state"`
		ResultState     string `json:"result_state"`
		StateMessage    string `json:"state_message"`
	} `json:"state"`
JobID       int64  `json:"job_id"`
RunName     string `json:"run_name"`
RunPageURL  string `json:"run_page_url"`
}

type databricksWebhookPayload struct {
	EventType string           `json:"event_type"`
	Run       databricksJobRun `json:"run"`
}

func main() {
	host := os.Getenv("DATABRICKS_HOST")
	token := os.Getenv("DATABRICKS_TOKEN")
	if host == "" || token == "" {
		log.Fatal("DATABRICKS_HOST and DATABRICKS_TOKEN are required")
	}
bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
if err != nil {
	log.Fatalf("failed to create event bus: %v", err)
}
adapter := &DatabricksAdapter{
	host:       strings.TrimRight(host, "/"),
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
mux.HandleFunc("/api/v1/jobs", adapter.HandleJobs)
mux.HandleFunc("/api/v1/runs", adapter.HandleRuns)
log.Printf("Databricks adapter listening on :19107")
http.ListenAndServe(":19107", mux)
}

func (a *DatabricksAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

var payload databricksWebhookPayload
if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}
switch payload.Run.State.LifeCycleState {
	case "TERMINATED":
	if payload.Run.State.ResultState == "SUCCESS" {
		a.handleRunSuccess(r.Context(), payload)
	} else {
	a.handleRunFailed(r.Context(), payload)
}
case "INTERNAL_ERROR", "SKIPPED":
a.handleRunFailed(r.Context(), payload)
}
w.WriteHeader(http.StatusOK)
}

func (a *DatabricksAdapter) handleRunSuccess(ctx context.Context, p databricksWebhookPayload) {
	ep, _ := json.Marshal(map[string]interface{}{
		"run_id":      p.Run.RunID,
		"job_id":      p.Run.JobID,
		"run_name":    p.Run.RunName,
		"run_page_url": p.Run.RunPageURL,
		"source":      "databricks",
})
if err := a.bus.Publish(ctx, events.Event{Type: events.TaskCompleted, Payload: ep}); err != nil {
	log.Printf("failed to publish task completed event: %v", err)
}
}

func (a *DatabricksAdapter) handleRunFailed(ctx context.Context, p databricksWebhookPayload) {
	ep, _ := json.Marshal(map[string]interface{}{
		"run_id":        p.Run.RunID,
		"job_id":        p.Run.JobID,
		"state_message": p.Run.State.StateMessage,
		"source":        "databricks",
})
if err := a.bus.Publish(ctx, events.Event{Type: events.TaskFailed, Payload: ep}); err != nil {
	log.Printf("failed to publish task failed event: %v", err)
}
}

func (a *DatabricksAdapter) HandleJobs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
		case http.MethodGet:

var result map[string]interface{}
if err := a.dbRequest(r.Context(), http.MethodGet, "/jobs/list", nil, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
default:
http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
}

func (a *DatabricksAdapter) HandleRuns(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
		case http.MethodGet:

var result map[string]interface{}
if err := a.dbRequest(r.Context(), http.MethodGet, "/jobs/runs/list", nil, &result); err != nil {
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

var result map[string]interface{}
if err := a.dbRequest(r.Context(), http.MethodPost, "/jobs/run-now", req, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
default:
http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
}

func (a *DatabricksAdapter) subscribeToEvents() {
	ctx := context.Background()
	a.bus.Subscribe(ctx, []events.EventType{events.TaskCreated}, func(e events.Event) error {
		return nil
})
}

func (a *DatabricksAdapter) dbRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {

var bodyReader io.Reader
if body != nil {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
bodyReader = strings.NewReader(string(b))
}
req, err := http.NewRequestWithContext(ctx, method, a.host+databricksAPIPath+path, bodyReader)
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
	return fmt.Errorf("databricks API error %d: %s", resp.StatusCode, string(b))
}
if out != nil {
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
}
return nil
}
