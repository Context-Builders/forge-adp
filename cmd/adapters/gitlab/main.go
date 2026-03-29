package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/dotrage/forge-adp/pkg/events"
	"github.com/dotrage/forge-adp/pkg/logger"
	gitlab "gitlab.com/gitlab-org/api/client-go"
)

type GitLabAdapter struct {
	client          *gitlab.Client
	bus             events.Bus
	secretToken     string
	orchestratorURL string
}

func main() {
	logger.Init("gitlab-adapter")

	client, err := gitlab.NewClient(
		os.Getenv("GITLAB_TOKEN"),
		gitlab.WithBaseURL(os.Getenv("GITLAB_BASE_URL")),
	)
	if err != nil {
		slog.Error("failed to create GitLab client", slog.Any("error", err))
		os.Exit(1)
	}

	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		slog.Error("failed to create event bus", slog.Any("error", err))
		os.Exit(1)
	}

	adapter := &GitLabAdapter{
		client:          client,
		bus:             bus,
		secretToken:     os.Getenv("GITLAB_WEBHOOK_SECRET"),
		orchestratorURL: os.Getenv("ORCHESTRATOR_URL"),
	}
	if adapter.orchestratorURL == "" {
		adapter.orchestratorURL = "http://localhost:19080"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/webhook", adapter.HandleWebhook)
	mux.HandleFunc("/api/v1/branches", adapter.HandleBranches)
	mux.HandleFunc("/api/v1/mergerequests", adapter.HandleMergeRequests)
	mux.HandleFunc("/api/v1/commits", adapter.HandleCommits)
	mux.HandleFunc("/api/v1/pipelines", adapter.HandlePipelines)

	slog.Info("GitLab adapter listening", slog.String("addr", ":19095"))
	http.ListenAndServe(":19095", logger.HTTPMiddleware("gitlab-adapter", mux))
}

