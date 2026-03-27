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

const browserStackAPIBase = "https://api.browserstack.com/automate/v1"

const sauceLabsAPIBase = "https://api.us-west-1.saucelabs.com/rest/v1"

type BrowserStackAdapter struct {
	bsUser      string
	bsAccessKey string
	slUser      string
	slAccessKey string
	bus         events.Bus
	httpClient  *http.Client
}

type browserStackBuildPayload struct {
	BuildID     string `json:"build_id"`
	BuildName   string `json:"build_name"`
	Status      string `json:"status"`
	Duration    int    `json:"duration"`
	TotalCount  int    `json:"total_count"`
	FailedCount int    `json:"failed_count"`
	PassedCount int    `json:"passed_count"`
}

type sauceLabsBuildPayload struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Passed int    `json:"passed"`
	Failed int    `json:"failed"`
}

func main() {
	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		log.Fatalf("failed to create event bus: %v", err)
	}
adapter := &BrowserStackAdapter{
	bsUser:      os.Getenv("BROWSERSTACK_USER"),
	bsAccessKey: os.Getenv("BROWSERSTACK_ACCESS_KEY"),
	slUser:      os.Getenv("SAUCE_USERNAME"),
	slAccessKey: os.Getenv("SAUCE_ACCESS_KEY"),
	bus:         bus,
	httpClient:  &http.Client{},
}
go adapter.subscribeToEvents()
mux := http.NewServeMux()
mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})
mux.HandleFunc("/webhook/browserstack", adapter.HandleBrowserStackWebhook)
mux.HandleFunc("/webhook/saucelabs", adapter.HandleSauceLabsWebhook)
mux.HandleFunc("/api/v1/builds", adapter.HandleBuilds)
mux.HandleFunc("/api/v1/sessions", adapter.HandleSessions)
log.Printf("BrowserStack / Sauce Labs adapter listening on :19131")
http.ListenAndServe(":19131", mux)
}

func (a *BrowserStackAdapter) HandleBrowserStackWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

var payload browserStackBuildPayload
if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}
switch payload.Status {
	case "done":
	if payload.FailedCount > 0 {
		ep, _ := json.Marshal(map[string]interface{}{
			"build_id":   payload.BuildID,
			"build_name": payload.BuildName,
			"failed":     payload.FailedCount,
			"passed":     payload.PassedCount,
			"source":     "browserstack",
	})
a.bus.Publish(r.Context(), events.Event{Type: events.TaskBlocked, Payload: ep})
} else {
ep, _ := json.Marshal(map[string]interface{}{
	"build_id":   payload.BuildID,
	"build_name": payload.BuildName,
	"passed":     payload.PassedCount,
	"source":     "browserstack",
})
a.bus.Publish(r.Context(), events.Event{Type: events.TaskCompleted, Payload: ep})
}
}
w.WriteHeader(http.StatusOK)
}

func (a *BrowserStackAdapter) HandleSauceLabsWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

var payload sauceLabsBuildPayload
if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}
if payload.Status == "complete" {
	if payload.Failed > 0 {
		ep, _ := json.Marshal(map[string]interface{}{
			"build_id": payload.ID,
			"name":     payload.Name,
			"failed":   payload.Failed,
			"passed":   payload.Passed,
			"source":   "saucelabs",
	})
a.bus.Publish(r.Context(), events.Event{Type: events.TaskBlocked, Payload: ep})
} else {
ep, _ := json.Marshal(map[string]interface{}{
	"build_id": payload.ID,
	"name":     payload.Name,
	"passed":   payload.Passed,
	"source":   "saucelabs",
})
a.bus.Publish(r.Context(), events.Event{Type: events.TaskCompleted, Payload: ep})
}
}
w.WriteHeader(http.StatusOK)
}

func (a *BrowserStackAdapter) HandleBuilds(w http.ResponseWriter, r *http.Request) {
	provider := r.URL.Query().Get("provider")
	if provider == "" {
		provider = "browserstack"
	}
switch r.Method {
	case http.MethodGet:

var result interface{}

var err error
switch provider {
	case "browserstack":
	err = a.bsRequest(r.Context(), "/builds.json", nil, &result)
	case "saucelabs":
	err = a.slRequest(r.Context(), fmt.Sprintf("/%s/builds", a.slUser), nil, &result)
	default:
	http.Error(w, "unsupported provider", http.StatusBadRequest)
	return
}
if err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
default:
http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
}

func (a *BrowserStackAdapter) HandleSessions(w http.ResponseWriter, r *http.Request) {
	buildID := r.URL.Query().Get("build_id")
	if buildID == "" {
		http.Error(w, "build_id query parameter is required", http.StatusBadRequest)
		return
	}
switch r.Method {
	case http.MethodGet:

var result interface{}
if err := a.bsRequest(r.Context(), fmt.Sprintf("/builds/%s/sessions.json", buildID), nil, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
default:
http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
}

func (a *BrowserStackAdapter) subscribeToEvents() {
	ctx := context.Background()
	a.bus.Subscribe(ctx, []events.EventType{events.ReviewApproved}, func(e events.Event) error {
		return nil
})
}

func (a *BrowserStackAdapter) bsRequest(ctx context.Context, path string, body interface{}, out interface{}) error {

var bodyReader io.Reader
if body != nil {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
bodyReader = strings.NewReader(string(b))
}
method := http.MethodGet
if bodyReader != nil {
	method = http.MethodPost
}
req, err := http.NewRequestWithContext(ctx, method, browserStackAPIBase+path, bodyReader)
if err != nil {
	return fmt.Errorf("create request: %w", err)
}
req.SetBasicAuth(a.bsUser, a.bsAccessKey)
req.Header.Set("Content-Type", "application/json")
resp, err := a.httpClient.Do(req)
if err != nil {
	return fmt.Errorf("execute request: %w", err)
}
defer resp.Body.Close()
if resp.StatusCode >= 300 {
	b, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("browserstack API error %d: %s", resp.StatusCode, string(b))
}
if out != nil {
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
}
return nil
}

func (a *BrowserStackAdapter) slRequest(ctx context.Context, path string, body interface{}, out interface{}) error {

var bodyReader io.Reader
if body != nil {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
bodyReader = strings.NewReader(string(b))
}
method := http.MethodGet
if bodyReader != nil {
	method = http.MethodPost
}
req, err := http.NewRequestWithContext(ctx, method, sauceLabsAPIBase+path, bodyReader)
if err != nil {
	return fmt.Errorf("create request: %w", err)
}
req.SetBasicAuth(a.slUser, a.slAccessKey)
req.Header.Set("Content-Type", "application/json")
resp, err := a.httpClient.Do(req)
if err != nil {
	return fmt.Errorf("execute request: %w", err)
}
defer resp.Body.Close()
if resp.StatusCode >= 300 {
	b, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("sauce labs API error %d: %s", resp.StatusCode, string(b))
}
if out != nil {
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
}
return nil
}
