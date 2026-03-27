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

const pagerDutyAPIBase = "https://api.pagerduty.com"

type PagerDutyAdapter struct {
	apiKey     string
	serviceID  string
	fromEmail  string
	httpClient *http.Client
	bus        events.Bus
}
// PagerDuty webhook payload structures

type pdWebhookEnvelope struct {
	Messages []pdWebhookMessage `json:"messages"`
}

type pdWebhookMessage struct {
	Event   string    `json:"event"`
	Incident pdIncident `json:"incident"`
}

type pdIncident struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	Urgency     string `json:"urgency"`
	HTMLURL     string `json:"html_url"`
	ServiceID   string `json:"service_id"`
}
// PagerDuty REST API request/response types

type pdCreateIncidentRequest struct {
	Incident pdNewIncident `json:"incident"`
}

type pdNewIncident struct {
	Type    string    `json:"type"`
	Title   string    `json:"title"`
	Service pdRef     `json:"service"`
	Urgency string    `json:"urgency,omitempty"`
	Body    *pdBody   `json:"body,omitempty"`
}

type pdRef struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

type pdBody struct {
	Type    string `json:"type"`
	Details string `json:"details"`
}

type pdUpdateIncidentRequest struct {
	Incident pdUpdateIncident `json:"incident"`
}

type pdUpdateIncident struct {
	Type       string `json:"type"`
	Status     string `json:"status"`
	Resolution string `json:"resolution,omitempty"`
}

func main() {
	apiKey := os.Getenv("PAGERDUTY_API_KEY")
	if apiKey == "" {
		log.Fatal("PAGERDUTY_API_KEY is required")
	}
bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
if err != nil {
	log.Fatalf("failed to create event bus: %v", err)
}
adapter := &PagerDutyAdapter{
	apiKey:     apiKey,
	serviceID:  os.Getenv("PAGERDUTY_SERVICE_ID"),
	fromEmail:  os.Getenv("PAGERDUTY_FROM_EMAIL"),
	httpClient: &http.Client{},
	bus:        bus,
}
go adapter.subscribeToEvents()
mux := http.NewServeMux()
mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})
mux.HandleFunc("/webhook", adapter.HandleWebhook)
mux.HandleFunc("/api/v1/incidents", adapter.HandleIncidents)
log.Printf("PagerDuty adapter listening on :19098")
http.ListenAndServe(":19098", mux)
}
// HandleWebhook processes inbound PagerDuty webhook events.

func (a *PagerDutyAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

var envelope pdWebhookEnvelope
if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}
for _, msg := range envelope.Messages {
	switch msg.Event {
		case "incident.trigger":
		a.handleIncidentTriggered(r.Context(), msg.Incident)
		case "incident.resolve":
		a.handleIncidentResolved(r.Context(), msg.Incident)
		case "incident.acknowledge":
		a.handleIncidentAcknowledged(r.Context(), msg.Incident)
	}
}
w.WriteHeader(http.StatusOK)
}

func (a *PagerDutyAdapter) handleIncidentTriggered(ctx context.Context, inc pdIncident) {
	payload, _ := json.Marshal(map[string]interface{}{
		"incident_id": inc.ID,
		"title":       inc.Title,
		"urgency":     inc.Urgency,
		"url":         inc.HTMLURL,
		"source":      "pagerduty",
})
if err := a.bus.Publish(ctx, events.Event{
	Type:    events.EscalationCreated,
	Payload: payload,
}); err != nil {
log.Printf("failed to publish escalation event: %v", err)
}
}

func (a *PagerDutyAdapter) handleIncidentResolved(ctx context.Context, inc pdIncident) {
	payload, _ := json.Marshal(map[string]interface{}{
		"incident_id": inc.ID,
		"title":       inc.Title,
		"source":      "pagerduty",
})
if err := a.bus.Publish(ctx, events.Event{
	Type:    events.TaskCompleted,
	Payload: payload,
}); err != nil {
log.Printf("failed to publish task completed event: %v", err)
}
}

