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
	"time"

	"github.com/dotrage/forge-adp/pkg/events"
	"github.com/dotrage/forge-adp/pkg/logger"
)

type GrafanaAdapter struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	bus        events.Bus
}

// Grafana Alertmanager webhook payload structures (unified alerting).
type grafanaWebhookPayload struct {
	Receiver          string            `json:"receiver"`
	Status            string            `json:"status"` // "firing" or "resolved"
	Alerts            []grafanaAlert    `json:"alerts"`
	GroupLabels       map[string]string `json:"groupLabels"`
	CommonLabels      map[string]string `json:"commonLabels"`
	CommonAnnotations map[string]string `json:"commonAnnotations"`
	ExternalURL       string            `json:"externalURL"`
}

type grafanaAlert struct {
	Status       string            `json:"status"` // "firing" or "resolved"
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     string            `json:"startsAt"`
	EndsAt       string            `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Fingerprint  string            `json:"fingerprint"`
	SilenceURL   string            `json:"silenceURL"`
	DashboardURL string            `json:"dashboardURL"`
	PanelURL     string            `json:"panelURL"`
	ValueString  string            `json:"valueString"`
}

// Grafana REST API request/response types.
type grafanaAnnotationRequest struct {
	DashboardUID string   `json:"dashboardUID,omitempty"`
	PanelID      int      `json:"panelId,omitempty"`
	Time         int64    `json:"time"`
	TimeEnd      int64    `json:"timeEnd,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	Text         string   `json:"text"`
}

type grafanaAnnotationResponse struct {
	ID      int    `json:"id"`
	Message string `json:"message"`
}

type grafanaSilenceRequest struct {
	Matchers  []grafanaMatcher `json:"matchers"`
	StartsAt  string           `json:"startsAt"`
	EndsAt    string           `json:"endsAt"`
	CreatedBy string           `json:"createdBy"`
	Comment   string           `json:"comment"`
}

type grafanaMatcher struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	IsRegex bool   `json:"isRegex"`
	IsEqual bool   `json:"isEqual"`
}

type grafanaSilenceResponse struct {
	SilenceID string `json:"silenceID"`
}

func main() {
	logger.Init("grafana-adapter")

	baseURL := os.Getenv("GRAFANA_URL")
	if baseURL == "" {
		slog.Error("GRAFANA_URL is required")
		os.Exit(1)
	}
	apiKey := os.Getenv("GRAFANA_API_KEY")
	if apiKey == "" {
		slog.Error("GRAFANA_API_KEY is required")
		os.Exit(1)
	}

	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		slog.Error("failed to create event bus", slog.Any("error", err))
		os.Exit(1)
	}

	adapter := &GrafanaAdapter{
		baseURL:    baseURL,
		apiKey:     apiKey,
		httpClient: &http.Client{},
		bus:        bus,
	}

	go adapter.subscribeToEvents()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/webhook", adapter.HandleWebhook)
	mux.HandleFunc("/api/v1/annotations", adapter.HandleAnnotations)
	mux.HandleFunc("/api/v1/silences", adapter.HandleSilences)

	slog.Info("Grafana adapter listening", slog.String("addr", ":19101"))
	http.ListenAndServe(":19101", logger.HTTPMiddleware("grafana-adapter", mux))
}

// HandleWebhook processes inbound Grafana Alertmanager webhook events.
func (a *GrafanaAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload grafanaWebhookPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	for _, alert := range payload.Alerts {
		switch alert.Status {
		case "firing":
			a.handleAlertFiring(r.Context(), alert, payload.ExternalURL)
		case "resolved":
			a.handleAlertResolved(r.Context(), alert)
		}
	}
	w.WriteHeader(http.StatusOK)
}

