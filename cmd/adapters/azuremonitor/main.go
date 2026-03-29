package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/dotrage/forge-adp/pkg/events"
	"github.com/dotrage/forge-adp/pkg/logger"
)

const azureDevOpsAPIBase = "https://dev.azure.com"

// AzureMonitorAdapter handles Azure Monitor action group webhooks (alerts) and
// Azure DevOps service hook events (pipeline runs, release events).
type AzureMonitorAdapter struct {
	organization string
	project      string
	pat          string
	bus          events.Bus
	httpClient   *http.Client
}

type azureMonitorAlert struct {
	SchemaID string `json:"schemaId"`
	Data     struct {
		Essentials struct {
			AlertID          string `json:"alertId"`
			AlertRule        string `json:"alertRule"`
			Severity         string `json:"severity"`
			MonitorCondition string `json:"monitorCondition"`
			TargetResource   string `json:"targetResource"`
			Description      string `json:"description"`
		} `json:"essentials"`
	} `json:"data"`
}

type adoPipelineEvent struct {
	EventType string `json:"eventType"`
	Resource  struct {
		ID     int    `json:"id"`
		Name   string `json:"name"`
		Result string `json:"result"`
		State  string `json:"state"`
		URL    string `json:"url"`
		Links  struct {
			Web struct {
				Href string `json:"href"`
			} `json:"web"`
		} `json:"_links"`
	} `json:"resource"`
}

func main() {
	logger.Init("azuremonitor-adapter")

	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		slog.Error("failed to create event bus", slog.Any("error", err))
		os.Exit(1)
	}

	adapter := &AzureMonitorAdapter{
		organization: os.Getenv("AZURE_DEVOPS_ORG"),
		project:      os.Getenv("AZURE_DEVOPS_PROJECT"),
		pat:          os.Getenv("AZURE_DEVOPS_PAT"),
		bus:          bus,
		httpClient:   &http.Client{},
	}

	go adapter.subscribeToEvents()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/webhook/monitor", adapter.HandleMonitorWebhook)
	mux.HandleFunc("/webhook/devops", adapter.HandleDevOpsWebhook)
	mux.HandleFunc("/api/v1/alerts", adapter.HandleAlerts)
	mux.HandleFunc("/api/v1/pipelines", adapter.HandlePipelines)

	slog.Info("Azure Monitor / Azure DevOps adapter listening", slog.String("addr", ":19119"))
	http.ListenAndServe(":19119", logger.HTTPMiddleware("azuremonitor-adapter", mux))
}

func (a *AzureMonitorAdapter) HandleMonitorWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var alert azureMonitorAlert
	if err := json.NewDecoder(r.Body).Decode(&alert); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ess := alert.Data.Essentials
	switch ess.MonitorCondition {
	case "Fired":
		ep, _ := json.Marshal(map[string]interface{}{
			"alert_id":    ess.AlertID,
			"rule":        ess.AlertRule,
			"severity":    ess.Severity,
			"resource":    ess.TargetResource,
			"description": ess.Description,
			"source":      "azure_monitor",
		})
		if err := a.bus.Publish(r.Context(), events.Event{Type: events.EscalationCreated, Payload: ep}); err != nil {
			slog.Error("failed to publish escalation event",
				slog.String("alert_id", ess.AlertID),
				slog.Any("error", err))
		}
	case "Resolved":
		ep, _ := json.Marshal(map[string]interface{}{
			"alert_id": ess.AlertID,
			"rule":     ess.AlertRule,
			"source":   "azure_monitor",
		})
		if err := a.bus.Publish(r.Context(), events.Event{Type: events.TaskCompleted, Payload: ep}); err != nil {
			slog.Error("failed to publish task completed event",
				slog.String("alert_id", ess.AlertID),
				slog.Any("error", err))
		}
	}
	w.WriteHeader(http.StatusOK)
}

func (a *AzureMonitorAdapter) HandleDevOpsWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload adoPipelineEvent
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	switch payload.EventType {
	case "build.complete":
		switch payload.Resource.Result {
		case "succeeded":
			ep, _ := json.Marshal(map[string]interface{}{
				"build_id": payload.Resource.ID,
				"name":     payload.Resource.Name,
				"url":      payload.Resource.Links.Web.Href,
				"source":   "azure_devops",
			})
			a.bus.Publish(r.Context(), events.Event{Type: events.TaskCompleted, Payload: ep})
		case "failed", "canceled":
			ep, _ := json.Marshal(map[string]interface{}{
				"build_id": payload.Resource.ID,
				"name":     payload.Resource.Name,
				"result":   payload.Resource.Result,
				"source":   "azure_devops",
			})
			a.bus.Publish(r.Context(), events.Event{Type: events.TaskFailed, Payload: ep})
		}
	}
	w.WriteHeader(http.StatusOK)
}

// HandleAlerts returns a hint about using the Azure Monitor REST API directly.
//
//	GET /api/v1/alerts
func (a *AzureMonitorAdapter) HandleAlerts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	result := map[string]interface{}{
		"message": "Use Azure Monitor REST API with subscription credentials",
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// HandlePipelines lists Azure DevOps pipelines for the configured org/project.
//
//	GET /api/v1/pipelines
func (a *AzureMonitorAdapter) HandlePipelines(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	path := fmt.Sprintf("/%s/%s/_apis/pipelines?api-version=7.0", a.organization, a.project)
	var result map[string]interface{}
	if err := a.adoRequest(r.Context(), http.MethodGet, path, nil, &result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// subscribeToEvents listens for DeploymentRequested events and triggers an
// Azure DevOps pipeline run for the pipeline named in the payload.
func (a *AzureMonitorAdapter) subscribeToEvents() {
	ctx := context.Background()
	if err := a.bus.Subscribe(ctx, []events.EventType{events.DeploymentRequested}, func(e events.Event) error {
		var payload struct {
			PipelineID string `json:"pipeline_id"`
			Branch     string `json:"branch"`
		}
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			return fmt.Errorf("unmarshal deployment payload: %w", err)
		}
		if payload.PipelineID == "" || a.organization == "" || a.project == "" {
			return nil // not enough info to trigger a pipeline
		}

		branch := payload.Branch
		if branch == "" {
			branch = "main"
		}
		slog.Info("triggering ADO pipeline from deployment event",
			slog.String("pipeline_id", payload.PipelineID),
			slog.String("branch", branch),
			slog.String("task_id", e.TaskID))

		body := map[string]interface{}{
			"resources": map[string]interface{}{
				"repositories": map[string]interface{}{
					"self": map[string]interface{}{
						"refName": "refs/heads/" + branch,
					},
				},
			},
		}
		path := fmt.Sprintf("/%s/%s/_apis/pipelines/%s/runs?api-version=7.0",
			a.organization, a.project, payload.PipelineID)
		var result map[string]interface{}
		if err := a.adoRequest(ctx, http.MethodPost, path, body, &result); err != nil {
			return fmt.Errorf("trigger ADO pipeline %s: %w", payload.PipelineID, err)
		}
		return nil
	}); err != nil {
		slog.Error("failed to subscribe to deployment events", slog.Any("error", err))
	}
}

func (a *AzureMonitorAdapter) adoRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, azureDevOpsAPIBase+path, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.SetBasicAuth("", a.pat)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("azure devops API error %d: %s", resp.StatusCode, string(b))
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
