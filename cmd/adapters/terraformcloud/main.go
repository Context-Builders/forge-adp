package main

import (
	"crypto/hmac"
	"crypto/sha512"
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

// TerraformCloudAdapter bridges Terraform Cloud run events with the Forge event bus.
const tfcAPIBase = "https://app.terraform.io/api/v2"

type TerraformCloudAdapter struct {
	token        string
	organization string
	hmacKey      string
	httpClient   *http.Client
	bus          events.Bus
}
// tfcNotificationPayload is the envelope Terraform Cloud sends for notification events.

type tfcNotificationPayload struct {
	PayloadVersion    int    `json:"payload_version"`
	NotificationConfigurationID string `json:"notification_configuration_id"`
	RunURL            string `json:"run_url"`
	RunID             string `json:"run_id"`
	RunMessage        string `json:"run_message"`
	RunCreatedAt      string `json:"run_created_at"`
	RunCreatedBy      string `json:"run_created_by"`
	WorkspaceID       string `json:"workspace_id"`
	WorkspaceName     string `json:"workspace_name"`
	OrganizationName  string `json:"organization_name"`
	Notifications     []struct {
		Message  string `json:"message"`
		Trigger  string `json:"trigger"`
		RunStatus string `json:"run_status"`
		RunUpdatedAt string `json:"run_updated_at"`
		RunUpdatedBy string `json:"run_updated_by"`
	} `json:"notifications"`
}
// tfcRun represents a Terraform Cloud run response object.

type tfcRun struct {
	Data struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Attributes struct {
			Status  string `json:"status"`
			Message string `json:"message"`
			Source  string `json:"source"`
		} `json:"attributes"`
	Relationships struct {
		Workspace struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
	} `json:"workspace"`
} `json:"relationships"`
} `json:"data"`
}
// tfcRunCreateRequest is the payload for creating a new run.

type tfcRunCreateRequest struct {
	Data struct {
		Attributes struct {
			IsDestroy bool   `json:"is-destroy"`
			Message   string `json:"message"`
		} `json:"attributes"`
	Type string `json:"type"`
	Relationships struct {
		Workspace struct {
			Data struct {
				Type string `json:"type"`
				ID   string `json:"id"`
			} `json:"data"`
	} `json:"workspace"`
} `json:"relationships"`
} `json:"data"`
}
// tfcWorkspace represents a Terraform Cloud workspace.

type tfcWorkspace struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Attributes struct {
		Name             string `json:"name"`
		Environment      string `json:"environment"`
		AutoApply        bool   `json:"auto-apply"`
		Locked           bool   `json:"locked"`
		WorkingDirectory string `json:"working-directory"`
		TerraformVersion string `json:"terraform-version"`
	} `json:"attributes"`
}
// tfcWorkspacesResponse is the list response from /organizations/{org}/workspaces.

type tfcWorkspacesResponse struct {
	Data []tfcWorkspace `json:"data"`
}

func main() {
	token := os.Getenv("TFC_TOKEN")
	if token == "" {
		log.Fatal("TFC_TOKEN is required")
	}
organization := os.Getenv("TFC_ORGANIZATION")
if organization == "" {
	log.Fatal("TFC_ORGANIZATION is required")
}
bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
if err != nil {
	log.Fatalf("failed to create event bus: %v", err)
}
adapter := &TerraformCloudAdapter{
	token:        token,
	organization: organization,
	hmacKey:      os.Getenv("TFC_WEBHOOK_HMAC_KEY"),
	httpClient:   &http.Client{},
	bus:          bus,
}
go adapter.subscribeToEvents()
mux := http.NewServeMux()
mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})
mux.HandleFunc("/webhook", adapter.HandleWebhook)
mux.HandleFunc("/api/v1/runs", adapter.HandleRuns)
mux.HandleFunc("/api/v1/workspaces", adapter.HandleWorkspaces)
log.Printf("Terraform Cloud adapter listening on :19104")
http.ListenAndServe(":19104", mux)
}
// verifySignature validates the Terraform Cloud notification HMAC-SHA512 signature.

func (a *TerraformCloudAdapter) verifySignature(r *http.Request, body []byte) bool {
	if a.hmacKey == "" {
		return true
	}
sig := r.Header.Get("X-TFE-Notification-Signature")
if sig == "" {
	return false
}
mac := hmac.New(sha512.New, []byte(a.hmacKey))
mac.Write(body)
expected := hex.EncodeToString(mac.Sum(nil))
return hmac.Equal([]byte(sig), []byte(expected))
}
// HandleWebhook processes inbound Terraform Cloud notification events.

