package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"github.com/dotrage/forge-adp/pkg/events"
)

const newRelicAPIBase = "https://api.newrelic.com/v2"
const newRelicAlertsBase = "https://api.newrelic.com/v2/alerts_violations.json"

type NewRelicAdapter struct {
	apiKey     string
	accountID  string
	bus        events.Bus
	httpClient *http.Client
}

type newRelicAlertPayload struct {
	Severity    string `json:"severity"`
	State       string `json:"state"`
	PolicyName  string `json:"policy_name"`
	ConditionName string `json:"condition_name"`
	IncidentID  int    `json:"incident_id"`
	Details     string `json:"details"`
}

type newRelicWebhookEnvelope struct {
	Data newRelicAlertPayload `json:"data"`
}

func main() {
	apiKey := os.Getenv("NEWRELIC_API_KEY")
	if apiKey == "" {
		log.Fatal("NEWRELIC_API_KEY is required")
	}
bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
if err != nil {
	log.Fatalf("failed to create event bus: %v", err)
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
log.Printf("New Relic adapter listening on :19116")
http.ListenAndServe(":19116", mux)
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
		"incident_id":    p.IncidentID,
		"policy":         p.PolicyName,
		"condition":      p.ConditionName,
		"severity":       p.Severity,
		"details":        p.Details,
		"source":         "newrelic",
})
if err := a.bus.Publish(ctx, events.Event{Type: events.EscalationCreated, Payload: ep}); err != nil {
	log.Printf("failed to publish escalation event: %v", err)
}
}

func (a *NewRelicAdapter) handleAlertClosed(ctx context.Context, p newRelicAlertPayload) {
	ep, _ := json.Marshal(map[string]interface{}{
		"incident_id": p.IncidentID,
		"policy":      p.PolicyName,
		"source":      "newrelic",
})
if err := a.bus.Publish(ctx, events.Event{Type: events.TaskCompleted, Payload: ep}); err != nil {
	log.Printf("failed to publish task completed event: %v", err)
}
}

func (a *NewRelicAdapter) HandleAlerts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
		case http.MethodGet:

var result map[string]interface{}
if err := a.nrRequest(r.Context(), http.MethodGet, "/alerts_policies.json", nil, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
default:
http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
}

func (a *NewRelicAdapter) HandleViolations(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
		case http.MethodGet:

var result map[string]interface{}
if err := a.nrRequest(r.Context(), http.MethodGet, "/alerts_violations.json?only_open=true", nil, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
default:
http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
}

func (a *NewRelicAdapter) subscribeToEvents() {
	ctx := context.Background()
	a.bus.Subscribe(ctx, []events.EventType{events.TaskCompleted}, func(e events.Event) error {
		return nil
})
}

func (a *NewRelicAdapter) nrRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {

var bodyReader io.Reader
if body != nil {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
bodyReader = strings.NewReader(string(b))
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
