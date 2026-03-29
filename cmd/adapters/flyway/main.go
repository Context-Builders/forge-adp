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
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/dotrage/forge-adp/pkg/events"
	"github.com/dotrage/forge-adp/pkg/logger"
)

// FlywayAdapter bridges Flyway Teams/Enterprise migration events with the Forge event bus.
type FlywayAdapter struct {
	flywayURL     string
	token         string
	webhookSecret string
	httpClient    *http.Client
	bus           events.Bus
}

type flywayWebhookPayload struct {
	Event       string `json:"event"`
	Environment string `json:"environment"`
	Schema      string `json:"schema"`
	Migrated    int    `json:"migrated"`
	Message     string `json:"message"`
	Migrations  []struct {
		Version     string `json:"version"`
		Description string `json:"description"`
		Type        string `json:"type"`
		Script      string `json:"script"`
		State       string `json:"state"`
	} `json:"migrations"`
}

type flywayMigrationInfo struct {
	Version       string `json:"version"`
	Description   string `json:"description"`
	Type          string `json:"type"`
	Script        string `json:"script"`
	State         string `json:"state"`
	InstalledOn   string `json:"installedOn,omitempty"`
	ExecutionTime int    `json:"executionTime,omitempty"`
}

type flywayInfoResponse struct {
	SchemaVersion string                `json:"schemaVersion"`
	Migrations    []flywayMigrationInfo `json:"migrations"`
}

type flywayMigrateRequest struct {
	Target     string   `json:"target,omitempty"`
	OutOfOrder bool     `json:"outOfOrder,omitempty"`
	Schemas    []string `json:"schemas,omitempty"`
}

type flywayMigrateResponse struct {
	MigrationsExecuted int    `json:"migrationsExecuted"`
	Success            bool   `json:"success"`
	Message            string `json:"message,omitempty"`
}

func main() {
	logger.Init("flyway-adapter")

	flywayURL := os.Getenv("FLYWAY_URL")
	if flywayURL == "" {
		slog.Error("FLYWAY_URL is required")
		os.Exit(1)
	}

	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		slog.Error("failed to create event bus", slog.Any("error", err))
		os.Exit(1)
	}

	adapter := &FlywayAdapter{
		flywayURL:     flywayURL,
		token:         os.Getenv("FLYWAY_TOKEN"),
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

	slog.Info("Flyway adapter listening", slog.String("addr", ":19137"))
	http.ListenAndServe(":19137", logger.HTTPMiddleware("flyway-adapter", mux))
}

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
			slog.Error("failed to publish task completed event", slog.Any("error", err))
		}
	case payload.Event == "migration.failed" || payload.Event == "validate.failed":
		base["reason"] = fmt.Sprintf("Flyway migration failed for schema %s on %s: %s",
			payload.Schema, payload.Environment, payload.Message)
		eventPayload, _ = json.Marshal(base)
		if err := a.bus.Publish(r.Context(), events.Event{
			Type:    events.TaskFailed,
			Payload: eventPayload,
		}); err != nil {
			slog.Error("failed to publish task failed event", slog.Any("error", err))
		}
	}
	w.WriteHeader(http.StatusOK)
}

// HandleInfo returns the current Flyway migration state.
//
//	GET /api/v1/info
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

// HandleMigrate triggers a Flyway migration.
//
//	POST /api/v1/migrate
func (a *FlywayAdapter) HandleMigrate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req flywayMigrateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
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

// subscribeToEvents listens for DeploymentRequested events and triggers a Flyway
// migration if flyway_target is present in the payload.
func (a *FlywayAdapter) subscribeToEvents() {
	ctx := context.Background()
	if err := a.bus.Subscribe(ctx, []events.EventType{events.DeploymentRequested}, func(e events.Event) error {
		var payload struct {
			Target string `json:"flyway_target"`
			Schema string `json:"flyway_schema"`
		}
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			return fmt.Errorf("unmarshal deployment requested payload: %w", err)
		}
		if payload.Target == "" && payload.Schema == "" {
			return nil
		}

		slog.Info("triggering Flyway migration for deployment",
			slog.String("target", payload.Target),
			slog.String("task_id", e.TaskID))

		req := flywayMigrateRequest{Target: payload.Target}
		if payload.Schema != "" {
			req.Schemas = []string{payload.Schema}
		}
		var result flywayMigrateResponse
		if err := a.flywayRequest(ctx, http.MethodPost, "/flyway/migrate", req, &result); err != nil {
			slog.Warn("failed to trigger Flyway migration",
				slog.String("target", payload.Target),
				slog.Any("error", err))
		}
		return nil
	}); err != nil {
		slog.Error("failed to subscribe to deployment requested events", slog.Any("error", err))
	}
}

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