func (a *TerraformCloudAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
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

var payload tfcNotificationPayload
if err := json.Unmarshal(body, &payload); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}
for _, n := range payload.Notifications {
	switch n.RunStatus {
		case "applied":
		eventPayload, _ := json.Marshal(map[string]interface{}{
			"source":         "terraform-cloud",
			"run_id":         payload.RunID,
			"run_url":        payload.RunURL,
			"workspace":      payload.WorkspaceName,
			"organization":   payload.OrganizationName,
			"message":        payload.RunMessage,
			"applied_by":     n.RunUpdatedBy,
	})
if err := a.bus.Publish(r.Context(), events.Event{
	Type:    events.TaskCompleted,
	Payload: eventPayload,
}); err != nil {
log.Printf("failed to publish task completed event: %v", err)
}
case "planned_and_finished":
eventPayload, _ := json.Marshal(map[string]interface{}{
	"source":       "terraform-cloud",
	"run_id":       payload.RunID,
	"run_url":      payload.RunURL,
	"workspace":    payload.WorkspaceName,
	"organization": payload.OrganizationName,
	"message":      payload.RunMessage,
})
if err := a.bus.Publish(r.Context(), events.Event{
	Type:    events.DeploymentApproved,
	Payload: eventPayload,
}); err != nil {
log.Printf("failed to publish deployment approved event: %v", err)
}
case "errored", "canceled", "force_canceled":
eventPayload, _ := json.Marshal(map[string]interface{}{
	"source":       "terraform-cloud",
	"run_id":       payload.RunID,
	"run_url":      payload.RunURL,
	"workspace":    payload.WorkspaceName,
	"organization": payload.OrganizationName,
	"status":       n.RunStatus,
	"reason":       fmt.Sprintf("Terraform Cloud run %s in workspace %s: %s", payload.RunID, payload.WorkspaceName, n.RunStatus),
})
if err := a.bus.Publish(r.Context(), events.Event{
	Type:    events.TaskFailed,
	Payload: eventPayload,
}); err != nil {
log.Printf("failed to publish task failed event: %v", err)
}
}
}
w.WriteHeader(http.StatusOK)
}
// HandleRuns manages Terraform Cloud runs (create or get status).

func (a *TerraformCloudAdapter) HandleRuns(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
		case http.MethodGet:
		runID := r.URL.Query().Get("id")
		if runID == "" {
			http.Error(w, "id query parameter is required", http.StatusBadRequest)
			return
		}

var result tfcRun
if err := a.tfcRequest(r.Context(), http.MethodGet, "/runs/"+runID, nil, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
case http.MethodPost:

var req tfcRunCreateRequest
if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}
req.Data.Type = "runs"
req.Data.Relationships.Workspace.Data.Type = "workspaces"

var result tfcRun
if err := a.tfcRequest(r.Context(), http.MethodPost, "/runs", req, &result); err != nil {
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
// HandleWorkspaces lists or fetches Terraform Cloud workspaces.

func (a *TerraformCloudAdapter) HandleWorkspaces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

var result tfcWorkspacesResponse
if err := a.tfcRequest(r.Context(), http.MethodGet,
fmt.Sprintf("/organizations/%s/workspaces", a.organization),
nil, &result,
); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
}
// subscribeToEvents listens for Forge deployment events and triggers Terraform runs.

func (a *TerraformCloudAdapter) subscribeToEvents() {
	ctx := context.Background()
	a.bus.Subscribe(ctx, []events.EventType{
		events.DeploymentRequested,
	}, func(e events.Event) error {
// Future: extract workspace ID from payload and trigger a TFC run.
return nil
})
}
// tfcRequest is a helper that executes an authenticated Terraform Cloud API call.

func (a *TerraformCloudAdapter) tfcRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {

var bodyReader io.Reader
if body != nil {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
bodyReader = bytes.NewReader(b)
}
req, err := http.NewRequestWithContext(ctx, method, tfcAPIBase+path, bodyReader)
if err != nil {
	return fmt.Errorf("create request: %w", err)
}
req.Header.Set("Authorization", "Bearer "+a.token)
req.Header.Set("Content-Type", "application/vnd.api+json")
resp, err := a.httpClient.Do(req)
if err != nil {
	return fmt.Errorf("execute request: %w", err)
}
defer resp.Body.Close()
if resp.StatusCode >= 300 {
	b, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("terraform cloud API error %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
}
if out != nil {
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
}
return nil
}
