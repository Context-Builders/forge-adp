package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"github.com/dotrage/forge-adp/pkg/events"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

const jenkinsAPIBase = "/api/json"

type JenkinsAdapter struct {
	baseURL       string
	user          string
	apiToken      string
	webhookSecret string
	bus           events.Bus
	httpClient    *http.Client
}

type jenkinsBuildEvent struct {
	Name   string `json:"name"`
	Build  struct {
		Number   int    `json:"number"`
		Phase    string `json:"phase"`
		Status   string `json:"status"`
		URL      string `json:"url"`
		FullURL  string `json:"full_url"`
	} `json:"build"`
}

func main() {
	baseURL := os.Getenv("JENKINS_URL")
	user := os.Getenv("JENKINS_USER")
	apiToken := os.Getenv("JENKINS_API_TOKEN")
	if baseURL == "" || user == "" || apiToken == "" {
		log.Fatal("JENKINS_URL, JENKINS_USER, and JENKINS_API_TOKEN are required")
	}
bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
if err != nil {
	log.Fatalf("failed to create event bus: %v", err)
}
adapter := &JenkinsAdapter{
	baseURL:       strings.TrimRight(baseURL, "/"),
	user:          user,
	apiToken:      apiToken,
	webhookSecret: os.Getenv("JENKINS_WEBHOOK_SECRET"),
	bus:           bus,
	httpClient:    &http.Client{},
}
go adapter.subscribeToEvents()
mux := http.NewServeMux()
mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})
mux.HandleFunc("/webhook", adapter.HandleWebhook)
mux.HandleFunc("/api/v1/builds", adapter.HandleBuilds)
mux.HandleFunc("/api/v1/jobs", adapter.HandleJobs)
log.Printf("Jenkins adapter listening on :19111")
http.ListenAndServe(":19111", mux)
}

func (a *JenkinsAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
body, err := io.ReadAll(r.Body)
if err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}
if a.webhookSecret != "" {
	sig := r.Header.Get("X-Jenkins-Signature")
	mac := hmac.New(sha256.New, []byte(a.webhookSecret))
	mac.Write(body)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(sig)) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
}

var payload jenkinsBuildEvent
if err := json.Unmarshal(body, &payload); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}
switch payload.Build.Phase {
	case "COMPLETED":
	switch payload.Build.Status {
		case "SUCCESS":
		a.handleBuildSuccess(r.Context(), payload)
		case "FAILURE", "ABORTED", "UNSTABLE":
		a.handleBuildFailed(r.Context(), payload)
	}
case "STARTED":
a.handleBuildStarted(r.Context(), payload)
}
w.WriteHeader(http.StatusOK)
}

func (a *JenkinsAdapter) handleBuildStarted(ctx context.Context, p jenkinsBuildEvent) {
	ep, _ := json.Marshal(map[string]interface{}{
		"job":    p.Name,
		"build":  p.Build.Number,
		"url":    p.Build.FullURL,
		"source": "jenkins",
})
if err := a.bus.Publish(ctx, events.Event{Type: events.TaskStarted, Payload: ep}); err != nil {
	log.Printf("failed to publish task started event: %v", err)
}
}

func (a *JenkinsAdapter) handleBuildSuccess(ctx context.Context, p jenkinsBuildEvent) {
	ep, _ := json.Marshal(map[string]interface{}{
		"job":    p.Name,
		"build":  p.Build.Number,
		"url":    p.Build.FullURL,
		"source": "jenkins",
})
if err := a.bus.Publish(ctx, events.Event{Type: events.TaskCompleted, Payload: ep}); err != nil {
	log.Printf("failed to publish task completed event: %v", err)
}
}

func (a *JenkinsAdapter) handleBuildFailed(ctx context.Context, p jenkinsBuildEvent) {
	ep, _ := json.Marshal(map[string]interface{}{
		"job":    p.Name,
		"build":  p.Build.Number,
		"status": p.Build.Status,
		"url":    p.Build.FullURL,
		"source": "jenkins",
})
if err := a.bus.Publish(ctx, events.Event{Type: events.TaskFailed, Payload: ep}); err != nil {
	log.Printf("failed to publish task failed event: %v", err)
}
}

func (a *JenkinsAdapter) HandleBuilds(w http.ResponseWriter, r *http.Request) {
	job := r.URL.Query().Get("job")
	if job == "" {
		http.Error(w, "job query parameter is required", http.StatusBadRequest)
		return
	}
switch r.Method {
	case http.MethodGet:

var result map[string]interface{}
path := fmt.Sprintf("/job/%s%s", job, jenkinsAPIBase)
if err := a.jenkinsRequest(r.Context(), http.MethodGet, path, nil, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
case http.MethodPost:
path := fmt.Sprintf("/job/%s/build", job)
if err := a.jenkinsRequest(r.Context(), http.MethodPost, path, nil, nil); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.WriteHeader(http.StatusCreated)
default:
http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
}

func (a *JenkinsAdapter) HandleJobs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
		case http.MethodGet:

var result map[string]interface{}
if err := a.jenkinsRequest(r.Context(), http.MethodGet, jenkinsAPIBase, nil, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
default:
http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
}

func (a *JenkinsAdapter) subscribeToEvents() {
	ctx := context.Background()
	a.bus.Subscribe(ctx, []events.EventType{events.DeploymentRequested}, func(e events.Event) error {
		return nil
})
}

func (a *JenkinsAdapter) jenkinsRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {

var bodyReader io.Reader
if body != nil {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
bodyReader = strings.NewReader(string(b))
}
req, err := http.NewRequestWithContext(ctx, method, a.baseURL+path, bodyReader)
if err != nil {
	return fmt.Errorf("create request: %w", err)
}
req.SetBasicAuth(a.user, a.apiToken)
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
	return fmt.Errorf("jenkins API error %d: %s", resp.StatusCode, string(b))
}
if out != nil {
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
}
return nil
}
