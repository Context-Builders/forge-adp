package main

import (
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

const sentryAPIBase = "https://sentry.io/api/0"

type SentryAdapter struct {
	authToken     string
	orgSlug       string
	webhookSecret string
	bus           events.Bus
	httpClient    *http.Client
}

type sentryIssue struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Level     string `json:"level"`
	Status    string `json:"status"`
	Permalink string `json:"permalink"`
	Project   struct {
		Slug string `json:"slug"`
	} `json:"project"`
}

type sentryWebhookPayload struct {
	Action string `json:"action"`
	Data   struct {
		Issue sentryIssue `json:"issue"`
	} `json:"data"`
}

func main() {
	logger.Init("sentry-adapter")

	authToken := os.Getenv("SENTRY_AUTH_TOKEN")
	if authToken == "" {
		slog.Error("SENTRY_AUTH_TOKEN is required")
		os.Exit(1)
	}

	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		slog.Error("failed to create event bus", slog.Any("error", err))
		os.Exit(1)
	}

	adapter := &SentryAdapter{
		authToken:     authToken,
		orgSlug:       os.Getenv("SENTRY_ORG_SLUG"),
		webhookSecret: os.Getenv("SENTRY_WEBHOOK_SECRET"),
		bus:           bus,
		httpClient:    &http.Client{},
	}

	go adapter.subscribeToEvents()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/webhook", adapter.HandleWebhook)
	mux.HandleFunc("/api/v1/issues", adapter.HandleIssues)
	mux.HandleFunc("/api/v1/projects", adapter.HandleProjects)

	slog.Info("Sentry adapter listening", slog.String("addr", ":19115"))
	http.ListenAndServe(":19115", logger.HTTPMiddleware("sentry-adapter", mux))
}

func (a *SentryAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
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
		sig := r.Header.Get("Sentry-Hook-Signature")
		mac := hmac.New(sha256.New, []byte(a.webhookSecret))
		mac.Write(body)
		expected := hex.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(expected), []byte(sig)) {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
	}

	var payload sentryWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	switch payload.Action {
	case "created":
		if payload.Data.Issue.Level == "error" || payload.Data.Issue.Level == "fatal" {
			a.handleIssueCreated(r.Context(), payload.Data.Issue)
		}
	case "resolved":
		a.handleIssueResolved(r.Context(), payload.Data.Issue)
	}
	w.WriteHeader(http.StatusOK)
}

func (a *SentryAdapter) handleIssueCreated(ctx context.Context, issue sentryIssue) {
	ep, _ := json.Marshal(map[string]interface{}{
		"issue_id": issue.ID,
		"title":    issue.Title,
		"level":    issue.Level,
		"project":  issue.Project.Slug,
		"url":      issue.Permalink,
		"source":   "sentry",
	})
	if err := a.bus.Publish(ctx, events.Event{Type: events.EscalationCreated, Payload: ep}); err != nil {
		slog.Error("failed to publish escalation event",
			slog.String("issue_id", issue.ID),
			slog.Any("error", err))
	}
}

func (a *SentryAdapter) handleIssueResolved(ctx context.Context, issue sentryIssue) {
	ep, _ := json.Marshal(map[string]interface{}{
		"issue_id": issue.ID,
		"title":    issue.Title,
		"project":  issue.Project.Slug,
		"source":   "sentry",
	})
	if err := a.bus.Publish(ctx, events.Event{Type: events.TaskCompleted, Payload: ep}); err != nil {
		slog.Error("failed to publish task completed event",
			slog.String("issue_id", issue.ID),
			slog.Any("error", err))
	}
}

// HandleIssues lists open Sentry issues, optionally filtered by project.
//
//	GET /api/v1/issues[?project=<slug>]
func (a *SentryAdapter) HandleIssues(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := fmt.Sprintf("/organizations/%s/issues/", a.orgSlug)
	if project := r.URL.Query().Get("project"); project != "" {
		path += "?project=" + project
	}
	var result interface{}
	if err := a.sentryRequest(r.Context(), http.MethodGet, path, nil, &result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// HandleProjects lists Sentry projects for the configured organization.
//
//	GET /api/v1/projects
func (a *SentryAdapter) HandleProjects(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var result interface{}
	if err := a.sentryRequest(r.Context(), http.MethodGet,
		fmt.Sprintf("/organizations/%s/projects/", a.orgSlug), nil, &result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// subscribeToEvents listens for TaskCompleted events and resolves the
// corresponding Sentry issue if sentry_issue_id is present in the payload.
func (a *SentryAdapter) subscribeToEvents() {
	ctx := context.Background()
	if err := a.bus.Subscribe(ctx, []events.EventType{events.TaskCompleted}, func(e events.Event) error {
		var payload struct {
			SentryIssueID string `json:"sentry_issue_id"`
		}
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			return fmt.Errorf("unmarshal task completed payload: %w", err)
		}
		if payload.SentryIssueID == "" {
			return nil // no Sentry issue to resolve
		}

		slog.Info("resolving Sentry issue from task completed event",
			slog.String("issue_id", payload.SentryIssueID),
			slog.String("task_id", e.TaskID))

		body := map[string]interface{}{"status": "resolved"}
		if err := a.sentryRequest(ctx, http.MethodPut,
			fmt.Sprintf("/issues/%s/", payload.SentryIssueID), body, nil); err != nil {
			slog.Warn("failed to resolve Sentry issue",
				slog.String("issue_id", payload.SentryIssueID),
				slog.Any("error", err))
		}
		return nil
	}); err != nil {
		slog.Error("failed to subscribe to task completed events", slog.Any("error", err))
	}
}

func (a *SentryAdapter) sentryRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, sentryAPIBase+path, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+a.authToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("sentry API error %d: %s", resp.StatusCode, string(b))
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
