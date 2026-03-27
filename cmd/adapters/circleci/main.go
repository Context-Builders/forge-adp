package main

import (
	"github.com/dotrage/forge-adp/pkg/events"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
)

const circleAPIBase = "https://circleci.com/api/v2"

type CircleCIAdapter struct {
	token         string
	webhookSecret string
	bus           events.Bus
	httpClient    *http.Client
}

type circleCIWorkflow struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type circleCIPipeline struct {
	ID         string `json:"id"`
	ProjectSlug string `json:"project_slug"`
}

type circleCIWebhookPayload struct {
	Type     string           `json:"type"`
	Workflow circleCIWorkflow `json:"workflow"`
	Pipeline circleCIPipeline `json:"pipeline"`
}

func main() {
	token := os.Getenv("CIRCLECI_TOKEN")
	if token == "" {
		log.Fatal("CIRCLECI_TOKEN is required")
	}
bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
if err != nil {
	log.Fatalf("failed to create event bus: %v", err)
}
adapter := &CircleCIAdapter{
	token:         token,
	webhookSecret: os.Getenv("CIRCLECI_WEBHOOK_SECRET"),
	bus:           bus,
	httpClient:    &http.Client{},
}
go adapter.subscribeToEvents()
mux := http.NewServeMux()
mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})
mux.HandleFunc("/webhook", adapter.HandleWebhook)
mux.HandleFunc("/api/v1/pipelines", adapter.HandlePipelines)
mux.HandleFunc("/api/v1/workflows", adapter.HandleWorkflows)
log.Printf("CircleCI adapter listening on :19112")
http.ListenAndServe(":19112", mux)
}

func (a *CircleCIAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
body, err := io.ReadAll(r.Body)
if err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}
if a.webhookSecret != "" {
	sig := r.Header.Get("Circleci-Signature")
	mac := hmac.New(sha256.New, []byte(a.webhookSecret))
	mac.Write(body)
	expected := "v1=" + hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(sig)) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
}

var payload circleCIWebhookPayload
if err := json.Unmarshal(body, &payload); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}
if payload.Type == "workflow-completed" {
	switch payload.Workflow.Status {
		case "success":
		a.handleWorkflowSuccess(r.Context(), payload)
		case "failed", "error", "canceled", "unauthorized":
		a.handleWorkflowFailed(r.Context(), payload)
	}
}
w.WriteHeader(http.StatusOK)
}

func (a *CircleCIAdapter) handleWorkflowSuccess(ctx context.Context, p circleCIWebhookPayload) {
	ep, _ := json.Marshal(map[string]interface{}{
		"workflow_id":   p.Workflow.ID,
		"workflow_name": p.Workflow.Name,
		"pipeline_id":   p.Pipeline.ID,
		"project":       p.Pipeline.ProjectSlug,
		"source":        "circleci",
})
if err := a.bus.Publish(ctx, events.Event{Type: events.TaskCompleted, Payload: ep}); err != nil {
	log.Printf("failed to publish task completed event: %v", err)
}
}

func (a *CircleCIAdapter) handleWorkflowFailed(ctx context.Context, p circleCIWebhookPayload) {
	ep, _ := json.Marshal(map[string]interface{}{
		"workflow_id":   p.Workflow.ID,
		"workflow_name": p.Workflow.Name,
		"status":        p.Workflow.Status,
		"pipeline_id":   p.Pipeline.ID,
		"project":       p.Pipeline.ProjectSlug,
		"source":        "circleci",
})
if err := a.bus.Publish(ctx, events.Event{Type: events.TaskFailed, Payload: ep}); err != nil {
	log.Printf("failed to publish task failed event: %v", err)
}
}

func (a *CircleCIAdapter) HandlePipelines(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
		case http.MethodGet:
		projectSlug := r.URL.Query().Get("project_slug")
		if projectSlug == "" {
			http.Error(w, "project_slug query parameter is required", http.StatusBadRequest)
			return
		}

var result map[string]interface{}
if err := a.circleRequest(r.Context(), http.MethodGet, fmt.Sprintf("/project/%s/pipeline", projectSlug), nil, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
default:
http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
}

func (a *CircleCIAdapter) HandleWorkflows(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
		case http.MethodGet:
		pipelineID := r.URL.Query().Get("pipeline_id")
		if pipelineID == "" {
			http.Error(w, "pipeline_id query parameter is required", http.StatusBadRequest)
			return
		}

var result map[string]interface{}
if err := a.circleRequest(r.Context(), http.MethodGet, fmt.Sprintf("/pipeline/%s/workflow", pipelineID), nil, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
default:
http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
}

func (a *CircleCIAdapter) subscribeToEvents() {
	ctx := context.Background()
	a.bus.Subscribe(ctx, []events.EventType{events.DeploymentRequested}, func(e events.Event) error {
		return nil
})
}

func (a *CircleCIAdapter) circleRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {

var bodyReader io.Reader
if body != nil {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
bodyReader = strings.NewReader(string(b))
}
req, err := http.NewRequestWithContext(ctx, method, circleAPIBase+path, bodyReader)
if err != nil {
	return fmt.Errorf("create request: %w", err)
}
req.Header.Set("Circle-Token", a.token)
req.Header.Set("Content-Type", "application/json")
resp, err := a.httpClient.Do(req)
if err != nil {
	return fmt.Errorf("execute request: %w", err)
}
defer resp.Body.Close()
if resp.StatusCode >= 300 {
	b, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("circleci API error %d: %s", resp.StatusCode, string(b))
}
if out != nil {
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
}
return nil
}
