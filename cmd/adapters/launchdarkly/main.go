package main

import (
	"net/http"
	"os"
	"strings"
	"github.com/dotrage/forge-adp/pkg/events"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
)

const ldAPIBase = "https://app.launchdarkly.com/api/v2"

type LaunchDarklyAdapter struct {
	apiKey     string
	projectKey string
	bus        events.Bus
	httpClient *http.Client
}

type ldFlagChange struct {
	Kind   string `json:"kind"`
	Key    string `json:"key"`
	Name   string `json:"name"`
	Changes map[string]interface{} `json:"changes"`
}

type ldWebhookPayload struct {
	Kind        string     `json:"kind"`
	AcccessToken string    `json:"acccessToken,omitempty"`
	Data        ldFlagChange `json:"data,omitempty"`
}

func main() {
	apiKey := os.Getenv("LAUNCHDARKLY_API_KEY")
	if apiKey == "" {
		log.Fatal("LAUNCHDARKLY_API_KEY is required")
	}
bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
if err != nil {
	log.Fatalf("failed to create event bus: %v", err)
}
adapter := &LaunchDarklyAdapter{
	apiKey:     apiKey,
	projectKey: os.Getenv("LAUNCHDARKLY_PROJECT_KEY"),
	bus:        bus,
	httpClient: &http.Client{},
}
go adapter.subscribeToEvents()
mux := http.NewServeMux()
mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})
mux.HandleFunc("/webhook", adapter.HandleWebhook)
mux.HandleFunc("/api/v1/flags", adapter.HandleFlags)
mux.HandleFunc("/api/v1/environments", adapter.HandleEnvironments)
log.Printf("LaunchDarkly adapter listening on :19127")
http.ListenAndServe(":19127", mux)
}

func (a *LaunchDarklyAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

var payload ldWebhookPayload
if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}
switch payload.Kind {
	case "flag":
	a.handleFlagChanged(r.Context(), payload.Data)
}
w.WriteHeader(http.StatusOK)
}

func (a *LaunchDarklyAdapter) handleFlagChanged(ctx context.Context, f ldFlagChange) {
	ep, _ := json.Marshal(map[string]interface{}{
		"flag_key":  f.Key,
		"flag_name": f.Name,
		"changes":   f.Changes,
		"source":    "launchdarkly",
})
if err := a.bus.Publish(ctx, events.Event{Type: events.TaskCompleted, Payload: ep}); err != nil {
	log.Printf("failed to publish task completed event: %v", err)
}
}

func (a *LaunchDarklyAdapter) HandleFlags(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
		case http.MethodGet:
		projectKey := r.URL.Query().Get("project")
		if projectKey == "" {
			projectKey = a.projectKey
		}

var result interface{}
if err := a.ldRequest(r.Context(), http.MethodGet, fmt.Sprintf("/flags/%s", projectKey), nil, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
case http.MethodPost:
projectKey := r.URL.Query().Get("project")
if projectKey == "" {
	projectKey = a.projectKey
}

var req map[string]interface{}
if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}

var result interface{}
if err := a.ldRequest(r.Context(), http.MethodPost, fmt.Sprintf("/flags/%s", projectKey), req, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
w.WriteHeader(http.StatusCreated)
json.NewEncoder(w).Encode(result)
default:
http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
}

func (a *LaunchDarklyAdapter) HandleEnvironments(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
projectKey := r.URL.Query().Get("project")
if projectKey == "" {
	projectKey = a.projectKey
}

var result interface{}
if err := a.ldRequest(r.Context(), http.MethodGet, fmt.Sprintf("/projects/%s/environments", projectKey), nil, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
}

func (a *LaunchDarklyAdapter) subscribeToEvents() {
	ctx := context.Background()
	a.bus.Subscribe(ctx, []events.EventType{events.DeploymentApproved}, func(e events.Event) error {
		return nil
})
}

func (a *LaunchDarklyAdapter) ldRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {

var bodyReader io.Reader
if body != nil {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
bodyReader = strings.NewReader(string(b))
}
req, err := http.NewRequestWithContext(ctx, method, ldAPIBase+path, bodyReader)
if err != nil {
	return fmt.Errorf("create request: %w", err)
}
req.Header.Set("Authorization", a.apiKey)
req.Header.Set("Content-Type", "application/json")
resp, err := a.httpClient.Do(req)
if err != nil {
	return fmt.Errorf("execute request: %w", err)
}
defer resp.Body.Close()
if resp.StatusCode >= 300 {
	b, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("launchdarkly API error %d: %s", resp.StatusCode, string(b))
}
if out != nil {
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
}
return nil
}
