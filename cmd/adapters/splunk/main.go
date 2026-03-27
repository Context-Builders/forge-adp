package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"github.com/dotrage/forge-adp/pkg/events"
	"context"
	"encoding/json"
)

const splunkAPIBase = "/services"

type SplunkAdapter struct {
	baseURL    string
	token      string
	index      string
	bus        events.Bus
	httpClient *http.Client
}

type splunkAlertPayload struct {
	SearchName string `json:"search_name"`
	ResultsURL string `json:"results_link"`
	Owner      string `json:"owner"`
	App        string `json:"app"`
	Result     map[string]interface{} `json:"result"`
}

func main() {
	baseURL := os.Getenv("SPLUNK_URL")
	token := os.Getenv("SPLUNK_HEC_TOKEN")
	if baseURL == "" || token == "" {
		log.Fatal("SPLUNK_URL and SPLUNK_HEC_TOKEN are required")
	}
bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
if err != nil {
	log.Fatalf("failed to create event bus: %v", err)
}
adapter := &SplunkAdapter{
	baseURL:    strings.TrimRight(baseURL, "/"),
	token:      token,
	index:      os.Getenv("SPLUNK_INDEX"),
	bus:        bus,
	httpClient: &http.Client{},
}
go adapter.subscribeToEvents()
mux := http.NewServeMux()
mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})
mux.HandleFunc("/webhook", adapter.HandleWebhook)
mux.HandleFunc("/api/v1/search", adapter.HandleSearch)
mux.HandleFunc("/api/v1/events", adapter.HandleEvents)
log.Printf("Splunk adapter listening on :19117")
http.ListenAndServe(":19117", mux)
}

func (a *SplunkAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

var payload splunkAlertPayload
if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}
a.handleAlert(r.Context(), payload)
w.WriteHeader(http.StatusOK)
}

func (a *SplunkAdapter) handleAlert(ctx context.Context, p splunkAlertPayload) {
	ep, _ := json.Marshal(map[string]interface{}{
		"search_name": p.SearchName,
		"results_url": p.ResultsURL,
		"owner":       p.Owner,
		"app":         p.App,
		"source":      "splunk",
})
if err := a.bus.Publish(ctx, events.Event{Type: events.EscalationCreated, Payload: ep}); err != nil {
	log.Printf("failed to publish escalation event: %v", err)
}
}

func (a *SplunkAdapter) HandleSearch(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
		case http.MethodPost:

var req map[string]interface{}
if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}

var result map[string]interface{}
if err := a.splunkRequest(r.Context(), http.MethodPost, "/search/jobs", req, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
default:
http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
}

func (a *SplunkAdapter) HandleEvents(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
		case http.MethodPost:

var event map[string]interface{}
if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}
hecPayload := map[string]interface{}{
	"event": event,
	"index": a.index,
}
if err := a.hecRequest(r.Context(), hecPayload); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.WriteHeader(http.StatusCreated)
default:
http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
}

func (a *SplunkAdapter) subscribeToEvents() {
	ctx := context.Background()
	a.bus.Subscribe(ctx, []events.EventType{
		events.TaskCompleted,
		events.TaskFailed,
		events.EscalationCreated,
	}, func(e events.Event) error {
	hecPayload := map[string]interface{}{
		"event": map[string]interface{}{
			"type":    string(e.Type),
			"payload": json.RawMessage(e.Payload),
		},
	"index":      a.index,
	"sourcetype": "forge:event",
}
return a.hecRequest(ctx, hecPayload)
})
}

func (a *SplunkAdapter) hecRequest(ctx context.Context, payload interface{}) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/services/collector/event", strings.NewReader(string(b)))
if err != nil {
	return fmt.Errorf("create request: %w", err)
}
req.Header.Set("Authorization", "Splunk "+a.token)
req.Header.Set("Content-Type", "application/json")
resp, err := a.httpClient.Do(req)
if err != nil {
	return fmt.Errorf("execute request: %w", err)
}
defer resp.Body.Close()
if resp.StatusCode >= 300 {
	rb, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("splunk HEC error %d: %s", resp.StatusCode, string(rb))
}
return nil
}

func (a *SplunkAdapter) splunkRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {

var bodyReader io.Reader
if body != nil {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
bodyReader = strings.NewReader(string(b))
}
req, err := http.NewRequestWithContext(ctx, method, a.baseURL+splunkAPIBase+path+"?output_mode=json", bodyReader)
if err != nil {
	return fmt.Errorf("create request: %w", err)
}
req.Header.Set("Authorization", "Bearer "+a.token)
req.Header.Set("Content-Type", "application/json")
resp, err := a.httpClient.Do(req)
if err != nil {
	return fmt.Errorf("execute request: %w", err)
}
defer resp.Body.Close()
if resp.StatusCode >= 300 {
	rb, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("splunk API error %d: %s", resp.StatusCode, string(rb))
}
if out != nil {
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
}
return nil
}
