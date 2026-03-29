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

// LiquibaseAdapter bridges Liquibase Pro changelog events with the Forge event bus.
type LiquibaseAdapter struct {
	liquibaseURL  string
	apiKey        string
	webhookSecret string
	httpClient    *http.Client
	bus           events.Bus
}

type liquibaseWebhookPayload struct {
	Event       string `json:"event"`
	Project     string `json:"project"`
	Environment string `json:"environment"`
	Database    string `json:"database"`
	ChangeCount int    `json:"changeCount"`
	Message     string `json:"message"`
	Changesets  []struct {
		ID       string `json:"id"`
		Author   string `json:"author"`
		Filename string `json:"filename"`
		State    string `json:"state"`
	} `json:"changesets"`
}

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

type liquibaseStatusResponse struct {
	DatabaseVersion string                     `json:"databaseVersion"`
	Pending         []liquibaseChangesetStatus `json:"pending"`
	Applied         []liquibaseChangesetStatus `json:"applied"`
}

type liquibaseUpdateRequest struct {
	Tag           string `json:"tag,omitempty"`
	ChangelogFile string `json:"changelogFile,omitempty"`
	Contexts      string `json:"contexts,omitempty"`
	Labels        string `json:"labels,omitempty"`
}

type liquibaseUpdateResponse struct {
	ChangesetsApplied int    `json:"changesetsApplied"`
	Success           bool   `json:"success"`
	Message           string `json:"message,omitempty"`
}

func main() {
	logger.Init("liquibase-adapter")

	liquibaseURL := os.Getenv("LIQUIBASE_URL")
	if liquibaseURL == "" {
		slog.Error("LIQUIBASE_URL is required")
		os.Exit(1)
	}
	apiKey := os.Getenv("LIQUIBASE_API_KEY")
	if apiKey == "" {
		slog.Error("LIQUIBASE_API_KEY is required")
		os.Exit(1)
	}

	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		slog.Error("failed to create event bus", slog.Any("error", err))
		os.Exit(1)
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

	slog.Info("Liquibase adapter listening", slog.String("addr", ":19138"))
	http.ListenAndServe(":19138", logger.HTTPMiddleware("liquibase-adapter", mux))
}

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
			slog.Error("failed to publish task completed event", slog.Any("error", err))
		}
	case payload.Event == "update.failed" || payload.Event == "rollback.failed":
		base["reason"] = fmt.Sprintf("Liquibase %s failed for database %s in project %s: %s",
			payload.Event, payload.Database, payload.Project, payload.Message)
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

// HandleStatus returns the current Liquibase changelog status.
//
//	GET /api/v1/status
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

// HandleUpdate triggers a Liquibase update or returns status.
//
//	GET  /api/v1/update
//	POST /api/v1/update
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
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
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

// subscribeToEvents listens for DeploymentRequested events and triggers a Liquibase
// update if liquibase_tag is present in the payload.
func (a *LiquibaseAdapter) subscribeToEvents() {
	ctx := context.Background()
	if err := a.bus.Subscribe(ctx, []events.EventType{events.DeploymentRequested}, func(e events.Event) error {
		var payload struct {
			Tag           string `json:"liquibase_tag"`
			ChangelogFile string `json:"liquibase_changelog_file"`
		}
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			return fmt.Errorf("unmarshal deployment requested payload: %w", err)
		}
		if payload.Tag == "" && payload.ChangelogFile == "" {
			return nil
		}

		slog.Info("triggering Liquibase update for deployment",
			slog.String("tag", payload.Tag),
			slog.String("task_id", e.TaskID))

		req := liquibaseUpdateRequest{
			Tag:           payload.Tag,
			ChangelogFile: payload.ChangelogFile,
		}
		var result liquibaseUpdateResponse
		if err := a.liquibaseRequest(ctx, http.MethodPost, "/liquibase/update", req, &result); err != nil {
			slog.Warn("failed to trigger Liquibase update",
				slog.String("tag", payload.Tag),
				slog.Any("error", err))
		}
		return nil
	}); err != nil {
		slog.Error("failed to subscribe to deployment requested events", slog.Any("error", err))
	}
}

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
