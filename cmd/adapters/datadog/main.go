package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"github.com/dotrage/forge-adp/pkg/events"
	"bytes"
	"context"
	"encoding/json"
)

const datadogAPIBase = "https://api.datadoghq.com/api/v1"

type DatadogAdapter struct {
	apiKey     string
	appKey     string
	httpClient *http.Client
	bus        events.Bus
}
// Datadog webhook payload structures

type ddWebhookPayload struct {
	ID              string `json:"id"`
	Title           string `json:"title"`
	URL             string `json:"url"`
	Body            string `json:"body"`
	Priority        string `json:"priority"`
	Tags            string `json:"tags"`
	AlertID         int64  `json:"alert_id"`
	AlertStatus     string `json:"alert_status"`
	AlertMetric     string `json:"alert_metric"`
	AlertTransition string `json:"alert_transition"`
	OrgName         string `json:"org_name"`
}
// Datadog REST API request/response types

type ddCreateEventRequest struct {
	Title     string   `json:"title"`
	Text      string   `json:"text"`
	Priority  string   `json:"priority,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	AlertType string   `json:"alert_type,omitempty"`
}

type ddCreateEventResponse struct {
	Status string  `json:"status"`
	Event  ddEvent `json:"event"`
}

type ddEvent struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

func main() {
	apiKey := os.Getenv("DATADOG_API_KEY")
	if apiKey == "" {
		log.Fatal("DATADOG_API_KEY is required")
	}
bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
if err != nil {
	log.Fatalf("failed to create event bus: %v", err)
}
adapter := &DatadogAdapter{
	apiKey:     apiKey,
	appKey:     os.Getenv("DATADOG_APP_KEY"),
	httpClient: &http.Client{},
	bus:        bus,
}
go adapter.subscribeToEvents()
mux := http.NewServeMux()
mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})
mux.HandleFunc("/webhook", adapter.HandleWebhook)
mux.HandleFunc("/api/v1/events", adapter.HandleEvents)
log.Printf("Datadog adapter listening on :19100")
http.ListenAndServe(":19100", mux)
}
// HandleWebhook processes inbound Datadog webhook events.

func (a *DatadogAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

var payload ddWebhookPayload
if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}
switch payload.AlertTransition {
	case "Triggered", "No Data":
	a.handleAlertTriggered(r.Context(), payload)
	case "Recovered":
	a.handleAlertRecovered(r.Context(), payload)
}
w.WriteHeader(http.StatusOK)
}

func (a *DatadogAdapter) handleAlertTriggered(ctx context.Context, alert ddWebhookPayload) {
	p, _ := json.Marshal(map[string]interface{}{
		"alert_id": alert.AlertID,
		"title":    alert.Title,
		"url":      alert.URL,
		"priority": alert.Priority,
		"tags":     alert.Tags,
		"source":   "datadog",
})
if err := a.bus.Publish(ctx, events.Event{
	Type:    events.EscalationCreated,
	Payload: p,
}); err != nil {
log.Printf("failed to publish escalation event: %v", err)
}
}

func (a *DatadogAdapter) handleAlertRecovered(ctx context.Context, alert ddWebhookPayload) {
	p, _ := json.Marshal(map[string]interface{}{
		"alert_id": alert.AlertID,
		"title":    alert.Title,
		"source":   "datadog",
})
if err := a.bus.Publish(ctx, events.Event{
	Type:    events.TaskCompleted,
	Payload: p,
}); err != nil {
log.Printf("failed to publish task completed event: %v", err)
}
}
// subscribeToEvents listens for Forge events and posts Datadog events.

func (a *DatadogAdapter) subscribeToEvents() {
	ctx := context.Background()
	a.bus.Subscribe(ctx, []events.EventType{
		events.EscalationCreated,
		events.TaskFailed,
	}, func(e events.Event) error {
	switch e.Type {
		case events.EscalationCreated:
		return a.postEvent(e)
		case events.TaskFailed:
		return a.postEvent(e)
	}
return nil
})
}

func (a *DatadogAdapter) postEvent(e events.Event) error {

var payload struct {
	TaskID  string `json:"task_id"`
	JiraKey string `json:"jira_key"`
	Reason  string `json:"reason"`
	Source  string `json:"source"`
}
json.Unmarshal(e.Payload, &payload)
// Skip events that originated from Datadog to avoid loops.
if payload.Source == "datadog" {
	return nil
}
title := fmt.Sprintf("Forge: task %s failed", payload.TaskID)
if payload.JiraKey != "" {
	title = fmt.Sprintf("Forge: %s — %s", payload.JiraKey, payload.Reason)
}
tags := []string{"forge", "source:forge"}
if payload.JiraKey != "" {
	tags = append(tags, "jira_key:"+payload.JiraKey)
}
req := ddCreateEventRequest{
	Title:     title,
	Text:      payload.Reason,
	Priority:  "normal",
	Tags:      tags,
	AlertType: "error",
}
return a.ddRequest(context.Background(), http.MethodPost, "/events", req, nil)
}
// HandleEvents exposes a REST endpoint so other services can post Datadog events.

func (a *DatadogAdapter) HandleEvents(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
		case http.MethodPost:

var req ddCreateEventRequest
if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}

var result ddCreateEventResponse
if err := a.ddRequest(r.Context(), http.MethodPost, "/events", req, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
default:
http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
}
// ddRequest is a helper that executes an authenticated Datadog REST API call.

func (a *DatadogAdapter) ddRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {

var bodyReader io.Reader
if body != nil {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
bodyReader = bytes.NewReader(b)
}
req, err := http.NewRequestWithContext(ctx, method, datadogAPIBase+path, bodyReader)
if err != nil {
	return fmt.Errorf("create request: %w", err)
}
req.Header.Set("Content-Type", "application/json")
req.Header.Set("DD-API-KEY", a.apiKey)
if a.appKey != "" {
	req.Header.Set("DD-APPLICATION-KEY", a.appKey)
}
resp, err := a.httpClient.Do(req)
if err != nil {
	return fmt.Errorf("execute request: %w", err)
}
defer resp.Body.Close()
if resp.StatusCode >= 300 {
	b, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("datadog API error %d: %s", resp.StatusCode, string(b))
}
if out != nil && resp.StatusCode != http.StatusNoContent {
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
}
return nil
}