func (a *GitLabAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if a.secretToken != "" && r.Header.Get("X-Gitlab-Token") != a.secretToken {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	eventType := gitlab.EventType(r.Header.Get("X-Gitlab-Event"))
	payload, err := gitlab.ParseWebhook(eventType, body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	switch e := payload.(type) {
	case *gitlab.MergeEvent:
		a.handleMergeRequest(r.Context(), e)
	case *gitlab.MergeCommentEvent:
		a.handleMRComment(r.Context(), e)
	case *gitlab.PipelineEvent:
		a.handlePipeline(r.Context(), e)
	}

	w.WriteHeader(http.StatusOK)
}

func (a *GitLabAdapter) handleMergeRequest(ctx context.Context, e *gitlab.MergeEvent) {
	if !strings.HasPrefix(e.ObjectAttributes.SourceBranch, "forge/") {
		return
	}

	switch e.ObjectAttributes.Action {
	case "open":
		payload, _ := json.Marshal(map[string]interface{}{
			"mr_iid":        e.ObjectAttributes.IID,
			"project_id":    e.Project.ID,
			"source_branch": e.ObjectAttributes.SourceBranch,
			"target_branch": e.ObjectAttributes.TargetBranch,
			"url":           e.ObjectAttributes.URL,
		})
		if err := a.bus.Publish(ctx, events.Event{Type: events.ReviewRequested, Payload: payload}); err != nil {
			slog.Error("failed to publish review requested event", slog.Any("error", err))
		}
	case "merge":
		payload, _ := json.Marshal(map[string]interface{}{
			"mr_iid":     e.ObjectAttributes.IID,
			"project_id": e.Project.ID,
			"merged":     true,
		})
		if err := a.bus.Publish(ctx, events.Event{Type: events.TaskCompleted, Payload: payload}); err != nil {
			slog.Error("failed to publish task completed event", slog.Any("error", err))
		}
	}
}

// handleMRComment parses !forge commands from MR note bodies and calls the
// orchestrator to approve or reject the referenced task.
//
//	!forge approve <task_id>
//	!forge reject <task_id> [reason...]
func (a *GitLabAdapter) handleMRComment(ctx context.Context, e *gitlab.MergeCommentEvent) {
	body := strings.TrimSpace(e.ObjectAttributes.Note)
	if !strings.HasPrefix(body, "!forge ") {
		return
	}

	parts := strings.Fields(body) // ["!forge", "approve"|"reject", "<task_id>", ...]
	if len(parts) < 3 {
		slog.Warn("malformed forge command in MR comment",
			slog.Int64("mr_iid", e.MergeRequest.IID),
			slog.String("body", body))
		return
	}
	cmd := strings.ToLower(parts[1])
	taskID := parts[2]

	switch cmd {
	case "approve":
		url := fmt.Sprintf("%s/api/v1/tasks/%s/approve", a.orchestratorURL, taskID)
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			slog.Error("failed to approve task", slog.String("task_id", taskID), slog.Any("error", err))
			return
		}
		defer resp.Body.Close()
		slog.Info("approved task via GitLab MR comment",
			slog.String("task_id", taskID),
			slog.Int64("mr_iid", e.MergeRequest.IID),
			slog.Int("status", resp.StatusCode))

	case "reject":
		reason := strings.Join(parts[3:], " ")
		if reason == "" {
			reason = fmt.Sprintf("rejected via GitLab MR !%d comment", e.MergeRequest.IID)
		}
		reqBody, _ := json.Marshal(map[string]string{"reason": reason})
		url := fmt.Sprintf("%s/api/v1/tasks/%s/reject", a.orchestratorURL, taskID)
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			slog.Error("failed to reject task", slog.String("task_id", taskID), slog.Any("error", err))
			return
		}
		defer resp.Body.Close()
		slog.Info("rejected task via GitLab MR comment",
			slog.String("task_id", taskID),
			slog.Int64("mr_iid", e.MergeRequest.IID),
			slog.Int("status", resp.StatusCode))

	default:
		slog.Warn("unknown forge command in MR comment",
			slog.String("cmd", cmd),
			slog.Int64("mr_iid", e.MergeRequest.IID))
	}
}

func (a *GitLabAdapter) handlePipeline(ctx context.Context, e *gitlab.PipelineEvent) {
	switch e.ObjectAttributes.Status {
	case "success":
		payload, _ := json.Marshal(map[string]interface{}{
			"pipeline_id": e.ObjectAttributes.ID,
			"project_id":  e.Project.ID,
			"status":      "success",
			"source":      "gitlab",
		})
		if err := a.bus.Publish(ctx, events.Event{Type: events.DeploymentApproved, Payload: payload}); err != nil {
			slog.Error("failed to publish deployment approved event", slog.Any("error", err))
		}
	case "failed":
		payload, _ := json.Marshal(map[string]interface{}{
			"pipeline_id": e.ObjectAttributes.ID,
			"project_id":  e.Project.ID,
			"status":      "failed",
			"source":      "gitlab",
		})
		if err := a.bus.Publish(ctx, events.Event{Type: events.TaskFailed, Payload: payload}); err != nil {
			slog.Error("failed to publish task failed event", slog.Any("error", err))
		}
	}
}

func (a *GitLabAdapter) HandleBranches(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ProjectID string `json:"project_id"`
		Branch    string `json:"branch"`
		Ref       string `json:"ref"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	branch, _, err := a.client.Branches.CreateBranch(req.ProjectID, &gitlab.CreateBranchOptions{
		Branch: gitlab.Ptr(req.Branch),
		Ref:    gitlab.Ptr(req.Ref),
	}, gitlab.WithContext(ctx))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(branch)
}

func (a *GitLabAdapter) HandleMergeRequests(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ProjectID    string `json:"project_id"`
		Title        string `json:"title"`
		Description  string `json:"description"`
		SourceBranch string `json:"source_branch"`
		TargetBranch string `json:"target_branch"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	mr, _, err := a.client.MergeRequests.CreateMergeRequest(req.ProjectID, &gitlab.CreateMergeRequestOptions{
		Title:        gitlab.Ptr(req.Title),
		Description:  gitlab.Ptr(req.Description),
		SourceBranch: gitlab.Ptr(req.SourceBranch),
		TargetBranch: gitlab.Ptr(req.TargetBranch),
	}, gitlab.WithContext(ctx))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(mr)
}

