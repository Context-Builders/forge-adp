package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"github.com/dotrage/forge-adp/pkg/events"
	"bytes"
	"context"
	"crypto/hmac"
)

// SonarQubeAdapter bridges SonarQube analysis events with the Forge event bus.)

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
		Metric     string `json:"metric"`
		Operator   string `json:"operator"`
		Value      string `json:"value"`
		Status     string `json:"status"`
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
	ID         string `json:"id"`
	Name       string `json:"name"`
	IsDefault  bool   `json:"isDefault"`
	IsBuiltIn  bool   `json:"isBuiltIn"`
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
	baseURL := os.Getenv("SONARQUBE_URL")
	if baseURL == "" {
		log.Fatal("SONARQUBE_URL is required")
	}
token := os.Getenv("SONARQUBE_TOKEN")
if token == "" {
	log.Fatal("SONARQUBE_TOKEN is required")
}
bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
if err != nil {
	log.Fatalf("failed to create event bus: %v", err)
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
log.Printf("SonarQube adapter listening on :19103")
http.ListenAndServe(":19103", mux)
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
// Analysis succeeded but quality gate failed — surface to the bus as a blocker.

var failedConditions []string
for _, c := range payload.QualityGate.Conditions {
	if c.Status == "ERROR" {
		failedConditions = append(failedConditions, fmt.Sprintf("%s (value: %s, threshold: %s)", c.Metric, c.Value, c.ErrorThreshold))
	}
}
eventPayload, _ := json.Marshal(map[string]interface{}{
	"source":             "sonarqube",
	"project":            payload.Project.Name,
	"project_key":        payload.Project.Key,
	"project_url":        payload.Project.URL,
	"quality_gate":       payload.QualityGate.Name,
	"failed_conditions":  failedConditions,
	"reason":             fmt.Sprintf("SonarQube quality gate '%s' failed for project %s", payload.QualityGate.Name, payload.Project.Name),
})
if err := a.bus.Publish(r.Context(), events.Event{
	Type:    events.EscalationCreated,
	Payload: eventPayload,
}); err != nil {
log.Printf("failed to publish escalation event: %v", err)
}
}
case "FAILED", "CANCELLED":
eventPayload, _ := json.Marshal(map[string]interface{}{
	"source":      "sonarqube",
	"project":     payload.Project.Name,
	"project_key": payload.Project.Key,
	"task_id":     payload.TaskID,
	"status":      payload.Status,
})
if err := a.bus.Publish(r.Context(), events.Event{
	Type:    events.TaskFailed,
	Payload: eventPayload,
}); err != nil {
log.Printf("failed to publish task failed event: %v", err)
}
}
w.WriteHeader(http.StatusOK)
}
// HandleIssues proxies issue searches to SonarQube.

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
"/api/issues/search?"+q.Encode(),
nil, &result,
); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
}
// HandleQualityGates lists the configured SonarQube quality gates.

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
// subscribeToEvents listens for Forge events that should trigger SonarQube actions.

func (a *SonarQubeAdapter) subscribeToEvents() {
	ctx := context.Background()
	a.bus.Subscribe(ctx, []events.EventType{
		events.ReviewRequested,
	}, func(e events.Event) error {
// Future: trigger a SonarQube background task when a PR review is requested.
return nil
})
}
// sonarRequest is a helper that executes an authenticated SonarQube API call.

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
