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
	"net/url"
	"os"
	"strings"

	"github.com/dotrage/forge-adp/pkg/events"
	"github.com/dotrage/forge-adp/pkg/logger"
)

// SonarQubeAdapter bridges SonarQube analysis events with the Forge event bus.
// Incoming webhooks report analysis results and quality gate statuses.
// The adapter escalates quality gate failures and surfaces them as Forge events.

type SonarQubeAdapter struct {
	baseURL       string
	token         string
	webhookSecret string
	httpClient    *http.Client
	bus           events.Bus
}

// sonarWebhookPayload is the envelope SonarQube sends for analysis events.
type sonarWebhookPayload struct {
	TaskID   string `json:"taskId"`
	Status   string `json:"status"`
	Analysis struct {
		Key  string `json:"key"`
		Date string `json:"date"`
	} `json:"analysis"`
	Project struct {
		Key  string `json:"key"`
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"project"`
	QualityGate *struct {
		Name       string `json:"name"`
		Status     string `json:"status"`
		Conditions []struct {
			Metric         string `json:"metric"`
			Operator       string `json:"operator"`
			Value          string `json:"value"`
			Status         string `json:"status"`
			ErrorThreshold string `json:"errorThreshold"`
		} `json:"conditions"`
	} `json:"qualityGate,omitempty"`
	Branch *struct {
		Name string `json:"name"`
		Type string `json:"type"`
		URL  string `json:"url"`
	} `json:"branch,omitempty"`
}

// sonarIssue represents a SonarQube code issue.
type sonarIssue struct {
	Key      string `json:"key"`
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Status   string `json:"status"`
	Type     string `json:"type"`
}

// sonarIssuesResponse wraps the SonarQube issues search response.
type sonarIssuesResponse struct {
	Total  int          `json:"total"`
	Issues []sonarIssue `json:"issues"`
}

// sonarQualityGate represents a quality gate definition.
type sonarQualityGate struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	IsDefault bool   `json:"isDefault"`
	IsBuiltIn bool   `json:"isBuiltIn"`
	Conditions []struct {
		Metric   string `json:"metric"`
		Operator string `json:"op"`
		Error    string `json:"error"`
	} `json:"conditions"`
}

// sonarQualityGatesResponse wraps the list of quality gates.
type sonarQualityGatesResponse struct {
	QualityGates []sonarQualityGate `json:"qualitygates"`
}

func main() {
	logger.Init("sonarqube-adapter")

	baseURL := os.Getenv("SONARQUBE_URL")
	if baseURL == "" {
		slog.Error("SONARQUBE_URL is required")
		os.Exit(1)
	}
	token := os.Getenv("SONARQUBE_TOKEN")
	if token == "" {
		slog.Error("SONARQUBE_TOKEN is required")
		os.Exit(1)
	}

	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		slog.Error("failed to create event bus", slog.Any("error", err))
		os.Exit(1)
	}

	adapter := &SonarQubeAdapter{
		baseURL:       strings.TrimRight(baseURL, "/"),
		token:         token,
		webhookSecret: os.Getenv("SONARQUBE_WEBHOOK_SECRET"),
		httpClient:    &http.Client{},
		bus:           bus,
	}

	go adapter.subscribeToEvents()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/webhook", adapter.HandleWebhook)
	mux.HandleFunc("/api/v1/issues", adapter.HandleIssues)
	mux.HandleFunc("/api/v1/qualitygates", adapter.HandleQualityGates)
	mux.HandleFunc("/api/v1/projects", adapter.HandleProjects)

	slog.Info("SonarQube adapter listening", slog.String("addr", ":19103"))
	http.ListenAndServe(":19103", logger.HTTPMiddleware("sonarqube-adapter", mux))
}

// verifySignature validates the SonarQube webhook HMAC-SHA256 payload checksum.
func (a *SonarQubeAdapter) verifySignature(r *http.Request, body []byte) bool {
	if a.webhookSecret == "" {
		return true
	}
	sig := r.Header.Get("X-Sonar-Webhook-HMAC-SHA256")
	if sig == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(a.webhookSecret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(sig), []byte(expected))
}

// HandleWebhook processes inbound SonarQube analysis events.
func (a *SonarQubeAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if !a.verifySignature(r, body) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	var payload sonarWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	switch payload.Status {
	case "SUCCESS":
		if payload.QualityGate != nil && payload.QualityGate.Status == "ERROR" {
			// Analysis succeeded but quality gate failed — escalate as a blocker.
			var failedConditions []string
			for _, c := range payload.QualityGate.Conditions {
				if c.Status == "ERROR" {
					failedConditions = append(failedConditions,
						fmt.Sprintf("%s (value: %s, threshold: %s)", c.Metric, c.Value, c.ErrorThreshold))
				}
			}
			ep, _ := json.Marshal(map[string]interface{}{
				"source":            "sonarqube",
				"project":           payload.Project.Name,
				"project_key":       payload.Project.Key,
				"project_url":       payload.Project.URL,
				"quality_gate":      payload.QualityGate.Name,
				"failed_conditions": failedConditions,
				"reason": fmt.Sprintf("SonarQube quality gate '%s' failed for project %s",
					payload.QualityGate.Name, payload.Project.Name),
			})
			if err := a.bus.Publish(r.Context(), events.Event{Type: events.EscalationCreated, Payload: ep}); err != nil {
				slog.Error("failed to publish escalation event",
					slog.String("project_key", payload.Project.Key),
					slog.Any("error", err))
			}
		}
	case "FAILED", "CANCELLED":
		ep, _ := json.Marshal(map[string]interface{}{
			"source":      "sonarqube",
			"project":     payload.Project.Name,
			"project_key": payload.Project.Key,
			"task_id":     payload.TaskID,
			"status":      payload.Status,
		})
		if err := a.bus.Publish(r.Context(), events.Event{Type: events.TaskFailed, Payload: ep}); err != nil {
			slog.Error("failed to publish task failed event",
				slog.String("project_key", payload.Project.Key),
				slog.Any("error", err))
		}
	}

	w.WriteHeader(http.StatusOK)
}

// HandleIssues proxies issue searches to SonarQube.
//
//	GET /api/v1/issues?project_key=<key>[&severities=BLOCKER,CRITICAL][&statuses=OPEN]
func (a *SonarQubeAdapter) HandleIssues(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := url.Values{}
	if pk := r.URL.Query().Get("project_key"); pk != "" {
		q.Set("componentKeys", pk)
	}
	if sev := r.URL.Query().Get("severities"); sev != "" {
		q.Set("severities", sev)
	}
	if status := r.URL.Query().Get("statuses"); status != "" {
		q.Set("statuses", status)
	}
	q.Set("resolved", "false")

	var result sonarIssuesResponse
	if err := a.sonarRequest(r.Context(), http.MethodGet,
		"/api/issues/search?"+q.Encode(), nil, &result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// HandleQualityGates lists the configured SonarQube quality gates.
//
//	GET /api/v1/qualitygates
func (a *SonarQubeAdapter) HandleQualityGates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var result sonarQualityGatesResponse
	if err := a.sonarRequest(r.Context(), http.MethodGet, "/api/qualitygates/list", nil, &result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// HandleProjects lists SonarQube projects, optionally filtered by search term.
//
//	GET /api/v1/projects[?search=<name>]
func (a *SonarQubeAdapter) HandleProjects(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := url.Values{}
	if s := r.URL.Query().Get("search"); s != "" {
		q.Set("q", s)
	}
	q.Set("ps", "50")

	var result map[string]interface{}
	if err := a.sonarRequest(r.Context(), http.MethodGet,
		"/api/projects/search?"+q.Encode(), nil, &result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// subscribeToEvents listens for ReviewRequested events. When a PR review is
// requested Forge can poll SonarQube for open issues on that project so agents
// have code-quality context before the review begins.
func (a *SonarQubeAdapter) subscribeToEvents() {
	ctx := context.Background()
	if err := a.bus.Subscribe(ctx, []events.EventType{events.ReviewRequested}, func(e events.Event) error {
		var payload map[string]interface{}
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			return fmt.Errorf("unmarshal review requested payload: %w", err)
		}

		projectKey, _ := payload["project_key"].(string)
		if projectKey == "" {
			return nil // no sonar project key in event — nothing to do
		}

		slog.Info("fetching SonarQube issues for review",
			slog.String("project_key", projectKey),
			slog.String("task_id", e.TaskID))

		q := url.Values{
			"componentKeys": []string{projectKey},
			"severities":    []string{"BLOCKER,CRITICAL"},
			"resolved":      []string{"false"},
		}
		var result sonarIssuesResponse
		if err := a.sonarRequest(ctx, http.MethodGet,
			"/api/issues/search?"+q.Encode(), nil, &result); err != nil {
			slog.Warn("failed to fetch sonar issues for review",
				slog.String("project_key", projectKey),
				slog.Any("error", err))
			return nil // non-fatal; review can proceed without sonar context
		}

		if result.Total > 0 {
			slog.Warn("open blocker/critical issues found before review",
				slog.String("project_key", projectKey),
				slog.Int("total", result.Total))
		}
		return nil
	}); err != nil {
		slog.Error("failed to subscribe to review requested events", slog.Any("error", err))
	}
}

// sonarRequest executes an authenticated SonarQube API call using token-based auth.
func (a *SonarQubeAdapter) sonarRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {
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
	// SonarQube token auth: token as username, empty password
	req.SetBasicAuth(a.token, "")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("sonarqube API error %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
