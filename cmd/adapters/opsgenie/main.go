package main

import (
	"io"
	"log"
	"net/http"
	"os"
	"github.com/dotrage/forge-adp/pkg/events"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
)

const opsgenieAPIBase = "https://api.opsgenie.com/v2"

type OpsgenieAdapter struct {
	apiKey     string
	httpClient *http.Client
	bus        events.Bus
}
// Opsgenie webhook payload structures

type ogWebhookPayload struct {
	Action string   `json:"action"`
	Alert  ogAlert  `json:"alert"`
}

type ogAlert struct {
	AlertID string `json:"alertId"`
	Message string `json:"message"`
	Status  string `json:"status"`
	Tags    []string `json:"tags"`
}
// Opsgenie REST API request/response types

type ogCreateAlertRequest struct {
	Message     string            `json:"message"`
	Description string            `json:"description,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Priority    string            `json:"priority,omitempty"`
	Details     map[string]string `json:"details,omitempty"`
}

type ogCreateAlertResponse struct {
	Result    string `json:"result"`
	RequestID string `json:"requestId"`
}

type ogCloseAlertRequest struct {
	Note   string `json:"note,omitempty"`
	Source string `json:"source,omitempty"`
}

type ogAcknowledgeAlertRequest struct {
	Note   string `json:"note,omitempty"`
	Source string `json:"source,omitempty"`
}

func main() {
	apiKey := os.Getenv("OPSGENIE_API_KEY")
	if apiKey == "" {
		log.Fatal("OPSGENIE_API_KEY is required")
	}
bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
if err != nil {
	log.Fatalf("failed to create event bus: %v", err)
}
adapter := &OpsgenieAdapter{
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
mux.HandleFunc("/api/v1/alerts", adapter.HandleAlerts)
log.Printf("Opsgenie adapter listening on :19099")
http.ListenAndServe(":19099", mux)
}
// HandleWebhook processes inbound Opsgenie webhook events.

func (a *OpsgenieAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

var payload ogWebhookPayload
if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}
switch payload.Action {
	case "Create":
	a.handleAlertCreated(r.Context(), payload.Alert)
	case "Close":
	a.handleAlertClosed(r.Context(), payload.Alert)
	case "Acknowledge":
	a.handleAlertAcknowledged(r.Context(), payload.Alert)
}
w.WriteHeader(http.StatusOK)
}

func (a *OpsgenieAdapter) handleAlertCreated(ctx context.Context, alert ogAlert) {
	payload, _ := json.Marshal(map[string]interface{}{
		"alert_id": alert.AlertID,
		"message":  alert.Message,
		"tags":     alert.Tags,
		"source":   "opsgenie",
})
if err := a.bus.Publish(ctx, events.Event{
	Type:    events.EscalationCreated,
	Payload: payload,
}); err != nil {
log.Printf("failed to publish escalation event: %v", err)
}
}

func (a *OpsgenieAdapter) handleAlertClosed(ctx context.Context, alert ogAlert) {
	payload, _ := json.Marshal(map[string]interface{}{
		"alert_id": alert.AlertID,
		"message":  alert.Message,
		"source":   "opsgenie",
})
if err := a.bus.Publish(ctx, events.Event{
	Type:    events.TaskCompleted,
	Payload: payload,
}); err != nil {
log.Printf("failed to publish task completed event: %v", err)
}
}

func (a *OpsgenieAdapter) handleAlertAcknowledged(ctx context.Context, alert ogAlert) {
	log.Printf("Opsgenie alert acknowledged: %s (%s)", alert.AlertID, alert.Message)
}
// subscribeToEvents listens for Forge events and creates/closes Opsgenie alerts.

func (a *OpsgenieAdapter) subscribeToEvents() {
	ctx := context.Background()
	a.bus.Subscribe(ctx, []events.EventType{
		events.EscalationCreated,
		events.TaskFailed,
	}, func(e events.Event) error {
	switch e.Type {
		case events.EscalationCreated:
		return a.createAlert(e)
		case events.TaskFailed:
		return a.createAlert(e)
	}
return nil
})
}

func (a *OpsgenieAdapter) createAlert(e events.Event) error {

var payload struct {
	TaskID  string `json:"task_id"`
	JiraKey string `json:"jira_key"`
	Reason  string `json:"reason"`
	Source  string `json:"source"`
}
json.Unmarshal(e.Payload, &payload)
// Skip alerts that originated from Opsgenie to avoid loops.
if payload.Source == "opsgenie" {
	return nil
}
message := fmt.Sprintf("Forge: task %s failed", payload.TaskID)
if payload.JiraKey != "" {
	message = fmt.Sprintf("Forge: %s — %s", payload.JiraKey, payload.Reason)
}
tags := []string{"forge"}
if payload.JiraKey != "" {
	tags = append(tags, payload.JiraKey)
}
details := map[string]string{
	"task_id": payload.TaskID,
}
if payload.JiraKey != "" {
	details["jira_key"] = payload.JiraKey
}
if payload.Reason != "" {
	details["reason"] = payload.Reason
}
req := ogCreateAlertRequest{
	Message:     message,
	Description: payload.Reason,
	Tags:        tags,
	Priority:    "P2",
	Details:     details,
}
return a.ogRequest(context.Background(), http.MethodPost, "/alerts", req, nil)
}
// HandleAlerts exposes a REST endpoint so other services can create/close/acknowledge alerts.

func (a *OpsgenieAdapter) HandleAlerts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
		case http.MethodPost:

var req ogCreateAlertRequest
if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}

var result ogCreateAlertResponse
if err := a.ogRequest(r.Context(), http.MethodPost, "/alerts", req, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
case http.MethodDelete:
alertID := r.URL.Query().Get("id")
if alertID == "" {
	http.Error(w, "id query parameter is required", http.StatusBadRequest)
	return
}
closeReq := ogCloseAlertRequest{
	Note:   r.URL.Query().Get("note"),
	Source: "forge",
}
if err := a.ogRequest(r.Context(), http.MethodPost, "/alerts/"+alertID+"/close", closeReq, nil); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.WriteHeader(http.StatusNoContent)
case http.MethodPatch:
alertID := r.URL.Query().Get("id")
if alertID == "" {
	http.Error(w, "id query parameter is required", http.StatusBadRequest)
	return
}
ackReq := ogAcknowledgeAlertRequest{
	Note:   r.URL.Query().Get("note"),
	Source: "forge",
}
if err := a.ogRequest(r.Context(), http.MethodPost, "/alerts/"+alertID+"/acknowledge", ackReq, nil); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.WriteHeader(http.StatusOK)
default:
http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
}
// ogRequest is a helper that executes an authenticated Opsgenie REST API call.

func (a *OpsgenieAdapter) ogRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {

var bodyReader io.Reader
if body != nil {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
bodyReader = bytes.NewReader(b)
}
req, err := http.NewRequestWithContext(ctx, method, opsgenieAPIBase+path, bodyReader)
if err != nil {
	return fmt.Errorf("create request: %w", err)
}
req.Header.Set("Authorization", "GenieKey "+a.apiKey)
req.Header.Set("Content-Type", "application/json")
resp, err := a.httpClient.Do(req)
if err != nil {
	return fmt.Errorf("execute request: %w", err)
}
defer resp.Body.Close()
if resp.StatusCode >= 300 {
	b, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("opsgenie API error %d: %s", resp.StatusCode, string(b))
}
if out != nil && resp.StatusCode != http.StatusNoContent {
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
}
return nil
}
