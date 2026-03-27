package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/dotrage/forge-adp/pkg/events"
	gitlab "gitlab.com/gitlab-org/api/client-go"
)

type GitLabAdapter struct {
	client      *gitlab.Client
	bus         events.Bus
	secretToken string
}

func main() {
	client, err := gitlab.NewClient(
		os.Getenv("GITLAB_TOKEN"),
		gitlab.WithBaseURL(os.Getenv("GITLAB_BASE_URL")),
	)
	if err != nil {
		log.Fatalf("failed to create GitLab client: %v", err)
	}

	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		log.Fatalf("failed to create event bus: %v", err)
	}

	adapter := &GitLabAdapter{
		client:      client,
		bus:         bus,
		secretToken: os.Getenv("GITLAB_WEBHOOK_SECRET"),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/webhook", adapter.HandleWebhook)
	mux.HandleFunc("/api/v1/branches", adapter.HandleBranches)
	mux.HandleFunc("/api/v1/mergerequests", adapter.HandleMergeRequests)
	mux.HandleFunc("/api/v1/commits", adapter.HandleCommits)

	log.Printf("GitLab adapter listening on :19095")
	http.ListenAndServe(":19095", mux)
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
		a.bus.Publish(ctx, events.Event{
			Type:    events.ReviewRequested,
			Payload: payload,
		})
	case "merge":
		payload, _ := json.Marshal(map[string]interface{}{
			"mr_iid":     e.ObjectAttributes.IID,
			"project_id": e.Project.ID,
			"merged":     true,
		})
		a.bus.Publish(ctx, events.Event{
			Type:    events.TaskCompleted,
			Payload: payload,
		})
	}
}

func (a *GitLabAdapter) handleMRComment(ctx context.Context, e *gitlab.MergeCommentEvent) {
	// Handle review comments on merge requests.
}

func (a *GitLabAdapter) handlePipeline(ctx context.Context, e *gitlab.PipelineEvent) {
	switch e.ObjectAttributes.Status {
	case "success":
		payload, _ := json.Marshal(map[string]interface{}{
			"pipeline_id": e.ObjectAttributes.ID,
			"project_id":  e.Project.ID,
			"status":      "success",
		})
		a.bus.Publish(ctx, events.Event{
			Type:    events.DeploymentApproved,
			Payload: payload,
		})
	case "failed":
		payload, _ := json.Marshal(map[string]interface{}{
			"pipeline_id": e.ObjectAttributes.ID,
			"project_id":  e.Project.ID,
			"status":      "failed",
		})
		a.bus.Publish(ctx, events.Event{
			Type:    events.TaskFailed,
			Payload: payload,
		})
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
	json.NewEncoder(w).Encode(mr)
}

func (a *GitLabAdapter) HandleCommits(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
