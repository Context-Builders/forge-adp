package main

import (
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
	"context"
)

// AtlantisAdapter bridges Atlantis Terraform automation events with the Forge event bus.)

type AtlantisAdapter struct {
	atlantisURL   string
	token         string
	webhookSecret string
	httpClient    *http.Client
	bus           events.Bus
}

type atlantisWebhookPayload struct {// atlantisWebhookPayload is the payload Atlantis sends to configured webhook URLs.
	Stage     string `json:"stage"`       // plan, apply
	Operation string `json:"operation"`   // plan, apply, destroy
	Status    string `json:"status"`      // success, failure, error
	Repo      struct {
		FullName string `json:"full_name"`
	} `json:"repo"`
Pull *struct {
	Num    int    `json:"num"`
	Branch string `json:"branch"`
	Author string `json:"author"`
} `json:"pull"`
HeadCommit    string `json:"head_commit"`
Log           string `json:"log"`
ProjectResults []struct {
	ProjectName   string `json:"projectName"`
	Workspace     string `json:"workspace"`
	RepoRelDir    string `json:"repoRelDir"`
	Status        string `json:"status"`
	PlanSuccess   *struct {
		TerraformOutput string `json:"terraformOutput"`
		NumAdditions    int    `json:"numAdditions"`
		NumChanges      int    `json:"numChanges"`
		NumDestructions int    `json:"numDestructions"`
	} `json:"planSuccess,omitempty"`
ApplySuccess *struct {
	TerraformOutput string `json:"terraformOutput"`
} `json:"applySuccess,omitempty"`
Failure *struct {
	Error string `json:"error"`
} `json:"failure,omitempty"`
} `json:"projectResults"`
}
// atlantisPlanRequest triggers an Atlantis plan via the API.

type atlantisPlanRequest struct {
	Repo       string `json:"repo"`
	Branch     string `json:"branch"`
	Workspace  string `json:"workspace"`
	RepoRelDir string `json:"repoRelDir"`
	Verbose    bool   `json:"verbose"`
}
// atlantisApplyRequest triggers an Atlantis apply via the API.

type atlantisApplyRequest struct {
	Repo       string `json:"repo"`
	Branch     string `json:"branch"`
	Workspace  string `json:"workspace"`
	RepoRelDir string `json:"repoRelDir"`
}
// atlantisLockResponse is returned when locking a project.

type atlantisLockResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func main() {
	atlantisURL := os.Getenv("ATLANTIS_URL")
	if atlantisURL == "" {
		log.Fatal("ATLANTIS_URL is required")
	}
token := os.Getenv("ATLANTIS_TOKEN")
if token == "" {
	log.Fatal("ATLANTIS_TOKEN is required")
}
bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
if err != nil {
	log.Fatalf("failed to create event bus: %v", err)
}
adapter := &AtlantisAdapter{
	atlantisURL:   strings.TrimRight(atlantisURL, "/"),
	token:         token,
	webhookSecret: os.Getenv("ATLANTIS_WEBHOOK_SECRET"),
	httpClient:    &http.Client{},
	bus:           bus,
}
go adapter.subscribeToEvents()
mux := http.NewServeMux()
mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})
mux.HandleFunc("/webhook", adapter.HandleWebhook)
mux.HandleFunc("/api/v1/plan", adapter.HandlePlan)
mux.HandleFunc("/api/v1/apply", adapter.HandleApply)
log.Printf("Atlantis adapter listening on :19105")
http.ListenAndServe(":19105", mux)
}
// verifySignature validates the Atlantis webhook HMAC-SHA256 signature.

func (a *AtlantisAdapter) verifySignature(r *http.Request, body []byte) bool {
	if a.webhookSecret == "" {
		return true
	}
sig := r.Header.Get("X-Atlantis-Signature")
if sig == "" {
	return false
}
mac := hmac.New(sha256.New, []byte(a.webhookSecret))
mac.Write(body)
expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
return hmac.Equal([]byte(sig), []byte(expected))
}
// HandleWebhook processes inbound Atlantis event notifications.

func (a *AtlantisAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
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

var payload atlantisWebhookPayload
if err := json.Unmarshal(body, &payload); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}
base := map[string]interface{}{
	"source":    "atlantis",
	"repo":      payload.Repo.FullName,
	"stage":     payload.Stage,
	"operation": payload.Operation,
	"status":    payload.Status,
}
if payload.Pull != nil {
	base["pr_number"] = payload.Pull.Num
	base["branch"] = payload.Pull.Branch
}
switch {
	case payload.Stage == "apply" && payload.Status == "success":
	eventPayload, _ := json.Marshal(base)
	if err := a.bus.Publish(r.Context(), events.Event{
		Type:    events.TaskCompleted,
		Payload: eventPayload,
}); err != nil {
log.Printf("failed to publish task completed event: %v", err)
}
case payload.Stage == "plan" && payload.Status == "success":
eventPayload, _ := json.Marshal(base)
if err := a.bus.Publish(r.Context(), events.Event{
	Type:    events.DeploymentRequested,
	Payload: eventPayload,
}); err != nil {
log.Printf("failed to publish deployment requested event: %v", err)
}
case payload.Status == "failure" || payload.Status == "error":
base["reason"] = fmt.Sprintf("Atlantis %s %s failed for %s", payload.Stage, payload.Operation, payload.Repo.FullName)
eventPayload, _ := json.Marshal(base)
if err := a.bus.Publish(r.Context(), events.Event{
	Type:    events.TaskFailed,
	Payload: eventPayload,
}); err != nil {
log.Printf("failed to publish task failed event: %v", err)
}
}
w.WriteHeader(http.StatusOK)
}
// HandlePlan triggers an Atlantis plan operation.

func (a *AtlantisAdapter) HandlePlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

var req atlantisPlanRequest
if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}

var result map[string]interface{}
if err := a.atlantisRequest(r.Context(), http.MethodPost, "/api/v1/plan", req, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
}
// HandleApply triggers an Atlantis apply operation.

func (a *AtlantisAdapter) HandleApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

var req atlantisApplyRequest
if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}

var result map[string]interface{}
if err := a.atlantisRequest(r.Context(), http.MethodPost, "/api/v1/apply", req, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
}
// subscribeToEvents listens for Forge deployment approval events and triggers Atlantis applies.

func (a *AtlantisAdapter) subscribeToEvents() {
	ctx := context.Background()
	a.bus.Subscribe(ctx, []events.EventType{
		events.DeploymentApproved,
	}, func(e events.Event) error {
// Future: extract repo/workspace from payload and call atlantis apply.
return nil
})
}
// atlantisRequest is a helper that executes an authenticated Atlantis API call.

func (a *AtlantisAdapter) atlantisRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {

var bodyReader io.Reader
if body != nil {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
bodyReader = bytes.NewReader(b)
}
req, err := http.NewRequestWithContext(ctx, method, a.atlantisURL+path, bodyReader)
if err != nil {
	return fmt.Errorf("create request: %w", err)
}
req.Header.Set("X-Atlantis-Token", a.token)
req.Header.Set("Content-Type", "application/json")
resp, err := a.httpClient.Do(req)
if err != nil {
	return fmt.Errorf("execute request: %w", err)
}
defer resp.Body.Close()
if resp.StatusCode >= 300 {
	b, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("atlantis API error %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
}
if out != nil {
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
}
return nil
}