func (a *PagerDutyAdapter) handleIncidentAcknowledged(ctx context.Context, inc pdIncident) {
	log.Printf("PagerDuty incident acknowledged: %s (%s)", inc.ID, inc.Title)
}
// subscribeToEvents listens for Forge events and creates/resolves PagerDuty incidents.

func (a *PagerDutyAdapter) subscribeToEvents() {
	ctx := context.Background()
	a.bus.Subscribe(ctx, []events.EventType{
		events.EscalationCreated,
		events.TaskFailed,
	}, func(e events.Event) error {
	switch e.Type {
		case events.EscalationCreated:
		return a.createIncident(e)
		case events.TaskFailed:
		return a.createIncident(e)
	}
return nil
})
}

func (a *PagerDutyAdapter) createIncident(e events.Event) error {

var payload struct {
	TaskID  string `json:"task_id"`
	JiraKey string `json:"jira_key"`
	Reason  string `json:"reason"`
	Source  string `json:"source"`
}
json.Unmarshal(e.Payload, &payload)
// Skip incidents that originated from PagerDuty to avoid loops.
if payload.Source == "pagerduty" {
	return nil
}
title := fmt.Sprintf("Forge: task %s failed", payload.TaskID)
if payload.JiraKey != "" {
	title = fmt.Sprintf("Forge: %s — %s", payload.JiraKey, payload.Reason)
}
req := pdCreateIncidentRequest{
	Incident: pdNewIncident{
		Type:    "incident",
		Title:   title,
		Service: pdRef{ID: a.serviceID, Type: "service_reference"},
		Urgency: "high",
		Body: &pdBody{
			Type:    "incident_body",
			Details: payload.Reason,
		},
},
}
return a.pdRequest(context.Background(), http.MethodPost, "/incidents", req, nil)
}
// HandleIncidents exposes a REST endpoint so other services can create/resolve incidents.

func (a *PagerDutyAdapter) HandleIncidents(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
		case http.MethodPost:

var req pdCreateIncidentRequest
if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}
req.Incident.Type = "incident"
if req.Incident.Service.ID == "" {
	req.Incident.Service = pdRef{ID: a.serviceID, Type: "service_reference"}
}

var result map[string]interface{}
if err := a.pdRequest(r.Context(), http.MethodPost, "/incidents", req, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
case http.MethodPut:
incidentID := r.URL.Query().Get("id")
if incidentID == "" {
	http.Error(w, "id query parameter is required", http.StatusBadRequest)
	return
}

var req pdUpdateIncidentRequest
if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}
req.Incident.Type = "incident"

var result map[string]interface{}
if err := a.pdRequest(r.Context(), http.MethodPut, "/incidents/"+incidentID, req, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
default:
http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
}
// pdRequest is a helper that executes an authenticated PagerDuty REST API call.

func (a *PagerDutyAdapter) pdRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {

var bodyReader io.Reader
if body != nil {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
bodyReader = bytes.NewReader(b)
}
req, err := http.NewRequestWithContext(ctx, method, pagerDutyAPIBase+path, bodyReader)
if err != nil {
	return fmt.Errorf("create request: %w", err)
}
req.Header.Set("Authorization", "Token token="+a.apiKey)
req.Header.Set("Accept", "application/vnd.pagerduty+json;version=2")
req.Header.Set("Content-Type", "application/json")
if a.fromEmail != "" {
	req.Header.Set("From", a.fromEmail)
}
resp, err := a.httpClient.Do(req)
if err != nil {
	return fmt.Errorf("execute request: %w", err)
}
defer resp.Body.Close()
if resp.StatusCode >= 300 {
	b, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("pagerduty API error %d: %s", resp.StatusCode, string(b))
}
if out != nil {
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
}
return nil
}
