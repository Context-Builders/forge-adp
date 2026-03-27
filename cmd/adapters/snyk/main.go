package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"github.com/dotrage/forge-adp/pkg/events"
	"bytes"
)

// SnykAdapter bridges Snyk vulnerability events with the Forge event bus.
const snykAPIBase = "https://api.snyk.io/v1"

type SnykAdapter struct {
	apiToken      string
	orgID         string
	webhookSecret string
	httpClient    *http.Client
	bus           events.Bus
}
// snykWebhookPayload is the envelope Snyk sends for project events.

type snykWebhookPayload struct {
	Project struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"project"`
Vulnerabilities []struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Severity string `json:"severity"`
	CVSSv3   string `json:"CVSSv3"`
} `json:"vulnerabilities,omitempty"`
NewIssues []struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Severity string `json:"severity"`
} `json:"newIssues,omitempty"`
}
// snykProject represents a Snyk project as returned by the REST API.

type snykProject struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}
// snykProjectsResponse is the list response from /org/{orgID}/projects.

type snykProjectsResponse struct {
	Projects []snykProject `json:"projects"`
}
// snykIssuesResponse wraps the vulnerability list response.

type snykIssuesResponse struct {
	Results []struct {
		Issues struct {
			Vulnerabilities []struct {
				ID       string `json:"id"`
				Title    string `json:"title"`
				Severity string `json:"severity"`
			} `json:"vulnerabilities"`
	} `json:"issues"`
} `json:"results"`
}

func main() {
	apiToken := os.Getenv("SNYK_API_TOKEN")
	if apiToken == "" {
		log.Fatal("SNYK_API_TOKEN is required")
	}
orgID := os.Getenv("SNYK_ORG_ID")
if orgID == "" {
	log.Fatal("SNYK_ORG_ID is required")
}
bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
if err != nil {
	log.Fatalf("failed to create event bus: %v", err)
}
adapter := &SnykAdapter{
	apiToken:      apiToken,
	orgID:         orgID,
	webhookSecret: os.Getenv("SNYK_WEBHOOK_SECRET"),
	httpClient:    &http.Client{},
	bus:           bus,
}
go adapter.subscribeToEvents()
mux := http.NewServeMux()
mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})
mux.HandleFunc("/webhook", adapter.HandleWebhook)
mux.HandleFunc("/api/v1/vulnerabilities", adapter.HandleVulnerabilities)
mux.HandleFunc("/api/v1/projects", adapter.HandleProjects)
log.Printf("Snyk adapter listening on :19102")
http.ListenAndServe(":19102", mux)
}
// verifySignature validates the Snyk webhook HMAC-SHA256 signature.

func (a *SnykAdapter) verifySignature(r *http.Request, body []byte) bool {
	if a.webhookSecret == "" {
		return true
	}
sig := r.Header.Get("X-Snyk-Signature")
if sig == "" {
	return false
}
mac := hmac.New(sha256.New, []byte(a.webhookSecret))
mac.Write(body)
expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
return hmac.Equal([]byte(sig), []byte(expected))
}
// HandleWebhook processes inbound Snyk webhook events.

func (a *SnykAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
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

var payload snykWebhookPayload
if err := json.Unmarshal(body, &payload); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}
// Count critical/high new issues.

var criticalIssues []string
for _, issue := range payload.NewIssues {
	if issue.Severity == "critical" || issue.Severity == "high" {
		criticalIssues = append(criticalIssues, fmt.Sprintf("%s (%s)", issue.Title, issue.Severity))
	}
}
if len(criticalIssues) > 0 {
	eventPayload, _ := json.Marshal(map[string]interface{}{
		"source":      "snyk",
		"project_id":  payload.Project.ID,
		"project":     payload.Project.Name,
		"issues":      criticalIssues,
		"issue_count": len(criticalIssues),
		"reason":      fmt.Sprintf("Snyk detected %d new critical/high vulnerabilities in %s", len(criticalIssues), payload.Project.Name),
})
if err := a.bus.Publish(r.Context(), events.Event{
	Type:    events.EscalationCreated,
	Payload: eventPayload,
}); err != nil {
log.Printf("failed to publish escalation event: %v", err)
}
}
w.WriteHeader(http.StatusOK)
}
// HandleVulnerabilities proxies vulnerability queries for a specific project to Snyk's API.

func (a *SnykAdapter) HandleVulnerabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
projectID := r.URL.Query().Get("project_id")
if projectID == "" {
	http.Error(w, "project_id query parameter is required", http.StatusBadRequest)
	return
}

var result snykIssuesResponse
if err := a.snykRequest(r.Context(), http.MethodPost,
fmt.Sprintf("/org/%s/project/%s/aggregated-issues", a.orgID, projectID),
map[string]interface{}{"includeDescription": false},
&result,
); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
}
// HandleProjects lists Snyk projects for the configured organisation.

func (a *SnykAdapter) HandleProjects(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

var result snykProjectsResponse
if err := a.snykRequest(r.Context(), http.MethodGet,
fmt.Sprintf("/org/%s/projects", a.orgID),
nil,
&result,
); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
}
// subscribeToEvents listens for Forge events that should trigger Snyk actions.

func (a *SnykAdapter) subscribeToEvents() {
	ctx := context.Background()
	a.bus.Subscribe(ctx, []events.EventType{
		events.TaskCreated,
	}, func(e events.Event) error {
// Future: trigger a Snyk test when a new task with security label is created.
return nil
})
}
// snykRequest is a helper that executes an authenticated Snyk REST API call.

func (a *SnykAdapter) snykRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {

var bodyReader io.Reader
if body != nil {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
bodyReader = bytes.NewReader(b)
}
req, err := http.NewRequestWithContext(ctx, method, snykAPIBase+path, bodyReader)
if err != nil {
	return fmt.Errorf("create request: %w", err)
}
req.Header.Set("Authorization", "token "+a.apiToken)
req.Header.Set("Content-Type", "application/json")
resp, err := a.httpClient.Do(req)
if err != nil {
	return fmt.Errorf("execute request: %w", err)
}
defer resp.Body.Close()
if resp.StatusCode >= 300 {
	b, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("snyk API error %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
}
if out != nil {
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
}
return nil
}
