package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"

	"github.com/dotrage/forge-adp/pkg/events"
	"github.com/dotrage/forge-adp/pkg/logger"
)

const bridgecrewAPIBase = "https://www.bridgecrew.cloud/api/v1"

// CheckovAdapter receives IaC and container security scan results from Checkov
// and Trivy. Checkov posts results via a webhook or the Bridgecrew platform API.
// Trivy results are ingested as SARIF or JSON from CI pipeline artifacts.
type CheckovAdapter struct {
	bridgecrewToken string
	webhookSecret   string
	bus             events.Bus
	httpClient      *http.Client
}

type checkCovViolation struct {
	PolicyID    string `json:"policy_id"`
	Title       string `json:"title"`
	Severity    string `json:"severity"`
	Resource    string `json:"resource"`
	FilePath    string `json:"file_path"`
	LineNumber  int    `json:"line_from"`
	Description string `json:"description"`
}

type checkCovScanResult struct {
	RepoID     string              `json:"repo_id"`
	Branch     string              `json:"branch"`
	Passed     int                 `json:"passed"`
	Failed     int                 `json:"failed"`
	Violations []checkCovViolation `json:"violations"`
}

type trivySARIFResult struct {
	Schema string `json:"$schema"`
	Runs   []struct {
		Results []struct {
			RuleID  string `json:"ruleId"`
			Level   string `json:"level"`
			Message struct {
				Text string `json:"text"`
			} `json:"message"`
			Locations []struct {
				PhysicalLocation struct {
					ArtifactLocation struct {
						URI string `json:"uri"`
					} `json:"artifactLocation"`
				} `json:"physicalLocation"`
			} `json:"locations"`
		} `json:"results"`
	} `json:"runs"`
}

func main() {
	logger.Init("checkov-adapter")

	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		slog.Error("failed to create event bus", slog.Any("error", err))
		os.Exit(1)
	}

	adapter := &CheckovAdapter{
		bridgecrewToken: os.Getenv("BRIDGECREW_API_TOKEN"),
		webhookSecret:   os.Getenv("CHECKOV_WEBHOOK_SECRET"),
		bus:             bus,
		httpClient:      &http.Client{},
	}

	go adapter.subscribeToEvents()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/webhook/checkov", adapter.HandleCheckovWebhook)
	mux.HandleFunc("/webhook/trivy", adapter.HandleTrivyWebhook)
	mux.HandleFunc("/api/v1/violations", adapter.HandleViolations)
	mux.HandleFunc("/api/v1/suppressed", adapter.HandleSuppressed)

	slog.Info("Checkov / Trivy adapter listening", slog.String("addr", ":19123"))
	http.ListenAndServe(":19123", logger.HTTPMiddleware("checkov-adapter", mux))
}

func (a *CheckovAdapter) HandleCheckovWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var result checkCovScanResult
	if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	criticalOrHigh := 0
	for _, v := range result.Violations {
		if v.Severity == "CRITICAL" || v.Severity == "HIGH" {
			criticalOrHigh++
		}
	}

	if criticalOrHigh > 0 {
		ep, _ := json.Marshal(map[string]interface{}{
			"repo":          result.RepoID,
			"branch":        result.Branch,
			"failed":        result.Failed,
			"critical_high": criticalOrHigh,
			"source":        "checkov",
		})
		if err := a.bus.Publish(r.Context(), events.Event{Type: events.EscalationCreated, Payload: ep}); err != nil {
			slog.Error("failed to publish escalation event",
				slog.String("repo", result.RepoID),
				slog.Any("error", err))
		}
	} else if result.Failed > 0 {
		ep, _ := json.Marshal(map[string]interface{}{
			"repo":   result.RepoID,
			"branch": result.Branch,
			"failed": result.Failed,
			"source": "checkov",
		})
		if err := a.bus.Publish(r.Context(), events.Event{Type: events.TaskBlocked, Payload: ep}); err != nil {
			slog.Error("failed to publish task blocked event",
				slog.String("repo", result.RepoID),
				slog.Any("error", err))
		}
	} else {
		ep, _ := json.Marshal(map[string]interface{}{
			"repo":   result.RepoID,
			"branch": result.Branch,
			"passed": result.Passed,
			"source": "checkov",
		})
		if err := a.bus.Publish(r.Context(), events.Event{Type: events.TaskCompleted, Payload: ep}); err != nil {
			slog.Error("failed to publish task completed event",
				slog.String("repo", result.RepoID),
				slog.Any("error", err))
		}
	}
	w.WriteHeader(http.StatusOK)
}

func (a *CheckovAdapter) HandleTrivyWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var sarif trivySARIFResult
	if err := json.NewDecoder(r.Body).Decode(&sarif); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	errorCount := 0
	for _, run := range sarif.Runs {
		for _, res := range run.Results {
			if res.Level == "error" {
				errorCount++
			}
		}
	}

	if errorCount > 0 {
		ep, _ := json.Marshal(map[string]interface{}{
			"error_count": errorCount,
			"source":      "trivy",
		})
		if err := a.bus.Publish(r.Context(), events.Event{Type: events.EscalationCreated, Payload: ep}); err != nil {
			slog.Error("failed to publish escalation event",
				slog.Int("error_count", errorCount),
				slog.Any("error", err))
		}
	} else {
		ep, _ := json.Marshal(map[string]interface{}{
			"source": "trivy",
			"status": "clean",
		})
		if err := a.bus.Publish(r.Context(), events.Event{Type: events.TaskCompleted, Payload: ep}); err != nil {
			slog.Error("failed to publish task completed event", slog.Any("error", err))
		}
	}
	w.WriteHeader(http.StatusOK)
}

// HandleViolations lists violations from the Bridgecrew platform.
//
//	GET /api/v1/violations
func (a *CheckovAdapter) HandleViolations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var result interface{}
	if err := a.bcRequest(r.Context(), http.MethodGet, "/violations/resources", nil, &result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// HandleSuppressed lists suppressed violations from the Bridgecrew platform.
//
//	GET /api/v1/suppressed
func (a *CheckovAdapter) HandleSuppressed(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var result interface{}
	if err := a.bcRequest(r.Context(), http.MethodGet, "/suppressions", nil, &result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// subscribeToEvents listens for DeploymentRequested events. When IaC is about to
// deploy, the security agent is notified so it can run Checkov pre-checks.
func (a *CheckovAdapter) subscribeToEvents() {
	ctx := context.Background()
	if err := a.bus.Subscribe(ctx, []events.EventType{events.DeploymentRequested}, func(e events.Event) error {
		var payload struct {
			Repo   string `json:"repo"`
			Branch string `json:"branch"`
		}
		json.Unmarshal(e.Payload, &payload)
		if payload.Repo != "" {
			slog.Info("deployment requested — Checkov scan should be triggered",
				slog.String("repo", payload.Repo),
				slog.String("branch", payload.Branch),
				slog.String("task_id", e.TaskID))
		}
		return nil
	}); err != nil {
		slog.Error("failed to subscribe to deployment requested events", slog.Any("error", err))
	}
}

func (a *CheckovAdapter) bcRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, bridgecrewAPIBase+path, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", a.bridgecrewToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("bridgecrew API error %d: %s", resp.StatusCode, string(b))
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
