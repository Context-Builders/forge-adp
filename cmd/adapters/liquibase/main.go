package main

import (
	"bytes"
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
)

// LiquibaseAdapter bridges Liquibase Pro changelog events with the Forge event bus.

type LiquibaseAdapter struct {
	liquibaseURL  string
	apiKey        string
	webhookSecret string
	httpClient    *http.Client
	bus           events.Bus
}

type liquibaseWebhookPayload struct {// liquibaseWebhookPayload is the envelope Liquibase sends to configured webhook URLs on changelog events.
	Event       string `json:"event"`        // update.success, update.failed, rollback.success, rollback.failed
	Project     string `json:"project"`
	Environment string `json:"environment"`
	Database    string `json:"database"`
	ChangeCount int    `json:"changeCount"`
	Message     string `json:"message"`
	Changesets  []struct {
		ID       string `json:"id"`
		Author   string `json:"author"`
		Filename string `json:"filename"`
		State    string `json:"state"` // EXECUTED, FAILED, SKIPPED
	} `json:"changesets"`
}
// liquibaseChangesetStatus represents a single changeset row from Liquibase's status response.

type liquibaseChangesetStatus struct {
	ID            string `json:"id"`
	Author        string `json:"author"`
	Filename      string `json:"filename"`
	DeploymentID  string `json:"deploymentId,omitempty"`
	ExecType      string `json:"execType"`
	DateExecuted  string `json:"dateExecuted,omitempty"`
	OrderExecuted int    `json:"orderExecuted,omitempty"`
	MD5Sum        string `json:"md5Sum"`
}
// liquibaseStatusResponse wraps the list of changeset status records from /liquibase/status.

type liquibaseStatusResponse struct {
	DatabaseVersion string                     `json:"databaseVersion"`
	Pending         []liquibaseChangesetStatus `json:"pending"`
	Applied         []liquibaseChangesetStatus `json:"applied"`
}

type liquibaseUpdateRequest struct {// liquibaseUpdateRequest is the body for triggering a Liquibase update operation via the API.
	Tag         string `json:"tag,omitempty"`         // apply up to this tag; blank means apply all pending
	ChangelogFile string `json:"changelogFile,omitempty"`
	Contexts    string `json:"contexts,omitempty"`
	Labels      string `json:"labels,omitempty"`
}
// liquibaseUpdateResponse is returned after a successful update API call.

type liquibaseUpdateResponse struct {
	ChangesetsApplied int    `json:"changesetsApplied"`
	Success           bool   `json:"success"`
	Message           string `json:"message,omitempty"`
}
// verifySignature validates the Liquibase webhook HMAC-SHA256 signature.

func (a *LiquibaseAdapter) verifySignature(r *http.Request, body []byte) bool {
	if a.webhookSecret == "" {
		return true
	}
sig := r.Header.Get("X-Liquibase-Signature")
if sig == "" {
	return false
}
mac := hmac.New(sha256.New, []byte(a.webhookSecret))
mac.Write(body)
expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
return hmac.Equal([]byte(sig), []byte(expected))
}
// HandleWebhook processes inbound Liquibase Pro event notifications.

func (a *LiquibaseAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
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

var payload liquibaseWebhookPayload
if err := json.Unmarshal(body, &payload); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}
base := map[string]interface{}{
	"source":      "liquibase",
	"event":       payload.Event,
	"project":     payload.Project,
	"environment": payload.Environment,
	"database":    payload.Database,
	"changeCount": payload.ChangeCount,
	"message":     payload.Message,
}
eventPayload, _ := json.Marshal(base)
switch {
	case payload.Event == "update.success" || payload.Event == "rollback.success":
	if err := a.bus.Publish(r.Context(), events.Event{
		Type:    events.TaskCompleted,
		Payload: eventPayload,
}); err != nil {
log.Printf("failed to publish task completed event: %v", err)
}
case payload.Event == "update.failed" || payload.Event == "rollback.failed":
base["reason"] = fmt.Sprintf("Liquibase %s failed for database %s in project %s: %s", payload.Event, payload.Database, payload.Project, payload.Message)
eventPayload, _ = json.Marshal(base)
if err := a.bus.Publish(r.Context(), events.Event{
	Type:    events.TaskFailed,
	Payload: eventPayload,
}); err != nil {
log.Printf("failed to publish task failed event: %v", err)
}
}
w.WriteHeader(http.StatusOK)
}
// HandleStatus returns the current Liquibase changelog status (pending and applied changesets).

func (a *LiquibaseAdapter) HandleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

var result liquibaseStatusResponse
if err := a.liquibaseRequest(r.Context(), http.MethodGet, "/liquibase/status", nil, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
}
// HandleUpdate triggers a Liquibase update (apply pending changesets) via the connected Liquibase Pro API.

func (a *LiquibaseAdapter) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
		case http.MethodGet:

var result liquibaseStatusResponse
if err := a.liquibaseRequest(r.Context(), http.MethodGet, "/liquibase/status", nil, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
case http.MethodPost:

var req liquibaseUpdateRequest
if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}

var result liquibaseUpdateResponse
if err := a.liquibaseRequest(r.Context(), http.MethodPost, "/liquibase/update", req, &result); err != nil {
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
// subscribeToEvents listens for Forge deployment requested events and triggers Liquibase updates.

func (a *LiquibaseAdapter) subscribeToEvents() {
	ctx := context.Background()
	a.bus.Subscribe(ctx, []events.EventType{
		events.DeploymentRequested,
	}, func(e events.Event) error {
// Future: parse deployment payload, resolve target database, and call /liquibase/update.
return nil
})
}
// liquibaseRequest is a helper that executes an authenticated call to the Liquibase Pro REST API.

func (a *LiquibaseAdapter) liquibaseRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {

var bodyReader io.Reader
if body != nil {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
bodyReader = bytes.NewReader(b)
}
req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(a.liquibaseURL, "/")+path, bodyReader)
if err != nil {
	return fmt.Errorf("create request: %w", err)
}
req.Header.Set("Content-Type", "application/json")
if a.apiKey != "" {
	req.Header.Set("X-Liquibase-Api-Key", a.apiKey)
}
resp, err := a.httpClient.Do(req)
if err != nil {
	return fmt.Errorf("execute request: %w", err)
}
defer resp.Body.Close()
if resp.StatusCode >= 300 {
	b, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("liquibase API error %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
}
if out != nil {
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
}
return nil
}

func main() {
	liquibaseURL := os.Getenv("LIQUIBASE_URL")
	if liquibaseURL == "" {
		log.Fatal("LIQUIBASE_URL is required")
	}
apiKey := os.Getenv("LIQUIBASE_API_KEY")
if apiKey == "" {
	log.Fatal("LIQUIBASE_API_KEY is required")
}
bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
if err != nil {
	log.Fatalf("failed to create event bus: %v", err)
}
adapter := &LiquibaseAdapter{
	liquibaseURL:  liquibaseURL,
	apiKey:        apiKey,
	webhookSecret: os.Getenv("LIQUIBASE_WEBHOOK_SECRET"),
	httpClient:    &http.Client{},
	bus:           bus,
}
go adapter.subscribeToEvents()
mux := http.NewServeMux()
mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})
mux.HandleFunc("/webhook", adapter.HandleWebhook)
mux.HandleFunc("/api/v1/status", adapter.HandleStatus)
mux.HandleFunc("/api/v1/update", adapter.HandleUpdate)
log.Printf("Liquibase adapter listening on :19138")
http.ListenAndServe(":19138", mux)
}
