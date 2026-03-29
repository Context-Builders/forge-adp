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

// AzureDevOpsReposAdapter handles Git repository webhooks and exposes REST
// endpoints to create branches, pull requests, and read commits.
type AzureDevOpsReposAdapter struct {
	organization  string
	project       string
	pat           string
	webhookSecret string
	bus           events.Bus
	httpClient    *http.Client
}

type adoPREvent struct {
	EventType string `json:"eventType"`
	Resource  struct {
		PullRequestID int    `json:"pullRequestId"`
		Title         string `json:"title"`
		Status        string `json:"status"`
		SourceBranch  string `json:"sourceRefName"`
		Repository    struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"repository"`
		URL string `json:"url"`
	} `json:"resource"`
}

func main() {
	logger.Init("azuredevopsrepos-adapter")

	org := os.Getenv("AZURE_DEVOPS_ORG")
	pat := os.Getenv("AZURE_DEVOPS_PAT")
	if org == "" || pat == "" {
		slog.Error("AZURE_DEVOPS_ORG and AZURE_DEVOPS_PAT are required")
		os.Exit(1)
	}

	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		slog.Error("failed to create event bus", slog.Any("error", err))
		os.Exit(1)
	}

	adapter := &AzureDevOpsReposAdapter{
		organization:  org,
		project:       os.Getenv("AZURE_DEVOPS_PROJECT"),
		pat:           pat,
		webhookSecret: os.Getenv("AZURE_DEVOPS_REPOS_WEBHOOK_SECRET"),
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
	mux.HandleFunc("/api/v1/commits", adapter.HandleCommits)

	slog.Info("Azure DevOps Repos adapter listening", slog.String("addr", ":19110"))
	http.ListenAndServe(":19110", logger.HTTPMiddleware("azuredevopsrepos-adapter", mux))
}

func (a *AzureDevOpsReposAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
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
		sig := r.Header.Get("X-Hub-Signature-256")
		mac := hmac.New(sha256.New, []byte(a.webhookSecret))
		mac.Write(body)
		expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(expected), []byte(sig)) {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
	}

	var payload adoPREvent
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	switch payload.EventType {
	case "git.pullrequest.created", "git.pullrequest.updated":
		a.handlePRCreated(r.Context(), payload)
	case "git.pullrequest.merged":
		a.handlePRMerged(r.Context(), payload)
	}
	w.WriteHeader(http.StatusOK)
}

func (a *AzureDevOpsReposAdapter) handlePRCreated(ctx context.Context, p adoPREvent) {
	branch := strings.TrimPrefix(p.Resource.SourceBranch, "refs/heads/")
	if !strings.HasPrefix(branch, "forge/") {
		return
	}
	ep, _ := json.Marshal(map[string]interface{}{
		"pr_id":  p.Resource.PullRequestID,
		"title":  p.Resource.Title,
		"repo":   p.Resource.Repository.Name,
		"url":    p.Resource.URL,
		"source": "azuredevopsrepos",
	})
	if err := a.bus.Publish(ctx, events.Event{Type: events.ReviewRequested, Payload: ep}); err != nil {
		slog.Error("failed to publish review requested event",
			slog.Int("pr_id", p.Resource.PullRequestID),
			slog.Any("error", err))
	}
}

func (a *AzureDevOpsReposAdapter) handlePRMerged(ctx context.Context, p adoPREvent) {
	ep, _ := json.Marshal(map[string]interface{}{
		"pr_id":  p.Resource.PullRequestID,
		"repo":   p.Resource.Repository.Name,
		"source": "azuredevopsrepos",
	})
	if err := a.bus.Publish(ctx, events.Event{Type: events.TaskCompleted, Payload: ep}); err != nil {
		slog.Error("failed to publish task completed event",
			slog.Int("pr_id", p.Resource.PullRequestID),
			slog.Any("error", err))
	}
}

// HandlePullRequests lists pull requests for a repository.
//
//	GET /api/v1/pulls?repo_id=<id>
func (a *AzureDevOpsReposAdapter) HandlePullRequests(w http.ResponseWriter, r *http.Request) {
	repoID := r.URL.Query().Get("repo_id")
	if repoID == "" {
		http.Error(w, "repo_id query parameter is required", http.StatusBadRequest)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := fmt.Sprintf("/%s/%s/_apis/git/repositories/%s/pullrequests?api-version=7.0",
		a.organization, a.project, repoID)
	var result map[string]interface{}
	if err := a.adoRequest(r.Context(), http.MethodGet, path, nil, &result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// HandleBranches lists branches (refs) for a repository.
//
//	GET /api/v1/branches?repo_id=<id>
func (a *AzureDevOpsReposAdapter) HandleBranches(w http.ResponseWriter, r *http.Request) {
	repoID := r.URL.Query().Get("repo_id")
	if repoID == "" {
		http.Error(w, "repo_id query parameter is required", http.StatusBadRequest)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := fmt.Sprintf("/%s/%s/_apis/git/repositories/%s/refs?filter=heads&api-version=7.0",
		a.organization, a.project, repoID)
	var result map[string]interface{}
	if err := a.adoRequest(r.Context(), http.MethodGet, path, nil, &result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// HandleCommits lists commits for a repository.
//
//	GET /api/v1/commits?repo_id=<id>
func (a *AzureDevOpsReposAdapter) HandleCommits(w http.ResponseWriter, r *http.Request) {
	repoID := r.URL.Query().Get("repo_id")
	if repoID == "" {
		http.Error(w, "repo_id query parameter is required", http.StatusBadRequest)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := fmt.Sprintf("/%s/%s/_apis/git/repositories/%s/commits?api-version=7.0",
		a.organization, a.project, repoID)
	var result map[string]interface{}
	if err := a.adoRequest(r.Context(), http.MethodGet, path, nil, &result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// subscribeToEvents listens for ReviewApproved events. When a PR review is
// approved, Forge can signal the ADO side by completing the PR.
func (a *AzureDevOpsReposAdapter) subscribeToEvents() {
	ctx := context.Background()
	if err := a.bus.Subscribe(ctx, []events.EventType{events.ReviewApproved}, func(e events.Event) error {
		var payload struct {
			PRID   int    `json:"pr_id"`
			RepoID string `json:"repo_id"`
		}
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			return fmt.Errorf("unmarshal review approved payload: %w", err)
		}
		if payload.PRID == 0 || payload.RepoID == "" {
			return nil // not enough info to update the PR
		}

		slog.Info("updating ADO PR status to approved",
			slog.Int("pr_id", payload.PRID),
			slog.String("task_id", e.TaskID))

		body := map[string]interface{}{
			"vote": 10, // 10 = approved in ADO
		}
		path := fmt.Sprintf("/%s/%s/_apis/git/repositories/%s/pullrequests/%d/reviewers/%s?api-version=7.0",
			a.organization, a.project, payload.RepoID, payload.PRID, "forge")
		var result map[string]interface{}
		if err := a.adoRequest(ctx, http.MethodPut, path, body, &result); err != nil {
			slog.Warn("failed to approve ADO PR",
				slog.Int("pr_id", payload.PRID),
				slog.Any("error", err))
		}
		return nil
	}); err != nil {
		slog.Error("failed to subscribe to review approved events", slog.Any("error", err))
	}
}

func (a *AzureDevOpsReposAdapter) adoRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://dev.azure.com"+path, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.SetBasicAuth("", a.pat)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("azure devops API error %d: %s", resp.StatusCode, string(b))
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