// HandleCommits lists commits for a project/branch or fetches a single commit by SHA.
//
//	GET /api/v1/commits?project_id=<id>&ref=<branch>&per_page=20
//	GET /api/v1/commits?project_id=<id>&sha=<commit-sha>
func (a *GitLabAdapter) HandleCommits(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	projectID := r.URL.Query().Get("project_id")
	if projectID == "" {
		http.Error(w, "project_id query parameter is required", http.StatusBadRequest)
		return
	}

	// Single commit by SHA
	if sha := r.URL.Query().Get("sha"); sha != "" {
		commit, _, err := a.client.Commits.GetCommit(projectID, sha, nil, gitlab.WithContext(ctx))
		if err != nil {
			http.Error(w, fmt.Sprintf("get commit: %v", err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(commit)
		return
	}

	// List commits with optional ref/branch filter
	opts := &gitlab.ListCommitsOptions{
		ListOptions: gitlab.ListOptions{PerPage: 20},
	}
	if ref := r.URL.Query().Get("ref"); ref != "" {
		opts.RefName = gitlab.Ptr(ref)
	}
	commits, _, err := a.client.Commits.ListCommits(projectID, opts, gitlab.WithContext(ctx))
	if err != nil {
		http.Error(w, fmt.Sprintf("list commits: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(commits)
}

// HandlePipelines triggers or lists CI pipelines for a project.
//
//	GET  /api/v1/pipelines?project_id=<id>[&ref=<branch>]
//	POST /api/v1/pipelines  {"project_id":"<id>","ref":"main","variables":[{"key":"VAR","value":"val"}]}
func (a *GitLabAdapter) HandlePipelines(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	switch r.Method {
	case http.MethodGet:
		projectID := r.URL.Query().Get("project_id")
		if projectID == "" {
			http.Error(w, "project_id query parameter is required", http.StatusBadRequest)
			return
		}
		opts := &gitlab.ListProjectPipelinesOptions{ListOptions: gitlab.ListOptions{PerPage: 20}}
		if ref := r.URL.Query().Get("ref"); ref != "" {
			opts.Ref = gitlab.Ptr(ref)
		}
		pipelines, _, err := a.client.Pipelines.ListProjectPipelines(projectID, opts, gitlab.WithContext(ctx))
		if err != nil {
			http.Error(w, fmt.Sprintf("list pipelines: %v", err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(pipelines)

	case http.MethodPost:
		var req struct {
			ProjectID string `json:"project_id"`
			Ref       string `json:"ref"`
			Variables []struct {
				Key   string `json:"key"`
				Value string `json:"value"`
			} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.ProjectID == "" || req.Ref == "" {
			http.Error(w, "project_id and ref are required", http.StatusBadRequest)
			return
		}
		vars := make([]*gitlab.PipelineVariableOptions, len(req.Variables))
		for i, v := range req.Variables {
			vars[i] = &gitlab.PipelineVariableOptions{
				Key:   gitlab.Ptr(v.Key),
				Value: gitlab.Ptr(v.Value),
			}
		}
		pipeline, _, err := a.client.Pipelines.CreatePipeline(req.ProjectID, &gitlab.CreatePipelineOptions{
			Ref:       gitlab.Ptr(req.Ref),
			Variables: &vars,
		}, gitlab.WithContext(ctx))
		if err != nil {
			http.Error(w, fmt.Sprintf("create pipeline: %v", err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(pipeline)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
