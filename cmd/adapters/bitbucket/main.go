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

const bitbucketAPIBase = "https://api.bitbucket.org/2.0"

type BitbucketAdapter struct {
	username      string
	appPassword   string
	webhookSecret string
	bus           events.Bus
	httpClient    *http.Client
}

type bitbucketPR struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
	State string `json:"state"`
	Source struct {
		Branch struct {
			Name string `json:"name"`
		} `json:"branch"`
	} `json:"source"`
	Links struct {
		HTML struct {
			Href string `json:"href"`
		} `json:"html"`
	} `json:"links"`
}

type bitbucketWebhookPayload struct {
	Event       string      `json:"event"`
	PullRequest bitbucketPR `json:"pullrequest"`
	Repository  struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
}

func main() {
	logger.Init("bitbucket-adapter")

	username := os.Getenv("BITBUCKET_USERNAME")
	appPassword := os.Getenv("BITBUCKET_APP_PASSWORD")
	if username == "" || appPassword == "" {
		slog.Error("BITBUCKET_USERNAME and BITBUCKET_APP_PASSWORD are required")
		os.Exit(1)
	}

	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		slog.Error("failed to create event bus", slog.Any("error", err))
		os.Exit(1)
	}

	adapter := &BitbucketAdapter{
		username:      username,
		appPassword:   appPassword,
		webhookSecret: os.Getenv("BITBUCKET_WEBHOOK_SECRET"),
		bus:           bus,
		httpClient:    &http.Client{},
	}

	go adapter.subscribeToEvents()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/webhook", adapter.HandleWebhook)
	mux.HandleFunc("/api/v1/pulls", adapter.HandlePullRequests)
	mux.HandleFunc("/api/v1/branches", adapter.HandleBranches)

	slog.Info("Bitbucket adapter listening", slog.String("addr", ":19109"))
	http.ListenAndServe(":19109", logger.HTTPMiddleware("bitbucket-adapter", mux))
}

func (a *BitbucketAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
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
		sig := r.Header.Get("X-Hub-Signature")
		mac := hmac.New(sha256.New, []byte(a.webhookSecret))
		mac.Write(body)
		expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(expected), []byte(sig)) {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
	}

	var payload bitbucketWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	eventType := r.Header.Get("X-Event-Key")
	switch eventType {
	case "pullrequest:created", "pullrequest:updated":
		a.handlePROpened(r.Context(), payload)
	case "pullrequest:fulfilled":
		a.handlePRMerged(r.Context(), payload)
	case "pullrequest:rejected":
		a.handlePRDeclined(r.Context(), payload)
	}
	w.WriteHeader(http.StatusOK)
}

func (a *BitbucketAdapter) handlePROpened(ctx context.Context, p bitbucketWebhookPayload) {
	if !strings.HasPrefix(p.PullRequest.Source.Branch.Name, "forge/") {
		return
	}
	ep, _ := json.Marshal(map[string]interface{}{
		"pr_number": p.PullRequest.ID,
		"pr_title":  p.PullRequest.Title,
		"repo":      p.Repository.FullName,
		"url":       p.PullRequest.Links.HTML.Href,
		"source":    "bitbucket",
	})
	if err := a.bus.Publish(ctx, events.Event{Type: events.ReviewRequested, Payload: ep}); err != nil {
		slog.Error("failed to publish review requested event",
			slog.Int("pr_number", p.PullRequest.ID),
			slog.Any("error", err))
	}
}

func (a *BitbucketAdapter) handlePRMerged(ctx context.Context, p bitbucketWebhookPayload) {
	ep, _ := json.Marshal(map[string]interface{}{
		"pr_number": p.PullRequest.ID,
		"repo":      p.Repository.FullName,
		"source":    "bitbucket",
	})
	if err := a.bus.Publish(ctx, events.Event{Type: events.TaskCompleted, Payload: ep}); err != nil {
		slog.Error("failed to publish task completed event",
			slog.Int("pr_number", p.PullRequest.ID),
			slog.Any("error", err))
	}
}

func (a *BitbucketAdapter) handlePRDeclined(ctx context.Context, p bitbucketWebhookPayload) {
	ep, _ := json.Marshal(map[string]interface{}{
		"pr_number": p.PullRequest.ID,
		"repo":      p.Repository.FullName,
		"source":    "bitbucket",
	})
	if err := a.bus.Publish(ctx, events.Event{Type: events.ReviewRejected, Payload: ep}); err != nil {
		slog.Error("failed to publish review rejected event",
			slog.Int("pr_number", p.PullRequest.ID),
			slog.Any("error", err))
	}
}

// HandlePullRequests lists pull requests for a Bitbucket repository.
//
//	GET /api/v1/pulls?workspace=<ws>&repo=<repo>
func (a *BitbucketAdapter) HandlePullRequests(w http.ResponseWriter, r *http.Request) {
	workspace := r.URL.Query().Get("workspace")
	repo := r.URL.Query().Get("repo")
	if workspace == "" || repo == "" {
		http.Error(w, "workspace and repo query parameters are required", http.StatusBadRequest)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var result map[string]interface{}
	path := fmt.Sprintf("/repositories/%s/%s/pullrequests", workspace, repo)
	if err := a.bbRequest(r.Context(), http.MethodGet, path, nil, &result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// HandleBranches lists branches for a Bitbucket repository.
//
//	GET /api/v1/branches?workspace=<ws>&repo=<repo>
func (a *BitbucketAdapter) HandleBranches(w http.ResponseWriter, r *http.Request) {
	workspace := r.URL.Query().Get("workspace")
	repo := r.URL.Query().Get("repo")
	if workspace == "" || repo == "" {
		http.Error(w, "workspace and repo query parameters are required", http.StatusBadRequest)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var result map[string]interface{}
	path := fmt.Sprintf("/repositories/%s/%s/refs/branches", workspace, repo)
	if err := a.bbRequest(r.Context(), http.MethodGet, path, nil, &result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// subscribeToEvents listens for ReviewApproved events and approves the
// corresponding Bitbucket PR if pr_number and repo info are present.
func (a *BitbucketAdapter) subscribeToEvents() {
	ctx := context.Background()
	if err := a.bus.Subscribe(ctx, []events.EventType{events.ReviewApproved}, func(e events.Event) error {
		var payload struct {
			PRNumber  int    `json:"pr_number"`
			Workspace string `json:"workspace"`
			Repo      string `json:"repo"`
		}
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			return fmt.Errorf("unmarshal review approved payload: %w", err)
		}
		if payload.PRNumber == 0 || payload.Workspace == "" || payload.Repo == "" {
			return nil // not enough info to approve the PR
		}

		slog.Info("approving Bitbucket PR from review approved event",
			slog.Int("pr_number", payload.PRNumber),
			slog.String("repo", payload.Repo),
			slog.String("task_id", e.TaskID))

		path := fmt.Sprintf("/repositories/%s/%s/pullrequests/%d/approve",
			payload.Workspace, payload.Repo, payload.PRNumber)
		if err := a.bbRequest(ctx, http.MethodPost, path, nil, nil); err != nil {
			slog.Warn("failed to approve Bitbucket PR",
				slog.Int("pr_number", payload.PRNumber),
				slog.Any("error", err))
		}
		return nil
	}); err != nil {
		slog.Error("failed to subscribe to review approved events", slog.Any("error", err))
	}
}

func (a *BitbucketAdapter) bbRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, bitbucketAPIBase+path, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.SetBasicAuth(a.username, a.appPassword)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("bitbucket API error %d: %s", resp.StatusCode, string(b))
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
