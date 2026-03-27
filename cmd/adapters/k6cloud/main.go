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

const k6APIBase = "https://api.k6.io/v3"

type K6Adapter struct {
	apiToken   string
	projectID  string
	bus        events.Bus
	httpClient *http.Client
}

type k6RunWebhook struct {
	Event struct {
		Type    string `json:"type"`
		TestRun struct {
			ID           int    `json:"id"`
			Name         string `json:"name"`
			Status       string `json:"status"`
			ResultStatus string `json:"result_status"`
			ProjectID    int    `json:"project_id"`
			ThresholdsResults []struct {
				Name   string `json:"name"`
				Passed bool   `json:"passed"`
			} `json:"thresholds_results"`
		} `json:"test_run"`
	} `json:"event"`
}

func main() {
	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		log.Fatalf("failed to create event bus: %v", err)
	}
adapter := &K6Adapter{
	apiToken:  os.Getenv("K6_CLOUD_API_TOKEN"),
	projectID: os.Getenv("K6_PROJECT_ID"),
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
mux.HandleFunc("/api/v1/thresholds", adapter.HandleThresholds)
log.Printf("k6 Cloud adapter listening on :19135")
http.ListenAndServe(":19135", mux)
}

func (a *K6Adapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

var payload k6RunWebhook
if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}
switch payload.Event.Type {
	case "TEST_FINISHED":
	run := payload.Event.TestRun
// Identify breached thresholds

var breached []string
for _, t := range run.ThresholdsResults {
	if !t.Passed {
		breached = append(breached, t.Name)
	}
}
if run.ResultStatus == "passed" && len(breached) == 0 {
	ep, _ := json.Marshal(map[string]interface{}{
		"run_id":     run.ID,
		"run_name":   run.Name,
		"project_id": run.ProjectID,
		"source":     "k6cloud",
})
a.bus.Publish(r.Context(), events.Event{Type: events.TaskCompleted, Payload: ep})
} else {
ep, _ := json.Marshal(map[string]interface{}{
	"run_id":             run.ID,
	"run_name":           run.Name,
	"project_id":         run.ProjectID,
	"result_status":      run.ResultStatus,
	"breached_thresholds": breached,
	"source":             "k6cloud",
})
a.bus.Publish(r.Context(), events.Event{Type: events.EscalationCreated, Payload: ep})
}
case "TEST_ABORTED":
run := payload.Event.TestRun
ep, _ := json.Marshal(map[string]interface{}{
	"run_id":     run.ID,
	"run_name":   run.Name,
	"project_id": run.ProjectID,
	"source":     "k6cloud",
})
a.bus.Publish(r.Context(), events.Event{Type: events.TaskFailed, Payload: ep})
}
w.WriteHeader(http.StatusOK)
}

func (a *K6Adapter) HandleRuns(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
		case http.MethodGet:
		projectID := r.URL.Query().Get("project_id")
		if projectID == "" {
			projectID = a.projectID
		}

var result interface{}
if err := a.k6Request(r.Context(), http.MethodGet, fmt.Sprintf("/test-runs?project_id=%s", projectID), nil, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
case http.MethodPost:
// Trigger a k6 Cloud test run

var body interface{}
if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}

var result interface{}
if err := a.k6Request(r.Context(), http.MethodPost, "/test-runs", body, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
w.WriteHeader(http.StatusCreated)
json.NewEncoder(w).Encode(result)
default:
http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
}

func (a *K6Adapter) HandleThresholds(w http.ResponseWriter, r *http.Request) {
	runID := r.URL.Query().Get("run_id")
	if runID == "" {
		http.Error(w, "run_id query parameter is required", http.StatusBadRequest)
		return
	}
switch r.Method {
	case http.MethodGet:

var result interface{}
if err := a.k6Request(r.Context(), http.MethodGet, fmt.Sprintf("/test-runs/%s/thresholds", runID), nil, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
default:
http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
}

func (a *K6Adapter) subscribeToEvents() {
	ctx := context.Background()
	a.bus.Subscribe(ctx, []events.EventType{events.DeploymentApproved}, func(e events.Event) error {
// On deployment approval, QA/SRE agents may trigger a load test run
return nil
})
}

func (a *K6Adapter) k6Request(ctx context.Context, method, path string, body interface{}, out interface{}) error {

var bodyReader io.Reader
if body != nil {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
bodyReader = strings.NewReader(string(b))
}
req, err := http.NewRequestWithContext(ctx, method, k6APIBase+path, bodyReader)
if err != nil {
	return fmt.Errorf("create request: %w", err)
}
req.Header.Set("Authorization", "Bearer "+a.apiToken)
req.Header.Set("Content-Type", "application/json")
resp, err := a.httpClient.Do(req)
if err != nil {
	return fmt.Errorf("execute request: %w", err)
}
defer resp.Body.Close()
if resp.StatusCode >= 300 {
	b, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("k6 cloud API error %d: %s", resp.StatusCode, string(b))
}
if out != nil {
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
}
return nil
}
