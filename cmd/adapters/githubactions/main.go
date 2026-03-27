package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"github.com/dotrage/forge-adp/pkg/events"
)

const githubAPIBase = "https://api.github.com"// Forge events.// endpoint, distinguishes the source by header, and publishes the appropriate// pipeline events into Forge's message bus. It listens on a single webhook// githubactions adapter bridges GitHub Actions workflow run events and GitLab CI

const gitlabAPIBase = "https://gitlab.com/api/v4"

type CICDAdapter struct {
	githubToken         string
	githubWebhookSecret string
	gitlabToken         string
	gitlabWebhookSecret string
	bus                 events.Bus
	httpClient          *http.Client
}

type workflowRunEvent struct {
	Action      string `json:"action"`
	WorkflowRun struct {
		ID         int64  `json:"id"`
		Name       string `json:"name"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		HTMLURL    string `json:"html_url"`
	} `json:"workflow_run"`
Repository struct {
	FullName string `json:"full_name"`
} `json:"repository"`
}

type gitlabPipelineEvent struct {
	ObjectKind     string `json:"object_kind"`
	ObjectAttributes struct {
		ID     int    `json:"id"`
		Status string `json:"status"`
		URL    string `json:"url"`
	} `json:"object_attributes"`
Project struct {
	PathWithNamespace string `json:"path_with_namespace"`
} `json:"project"`
}

func main() {
	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		log.Fatalf("failed to create event bus: %v", err)
	}
adapter := &CICDAdapter{
	githubToken:         os.Getenv("GITHUB_TOKEN"),
	githubWebhookSecret: os.Getenv("GITHUB_WEBHOOK_SECRET"),
	gitlabToken:         os.Getenv("GITLAB_TOKEN"),
	gitlabWebhookSecret: os.Getenv("GITLAB_WEBHOOK_SECRET"),
	bus:                 bus,
	httpClient:          &http.Client{},
}
mux := http.NewServeMux()
mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})
mux.HandleFunc("/webhook/github", adapter.HandleGitHubWebhook)
mux.HandleFunc("/webhook/gitlab", adapter.HandleGitLabWebhook)
mux.HandleFunc("/api/v1/runs", adapter.HandleRuns)
log.Printf("GitHub Actions / GitLab CI adapter listening on :19114")
http.ListenAndServe(":19114", mux)
}

func (a *CICDAdapter) HandleGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

var payload workflowRunEvent
if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}
if r.Header.Get("X-Github-Event") == "workflow_run" {
	if payload.Action == "completed" {
		switch payload.WorkflowRun.Conclusion {
			case "success":
			a.handleGHRunSuccess(r.Context(), payload)
			case "failure", "cancelled", "timed_out":
			a.handleGHRunFailed(r.Context(), payload)
		}
}
}
w.WriteHeader(http.StatusOK)
}

func (a *CICDAdapter) HandleGitLabWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
if a.gitlabWebhookSecret != "" {
	if r.Header.Get("X-Gitlab-Token") != a.gitlabWebhookSecret {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}
}

var payload gitlabPipelineEvent
if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}
if payload.ObjectKind == "pipeline" {
	switch payload.ObjectAttributes.Status {
		case "success":
		a.handleGLPipelineSuccess(r.Context(), payload)
		case "failed", "canceled":
		a.handleGLPipelineFailed(r.Context(), payload)
	}
}
w.WriteHeader(http.StatusOK)
}

func (a *CICDAdapter) handleGHRunSuccess(ctx context.Context, p workflowRunEvent) {
	ep, _ := json.Marshal(map[string]interface{}{
		"run_id":  p.WorkflowRun.ID,
		"name":    p.WorkflowRun.Name,
		"url":     p.WorkflowRun.HTMLURL,
		"repo":    p.Repository.FullName,
		"source":  "github_actions",
})
if err := a.bus.Publish(ctx, events.Event{Type: events.TaskCompleted, Payload: ep}); err != nil {
	log.Printf("failed to publish task completed event: %v", err)
}
}

func (a *CICDAdapter) handleGHRunFailed(ctx context.Context, p workflowRunEvent) {
	ep, _ := json.Marshal(map[string]interface{}{
		"run_id":     p.WorkflowRun.ID,
		"name":       p.WorkflowRun.Name,
		"conclusion": p.WorkflowRun.Conclusion,
		"url":        p.WorkflowRun.HTMLURL,
		"repo":       p.Repository.FullName,
		"source":     "github_actions",
})
if err := a.bus.Publish(ctx, events.Event{Type: events.TaskFailed, Payload: ep}); err != nil {
	log.Printf("failed to publish task failed event: %v", err)
}
}

func (a *CICDAdapter) handleGLPipelineSuccess(ctx context.Context, p gitlabPipelineEvent) {
	ep, _ := json.Marshal(map[string]interface{}{
		"pipeline_id": p.ObjectAttributes.ID,
		"url":         p.ObjectAttributes.URL,
		"project":     p.Project.PathWithNamespace,
		"source":      "gitlab_ci",
})
if err := a.bus.Publish(ctx, events.Event{Type: events.TaskCompleted, Payload: ep}); err != nil {
	log.Printf("failed to publish task completed event: %v", err)
}
}

func (a *CICDAdapter) handleGLPipelineFailed(ctx context.Context, p gitlabPipelineEvent) {
	ep, _ := json.Marshal(map[string]interface{}{
		"pipeline_id": p.ObjectAttributes.ID,
		"status":      p.ObjectAttributes.Status,
		"project":     p.Project.PathWithNamespace,
		"source":      "gitlab_ci",
})
if err := a.bus.Publish(ctx, events.Event{Type: events.TaskFailed, Payload: ep}); err != nil {
	log.Printf("failed to publish task failed event: %v", err)
}
}

func (a *CICDAdapter) HandleRuns(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
		case http.MethodGet:
		provider := r.URL.Query().Get("provider")
		repo := r.URL.Query().Get("repo")
		if provider == "" || repo == "" {
			http.Error(w, "provider and repo query parameters are required", http.StatusBadRequest)
			return
		}

var result map[string]interface{}

var reqErr error
switch provider {
	case "github":
	path := fmt.Sprintf("/repos/%s/actions/runs", repo)
	reqErr = a.ghRequest(r.Context(), path, &result)
	case "gitlab":
	path := fmt.Sprintf("/projects/%s/pipelines", strings.ReplaceAll(repo, "/", "%2F"))
	reqErr = a.glRequest(r.Context(), path, &result)
	default:
	http.Error(w, "unsupported provider", http.StatusBadRequest)
	return
}
if reqErr != nil {
	http.Error(w, reqErr.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
default:
http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
}

func (a *CICDAdapter) ghRequest(ctx context.Context, path string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubAPIBase+path, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
if a.githubToken != "" {
	req.Header.Set("Authorization", "Bearer "+a.githubToken)
}
req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
resp, err := a.httpClient.Do(req)
if err != nil {
	return fmt.Errorf("execute request: %w", err)
}
defer resp.Body.Close()
if resp.StatusCode >= 300 {
	b, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("github API error %d: %s", resp.StatusCode, string(b))
}
return json.NewDecoder(resp.Body).Decode(out)
}

func (a *CICDAdapter) glRequest(ctx context.Context, path string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, gitlabAPIBase+path, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
if a.gitlabToken != "" {
	req.Header.Set("PRIVATE-TOKEN", a.gitlabToken)
}
resp, err := a.httpClient.Do(req)
if err != nil {
	return fmt.Errorf("execute request: %w", err)
}
defer resp.Body.Close()
if resp.StatusCode >= 300 {
	b, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("gitlab API error %d: %s", resp.StatusCode, string(b))
}
return json.NewDecoder(resp.Body).Decode(out)
}
