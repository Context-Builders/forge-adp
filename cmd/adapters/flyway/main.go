package main

import (
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
	"crypto/hmac"
	"crypto/sha256"
)

// FlywayAdapter bridges Flyway Teams/Enterprise migration events with the Forge event bus.)

type FlywayAdapter struct {
	flywayURL     string
	token         string
	webhookSecret string
	httpClient    *http.Client
	bus           events.Bus
}

type flywayWebhookPayload struct {// flywayWebhookPayload is the envelope Flyway sends to configured webhook URLs on migration events.
	Event       string `json:"event"`        // migration.success, migration.failed, validate.failed
	Environment string `json:"environment"`  // target environment name
	Schema      string `json:"schema"`       // target schema/database
	Migrated    int    `json:"migrated"`     // number of migrations applied
	Message     string `json:"message"`
	Migrations  []struct {
		Version     string `json:"version"`
		Description string `json:"description"`
		Type        string `json:"type"`
		Script      string `json:"script"`
		State       string `json:"state"` // Success, Failed
	} `json:"migrations"`
}
// flywayMigrationInfo represents a single row from Flyway's migration history.

type flywayMigrationInfo struct {
	Version        string `json:"version"`
	Description    string `json:"description"`
	Type           string `json:"type"`
	Script         string `json:"script"`
	State          string `json:"state"`
	InstalledOn    string `json:"installedOn,omitempty"`
	ExecutionTime  int    `json:"executionTime,omitempty"` // milliseconds
}
// flywayInfoResponse wraps the list of migration info records from /flyway/info.

type flywayInfoResponse struct {
	SchemaVersion string                `json:"schemaVersion"`
	Migrations    []flywayMigrationInfo `json:"migrations"`
}

type flywayMigrateRequest struct {// flywayMigrateRequest is the optional body for triggering a Flyway migration via the API.
	Target      string `json:"target,omitempty"`      // target version; blank means latest
	OutOfOrder  bool   `json:"outOfOrder,omitempty"`
	Schemas     []string `json:"schemas,omitempty"`
}
// flywayMigrateResponse is returned after a successful migration API call.

type flywayMigrateResponse struct {
	MigrationsExecuted int    `json:"migrationsExecuted"`
	Success            bool   `json:"success"`
	Message            string `json:"message,omitempty"`
}
// verifySignature validates the Flyway webhook HMAC-SHA256 signature.

func (a *FlywayAdapter) verifySignature(r *http.Request, body []byte) bool {
	if a.webhookSecret == "" {
		return true
	}
sig := r.Header.Get("X-Flyway-Signature")
if sig == "" {
	return false
}
mac := hmac.New(sha256.New, []byte(a.webhookSecret))
mac.Write(body)
expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
return hmac.Equal([]byte(sig), []byte(expected))
}
// HandleWebhook processes inbound Flyway event notifications.

func (a *FlywayAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
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

var payload flywayWebhookPayload
if err := json.Unmarshal(body, &payload); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}
base := map[string]interface{}{
	"source":      "flyway",
	"event":       payload.Event,
	"environment": payload.Environment,
	"schema":      payload.Schema,
	"migrated":    payload.Migrated,
	"message":     payload.Message,
}
eventPayload, _ := json.Marshal(base)
switch {
	case payload.Event == "migration.success":
	if err := a.bus.Publish(r.Context(), events.Event{
		Type:    events.TaskCompleted,
		Payload: eventPayload,
}); err != nil {
log.Printf("failed to publish task completed event: %v", err)
}
case payload.Event == "migration.failed" || payload.Event == "validate.failed":
base["reason"] = fmt.Sprintf("Flyway migration failed for schema %s on %s: %s", payload.Schema, payload.Environment, payload.Message)
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
// HandleInfo returns the current Flyway migration state from the connected Flyway server.

func (a *FlywayAdapter) HandleInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

var result flywayInfoResponse
if err := a.flywayRequest(r.Context(), http.MethodGet, "/flyway/info", nil, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
}
// HandleMigrate triggers a Flyway migration via the connected Flyway server's REST endpoint.

func (a *FlywayAdapter) HandleMigrate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

var req flywayMigrateRequest
if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}

var result flywayMigrateResponse
if err := a.flywayRequest(r.Context(), http.MethodPost, "/flyway/migrate", req, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
}
// subscribeToEvents listens for Forge deployment requested events and triggers Flyway migrations.

func (a *FlywayAdapter) subscribeToEvents() {
	ctx := context.Background()
	a.bus.Subscribe(ctx, []events.EventType{
		events.DeploymentRequested,
	}, func(e events.Event) error {
// Future: parse deployment payload, resolve target schema, and call /flyway/migrate.
return nil
})
}
// flywayRequest is a helper that executes an authenticated call to the Flyway server REST API.

func (a *FlywayAdapter) flywayRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {

var bodyReader io.Reader
if body != nil {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
bodyReader = bytes.NewReader(b)
}
req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(a.flywayURL, "/")+path, bodyReader)
if err != nil {
	return fmt.Errorf("create request: %w", err)
}
req.Header.Set("Content-Type", "application/json")
if a.token != "" {
	req.Header.Set("Authorization", "Bearer "+a.token)
}
resp, err := a.httpClient.Do(req)
if err != nil {
	return fmt.Errorf("execute request: %w", err)
}
defer resp.Body.Close()
if resp.StatusCode >= 300 {
	b, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("flyway API error %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
}
if out != nil {
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
}
return nil
}

func main() {
	flywayURL := os.Getenv("FLYWAY_URL")
	if flywayURL == "" {
		log.Fatal("FLYWAY_URL is required")
	}
token := os.Getenv("FLYWAY_TOKEN")
bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
if err != nil {
	log.Fatalf("failed to create event bus: %v", err)
}
adapter := &FlywayAdapter{
	flywayURL:     flywayURL,
	token:         token,
	webhookSecret: os.Getenv("FLYWAY_WEBHOOK_SECRET"),
	httpClient:    &http.Client{},
	bus:           bus,
}
go adapter.subscribeToEvents()
mux := http.NewServeMux()
mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})
mux.HandleFunc("/webhook", adapter.HandleWebhook)
mux.HandleFunc("/api/v1/info", adapter.HandleInfo)
mux.HandleFunc("/api/v1/migrate", adapter.HandleMigrate)
log.Printf("Flyway adapter listening on :19137")
http.ListenAndServe(":19137", mux)
}
