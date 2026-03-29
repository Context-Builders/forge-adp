package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"

	"github.com/dotrage/forge-adp/pkg/events"
	"github.com/dotrage/forge-adp/pkg/logger"
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
	ID          string `json:"id"`
	ProjectSlug string `json:"project_slug"`
}

type circleCIWebhookPayload struct {
	Type     string           `json:"type"`
	Workflow circleCIWorkflow `json:"workflow"`
	Pipeline circleCIPipeline `json:"pipeline"`
}

func main() {
	logger.Init("circleci-adapter")

	token := os.Getenv("CIRCLECI_TOKEN")
	if token == "" {
		slog.Error("CIRCLECI_TOKEN is required")
		os.Exit(1)
	}

	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		slog.Error("failed to create event bus", slog.Any("error", err))
		os.Exit(1)
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

	slog.Info("CircleCI adapter listening", slog.String("addr", ":19112"))
	http.ListenAndServe(":19112", logger.HTTPMiddleware("circleci-adapter", mux))
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
		slog.Error("failed to publish task completed event",
			slog.String("workflow_id", p.Workflow.ID),
			slog.Any("error", err))
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
		slog.Error("failed to publish task failed event",
			slog.String("workflow_id", p.Workflow.ID),
			slog.Any("error", err))
	}
}

// HandlePipelines lists pipelines for a CircleCI project.
//
//	GET /api/v1/pipelines?project_slug=<slug>
func (a *CircleCIAdapter) HandlePipelines(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	projectSlug := r.URL.Query().Get("project_slug")
	if projectSlug == "" {
		http.Error(w, "project_slug query parameter is required", http.StatusBadRequest)
		return
	}
	var result map[string]interface{}
	if err := a.circleRequest(r.Context(), http.MethodGet,
		fmt.Sprintf("/project/%s/pipeline", projectSlug), nil, &result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// HandleWorkflows lists workflows for a CircleCI pipeline.
//
//	GET /api/v1/workflows?pipeline_id=<id>
func (a *CircleCIAdapter) HandleWorkflows(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	pipelineID := r.URL.Query().Get("pipeline_id")
	if pipelineID == "" {
		http.Error(w, "pipeline_id query parameter is required", http.StatusBadRequest)
		return
	}
	var result map[string]interface{}
	if err := a.circleRequest(r.Context(), http.MethodGet,
		fmt.Sprintf("/pipeline/%s/workflow", pipelineID), nil, &result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// subscribeToEvents listens for DeploymentRequested events and triggers a
// CircleCI pipeline for the project named in the payload.
func (a *CircleCIAdapter) subscribeToEvents() {
	ctx := context.Background()
	if err := a.bus.Subscribe(ctx, []events.EventType{events.DeploymentRequested}, func(e events.Event) error {
		var payload struct {
			ProjectSlug string `json:"project_slug"`
			Branch      string `json:"branch"`
		}
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			return fmt.Errorf("unmarshal deployment payload: %w", err)
		}
		if payload.ProjectSlug == "" {
			return nil // no project — nothing to trigger
		}

		branch := payload.Branch
		if branch == "" {
			branch = "main"
		}
		slog.Info("triggering CircleCI pipeline from deployment event",
			slog.String("project_slug", payload.ProjectSlug),
			slog.String("branch", branch),
			slog.String("task_id", e.TaskID))

		body := map[string]interface{}{"branch": branch}
		var result map[string]interface{}
		if err := a.circleRequest(ctx, http.MethodPost,
			fmt.Sprintf("/project/%s/pipeline", payload.ProjectSlug), body, &result); err != nil {
			return fmt.Errorf("trigger CircleCI pipeline for %s: %w", payload.ProjectSlug, err)
		}
		return nil
	}); err != nil {
		slog.Error("failed to subscribe to deployment events", slog.Any("error", err))
	}
}

func (a *CircleCIAdapter) circleRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(b)
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