func (a *GrafanaAdapter) handleAlertFiring(ctx context.Context, alert grafanaAlert, externalURL string) {
	title := alert.Annotations["summary"]
	if title == "" {
		title = alert.Labels["alertname"]
	}
	p, _ := json.Marshal(map[string]interface{}{
		"fingerprint":   alert.Fingerprint,
		"title":         title,
		"description":   alert.Annotations["description"],
		"dashboard_url": alert.DashboardURL,
		"generator_url": alert.GeneratorURL,
		"labels":        alert.Labels,
		"source":        "grafana",
	})
	if err := a.bus.Publish(ctx, events.Event{
		Type:    events.EscalationCreated,
		Payload: p,
	}); err != nil {
		slog.Error("failed to publish escalation event",
			slog.String("fingerprint", alert.Fingerprint),
			slog.Any("error", err))
	}
}

func (a *GrafanaAdapter) handleAlertResolved(ctx context.Context, alert grafanaAlert) {
	title := alert.Annotations["summary"]
	if title == "" {
		title = alert.Labels["alertname"]
	}
	p, _ := json.Marshal(map[string]interface{}{
		"fingerprint": alert.Fingerprint,
		"title":       title,
		"source":      "grafana",
	})
	if err := a.bus.Publish(ctx, events.Event{
		Type:    events.TaskCompleted,
		Payload: p,
	}); err != nil {
		slog.Error("failed to publish task completed event",
			slog.String("fingerprint", alert.Fingerprint),
			slog.Any("error", err))
	}
}

// subscribeToEvents listens for Forge escalation/failure events and posts
// Grafana annotations so incidents are visible on dashboards.
func (a *GrafanaAdapter) subscribeToEvents() {
	ctx := context.Background()
	if err := a.bus.Subscribe(ctx, []events.EventType{
		events.EscalationCreated,
		events.TaskFailed,
	}, func(e events.Event) error {
		switch e.Type {
		case events.EscalationCreated, events.TaskFailed:
			return a.postAnnotation(e)
		}
		return nil
	}); err != nil {
		slog.Error("failed to subscribe to events", slog.Any("error", err))
	}
}

func (a *GrafanaAdapter) postAnnotation(e events.Event) error {
	var payload struct {
		TaskID  string `json:"task_id"`
		JiraKey string `json:"jira_key"`
		Reason  string `json:"reason"`
		Source  string `json:"source"`
	}
	json.Unmarshal(e.Payload, &payload)
	// Skip events that originated from Grafana to avoid loops.
	if payload.Source == "grafana" {
		return nil
	}

	text := fmt.Sprintf("Forge: task %s failed", payload.TaskID)
	if payload.JiraKey != "" {
		text = fmt.Sprintf("Forge: %s — %s", payload.JiraKey, payload.Reason)
	}
	tags := []string{"forge"}
	if payload.JiraKey != "" {
		tags = append(tags, payload.JiraKey)
	}
	req := grafanaAnnotationRequest{
		Time: time.Now().UnixMilli(),
		Tags: tags,
		Text: text,
	}
	return a.grafanaRequest(context.Background(), http.MethodPost, "/api/annotations", req, nil)
}

// HandleAnnotations exposes a REST endpoint so other services can post Grafana annotations.
//
//	POST /api/v1/annotations
func (a *GrafanaAdapter) HandleAnnotations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req grafanaAnnotationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var result grafanaAnnotationResponse
	if err := a.grafanaRequest(r.Context(), http.MethodPost, "/api/annotations", req, &result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// HandleSilences creates a Grafana alert silence.
//
//	POST /api/v1/silences
func (a *GrafanaAdapter) HandleSilences(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req grafanaSilenceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.CreatedBy == "" {
		req.CreatedBy = "forge"
	}
	var result grafanaSilenceResponse
	if err := a.grafanaRequest(r.Context(), http.MethodPost,
		"/api/alertmanager/grafana/api/v2/silences", req, &result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// grafanaRequest is a helper that executes an authenticated Grafana REST API call.
func (a *GrafanaAdapter) grafanaRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.baseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("grafana API error %d: %s", resp.StatusCode, string(b))
	}
	if out != nil && resp.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
