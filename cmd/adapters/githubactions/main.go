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

	"github.com/dotrage/forge-adp/pkg/events"
	"github.com/dotrage/forge-adp/pkg/logger"
)

const githubAPIBase = "https://api.github.com"

type GitHubActionsAdapter struct {
	token         string
	webhookSecret string
	bus           events.Bus
	httpClient    *http.Client
}

type workflowRunEvent struct {
	Action      string `json:"action"`
	WorkflowRun struct {
		ID         int64  `json:"id"`
		Name       string `json:"name"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		HTMLURL    string `json:"html_url"`
		HeadBranch string `json:"head_branch"`
		HeadSHA    string `json:"head_sha"`
	} `json:"workflow_run"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
}

type checkRunEvent struct {
	Action   string `json:"action"`
	CheckRun struct {
		ID         int64  `json:"id"`
		Name       string `json:"name"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		HTMLURL    string `json:"html_url"`
	} `json:"check_run"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
}

func main() {
	logger.Init("githubactions-adapter")

	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		slog.Error("failed to create event bus", slog.Any("error", err))
		os.Exit(1)
	}

	adapter := &GitHubActionsAdapter{
		token:         os.Getenv("GITHUB_TOKEN"),
		webhookSecret: os.Getenv("GITHUB_ACTIONS_WEBHOOK_SECRET"),
		bus:           bus,
		httpClient:    &http.Client{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/webhook", adapter.HandleWebhook)
	mux.HandleFunc("/api/v1/runs", adapter.HandleRuns)

	slog.Info("GitHub Actions adapter listening", slog.String("addr", ":19114"))
	http.ListenAndServe(":19114", logger.HTTPMiddleware("githubactions-adapter", mux))
}

func (a *GitHubActionsAdapter) validateSignature(r *http.Request, body []byte) bool {
	if a.webhookSecret == "" {
		return true
	}
	sig := r.Header.Get("X-Hub-Signature-256")
	if sig == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(a.webhookSecret))
	mac.Write(body)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(sig), []byte(expected))
}

func (a *GitHubActionsAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if !a.validateSignature(r, body) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	switch r.Header.Get("X-GitHub-Event") {
	case "workflow_run":
		var payload workflowRunEvent
		if err := json.Unmarshal(body, &payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		a.handleWorkflowRun(r.Context(), payload)
	case "check_run":
		var payload checkRunEvent
		if err := json.Unmarshal(body, &payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		a.handleCheckRun(r.Context(), payload)
	}

	w.WriteHeader(http.StatusOK)
}

func (a *GitHubActionsAdapter) handleWorkflowRun(ctx context.Context, p workflowRunEvent) {
	if p.Action != "completed" {
		return
	}

	ep, _ := json.Marshal(map[string]interface{}{
		"run_id":     p.WorkflowRun.ID,
		"name":       p.WorkflowRun.Name,
		"conclusion": p.WorkflowRun.Conclusion,
		"url":        p.WorkflowRun.HTMLURL,
		"repo":       p.Repository.FullName,
		"branch":     p.WorkflowRun.HeadBranch,
		"sha":        p.WorkflowRun.HeadSHA,
		"source":     "github_actions",
	})

	var eventType events.EventType
	switch p.WorkflowRun.Conclusion {
	case "success":
		eventType = events.TaskCompleted
	case "failure", "cancelled", "timed_out":
		eventType = events.TaskFailed
	default:
		return
	}

	if err := a.bus.Publish(ctx, events.Event{Type: eventType, Payload: ep}); err != nil {
		slog.Error("failed to publish workflow run event",
			slog.String("event_type", string(eventType)),
			slog.Int64("run_id", p.WorkflowRun.ID),
			slog.Any("error", err))
	}
}

func (a *GitHubActionsAdapter) handleCheckRun(ctx context.Context, p checkRunEvent) {
	if p.Action != "completed" || p.CheckRun.Conclusion != "failure" {
		return
	}
	ep, _ := json.Marshal(map[string]interface{}{
		"check_run_id": p.CheckRun.ID,
		"name":         p.CheckRun.Name,
		"conclusion":   p.CheckRun.Conclusion,
		"url":          p.CheckRun.HTMLURL,
		"repo":         p.Repository.FullName,
		"source":       "github_actions",
	})
	if err := a.bus.Publish(ctx, events.Event{Type: events.TaskFailed, Payload: ep}); err != nil {
		slog.Error("failed to publish check run failed event",
			slog.Int64("check_run_id", p.CheckRun.ID),
			slog.Any("error", err))
	}
}

// HandleRuns lists or triggers GitHub Actions workflow runs.
//
//	GET  /api/v1/runs?repo=owner/repo[&workflow=<id-or-filename>]
//	POST /api/v1/runs  {"repo":"owner/repo","workflow":"ci.yml","ref":"main","inputs":{...}}
func (a *GitHubActionsAdapter) HandleRuns(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		repo := r.URL.Query().Get("repo")
		if repo == "" {
			http.Error(w, "repo query parameter is required", http.StatusBadRequest)
			return
		}
		path := fmt.Sprintf("/repos/%s/actions/runs", repo)
		if wf := r.URL.Query().Get("workflow"); wf != "" {
			path = fmt.Sprintf("/repos/%s/actions/workflows/%s/runs", repo, wf)
		}
		var result map[string]interface{}
		if err := a.ghRequest(r.Context(), http.MethodGet, path, nil, &result); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)

	case http.MethodPost:
		var req struct {
			Repo     string                 `json:"repo"`
			Workflow string                 `json:"workflow"`
			Ref      string                 `json:"ref"`
			Inputs   map[string]interface{} `json:"inputs"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Repo == "" || req.Workflow == "" || req.Ref == "" {
			http.Error(w, "repo, workflow, and ref are required", http.StatusBadRequest)
			return
		}
		path := fmt.Sprintf("/repos/%s/actions/workflows/%s/dispatches", req.Repo, req.Workflow)
		dispatchBody, _ := json.Marshal(map[string]interface{}{
			"ref":    req.Ref,
			"inputs": req.Inputs,
		})
		if err := a.ghRequest(r.Context(), http.MethodPost, path, dispatchBody, nil); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *GitHubActionsAdapter) ghRequest(ctx context.Context, method, path string, body []byte, out interface{}) error {
	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, githubAPIBase+path, reqBody)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("github API error %d: %s", resp.StatusCode, string(b))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
