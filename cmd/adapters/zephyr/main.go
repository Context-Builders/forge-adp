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

const zephyrAPIBase = "https://api.zephyrscale.smartbear.com/v2"// bridge for the QA agent to create/report test cycles directly.// API. It receives test cycle and execution result webhooks and exposes a REST// Zephyr Scale adapter integrates with the Zephyr Scale (TM4J) test management)

type ZephyrAdapter struct {
	apiToken   string
	projectKey string
	bus        events.Bus
	httpClient *http.Client
}

type zephyrWebhookPayload struct {
	WebhookEvent string `json:"webhookEvent"`
	TestCycle    struct {
		ID         string `json:"id"`
		Key        string `json:"key"`
		Name       string `json:"name"`
		Status     string `json:"status"`
		ProjectKey string `json:"projectKey"`
	} `json:"testCycle"`
TestExecution struct {
	ID          string `json:"id"`
	Key         string `json:"key"`
	StatusName  string `json:"statusName"`
	TestCaseKey string `json:"testCaseKey"`
} `json:"testExecution"`
}

func main() {
	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		log.Fatalf("failed to create event bus: %v", err)
	}
adapter := &ZephyrAdapter{
	apiToken:   os.Getenv("ZEPHYR_API_TOKEN"),
	projectKey: os.Getenv("ZEPHYR_PROJECT_KEY"),
	bus:        bus,
	httpClient: &http.Client{},
}
go adapter.subscribeToEvents()
mux := http.NewServeMux()
mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})
mux.HandleFunc("/webhook", adapter.HandleWebhook)
mux.HandleFunc("/api/v1/cycles", adapter.HandleCycles)
mux.HandleFunc("/api/v1/executions", adapter.HandleExecutions)
mux.HandleFunc("/api/v1/cases", adapter.HandleTestCases)
log.Printf("Zephyr Scale adapter listening on :19132")
http.ListenAndServe(":19132", mux)
}

func (a *ZephyrAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
// Authenticate via shared secret header
if secret := os.Getenv("ZEPHYR_WEBHOOK_SECRET"); secret != "" {
	if r.Header.Get("X-Zephyr-Secret") != secret {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
}

var payload zephyrWebhookPayload
if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}
switch payload.WebhookEvent {
	case "testCycle_updated":
	switch strings.ToUpper(payload.TestCycle.Status) {
		case "DONE", "PASSED":
		ep, _ := json.Marshal(map[string]interface{}{
			"cycle_key":   payload.TestCycle.Key,
			"cycle_name":  payload.TestCycle.Name,
			"project_key": payload.TestCycle.ProjectKey,
			"source":      "zephyr",
	})
a.bus.Publish(r.Context(), events.Event{Type: events.TaskCompleted, Payload: ep})
case "FAILED":
ep, _ := json.Marshal(map[string]interface{}{
	"cycle_key":   payload.TestCycle.Key,
	"cycle_name":  payload.TestCycle.Name,
	"project_key": payload.TestCycle.ProjectKey,
	"source":      "zephyr",
})
a.bus.Publish(r.Context(), events.Event{Type: events.TaskBlocked, Payload: ep})
}
case "testExecution_updated":
if strings.ToUpper(payload.TestExecution.StatusName) == "FAIL" {
	ep, _ := json.Marshal(map[string]interface{}{
		"execution_key": payload.TestExecution.Key,
		"test_case_key": payload.TestExecution.TestCaseKey,
		"source":        "zephyr",
})
a.bus.Publish(r.Context(), events.Event{Type: events.EscalationCreated, Payload: ep})
}
}
w.WriteHeader(http.StatusOK)
}

func (a *ZephyrAdapter) HandleCycles(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
		case http.MethodGet:
		projectKey := r.URL.Query().Get("project_key")
		if projectKey == "" {
			projectKey = a.projectKey
		}

var result interface{}
if err := a.zephyrRequest(r.Context(), http.MethodGet, fmt.Sprintf("/testcycles?projectKey=%s", projectKey), nil, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
case http.MethodPost:

var body interface{}
if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}

var result interface{}
if err := a.zephyrRequest(r.Context(), http.MethodPost, "/testcycles", body, &result); err != nil {
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

func (a *ZephyrAdapter) HandleExecutions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
		case http.MethodGet:
		cycleKey := r.URL.Query().Get("cycle_key")
		if cycleKey == "" {
			http.Error(w, "cycle_key query parameter is required", http.StatusBadRequest)
			return
		}

var result interface{}
if err := a.zephyrRequest(r.Context(), http.MethodGet, fmt.Sprintf("/testexecutions?testCycle=%s", cycleKey), nil, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
case http.MethodPost:

var body interface{}
if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}

var result interface{}
if err := a.zephyrRequest(r.Context(), http.MethodPost, "/testexecutions", body, &result); err != nil {
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

func (a *ZephyrAdapter) HandleTestCases(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
		case http.MethodGet:
		projectKey := r.URL.Query().Get("project_key")
		if projectKey == "" {
			projectKey = a.projectKey
		}

var result interface{}
if err := a.zephyrRequest(r.Context(), http.MethodGet, fmt.Sprintf("/testcases?projectKey=%s", projectKey), nil, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
default:
http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
}

func (a *ZephyrAdapter) subscribeToEvents() {
	ctx := context.Background()
	a.bus.Subscribe(ctx, []events.EventType{events.ReviewApproved}, func(e events.Event) error {
		return nil
})
}

func (a *ZephyrAdapter) zephyrRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {

var bodyReader io.Reader
if body != nil {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
bodyReader = strings.NewReader(string(b))
}
req, err := http.NewRequestWithContext(ctx, method, zephyrAPIBase+path, bodyReader)
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
	return fmt.Errorf("zephyr API error %d: %s", resp.StatusCode, string(b))
}
if out != nil {
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
}
return nil
}
