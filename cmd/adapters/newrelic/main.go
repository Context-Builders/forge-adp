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

const newRelicAPIBase = "https://api.newrelic.com/v2"

type NewRelicAdapter struct {
	apiKey     string
	accountID  string
	bus        events.Bus
	httpClient *http.Client
}

type newRelicAlertPayload struct {
	Severity      string `json:"severity"`
	State         string `json:"state"`
	PolicyName    string `json:"policy_name"`
	ConditionName string `json:"condition_name"`
	IncidentID    int    `json:"incident_id"`
	Details       string `json:"details"`
}

func main() {
	logger.Init("newrelic-adapter")

	apiKey := os.Getenv("NEWRELIC_API_KEY")
	if apiKey == "" {
		slog.Error("NEWRELIC_API_KEY is required")
		os.Exit(1)
	}

	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		slog.Error("failed to create event bus", slog.Any("error", err))
		os.Exit(1)
	}

	adapter := &NewRelicAdapter{
		apiKey:     apiKey,
		accountID:  os.Getenv("NEWRELIC_ACCOUNT_ID"),
		bus:        bus,
		httpClient: &http.Client{},
	}

	go adapter.subscribeToEvents()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/webhook", adapter.HandleWebhook)
	mux.HandleFunc("/api/v1/alerts", adapter.HandleAlerts)
	mux.HandleFunc("/api/v1/violations", adapter.HandleViolations)

	slog.Info("New Relic adapter listening", slog.String("addr", ":19116"))
	http.ListenAndServe(":19116", logger.HTTPMiddleware("newrelic-adapter", mux))
}

func (a *NewRelicAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload newRelicAlertPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	switch payload.State {
	case "open":
		if payload.Severity == "CRITICAL" || payload.Severity == "WARNING" {
			a.handleAlertOpened(r.Context(), payload)
		}
	case "closed":
		a.handleAlertClosed(r.Context(), payload)
	}
	w.WriteHeader(http.StatusOK)
}

func (a *NewRelicAdapter) handleAlertOpened(ctx context.Context, p newRelicAlertPayload) {
	ep, _ := json.Marshal(map[string]interface{}{
		"incident_id": p.IncidentID,
		"policy":      p.PolicyName,
		"condition":   p.ConditionName,
		"severity":    p.Severity,
		"details":     p.Details,
		"source":      "newrelic",
	})
	if err := a.bus.Publish(ctx, events.Event{Type: events.EscalationCreated, Payload: ep}); err != nil {
		slog.Error("failed to publish escalation event",
			slog.Int("incident_id", p.IncidentID),
			slog.Any("error", err))
	}
}

func (a *NewRelicAdapter) handleAlertClosed(ctx context.Context, p newRelicAlertPayload) {
	ep, _ := json.Marshal(map[string]interface{}{
		"incident_id": p.IncidentID,
		"policy":      p.PolicyName,
		"source":      "newrelic",
	})
	if err := a.bus.Publish(ctx, events.Event{Type: events.TaskCompleted, Payload: ep}); err != nil {
		slog.Error("failed to publish task completed event",
			slog.Int("incident_id", p.IncidentID),
			slog.Any("error", err))
	}
}

// HandleAlerts lists New Relic alert policies.
//
//	GET /api/v1/alerts
func (a *NewRelicAdapter) HandleAlerts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var result map[string]interface{}
	if err := a.nrRequest(r.Context(), http.MethodGet, "/alerts_policies.json", nil, &result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// HandleViolations lists open New Relic alert violations.
//
//	GET /api/v1/violations
func (a *NewRelicAdapter) HandleViolations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var result map[string]interface{}
	if err := a.nrRequest(r.Context(), http.MethodGet, "/alerts_violations.json?only_open=true", nil, &result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// subscribeToEvents listens for EscalationCreated and TaskFailed events and
// logs them so New Relic operators can correlate with alert policies.
func (a *NewRelicAdapter) subscribeToEvents() {
	ctx := context.Background()
	if err := a.bus.Subscribe(ctx, []events.EventType{
		events.EscalationCreated,
		events.TaskFailed,
	}, func(e events.Event) error {
		var payload struct {
			TaskID  string `json:"task_id"`
			JiraKey string `json:"jira_key"`
			Source  string `json:"source"`
		}
		json.Unmarshal(e.Payload, &payload)
		if payload.Source == "newrelic" {
			return nil // avoid loops
		}
		slog.Info("forge event received",
			slog.String("type", string(e.Type)),
			slog.String("task_id", payload.TaskID),
			slog.String("jira_key", payload.JiraKey))
		return nil
	}); err != nil {
		slog.Error("failed to subscribe to events", slog.Any("error", err))
	}
}

func (a *NewRelicAdapter) nrRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, newRelicAPIBase+path, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("X-Api-Key", a.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("new relic API error %d: %s", resp.StatusCode, string(b))
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
