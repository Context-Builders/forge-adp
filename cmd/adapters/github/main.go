package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/google/go-github/v58/github"
	"github.com/dotrage/forge-adp/pkg/events"
	"golang.org/x/oauth2"
)

type GitHubAdapter struct {
	client *github.Client
	bus    events.Bus
}

func main() {
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: os.Getenv("GITHUB_TOKEN")},
	)
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	bus, _ := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")

	adapter := &GitHubAdapter{client: client, bus: bus}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/webhook", adapter.HandleWebhook)
	mux.HandleFunc("/api/v1/branches", adapter.HandleBranches)
	mux.HandleFunc("/api/v1/pulls", adapter.HandlePullRequests)
	mux.HandleFunc("/api/v1/commits", adapter.HandleCommits)

	log.Printf("GitHub adapter listening on :19091")
	http.ListenAndServe(":19091", mux)
}

func (a *GitHubAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	payload, err := github.ValidatePayload(r, []byte(os.Getenv("GITHUB_WEBHOOK_SECRET")))
	if err != nil {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	event, err := github.ParseWebHook(github.WebHookType(r), payload)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	switch e := event.(type) {
	case *github.PullRequestEvent:
		a.handlePullRequest(r.Context(), e)
	case *github.PullRequestReviewEvent:
		a.handleReview(r.Context(), e)
	case *github.CheckSuiteEvent:
		a.handleCheckSuite(r.Context(), e)
	}

	w.WriteHeader(http.StatusOK)
}

func (a *GitHubAdapter) handlePullRequest(ctx context.Context, e *github.PullRequestEvent) {
	if !strings.HasPrefix(e.PullRequest.Head.GetRef(), "forge/") {
		return
	}

	switch e.GetAction() {
	case "opened":
		payload, _ := json.Marshal(map[string]interface{}{
			"pr_number": e.PullRequest.GetNumber(),
			"repo":      e.Repo.GetFullName(),
			"branch":    e.PullRequest.Head.GetRef(),
		})
		a.bus.Publish(ctx, events.Event{
			Type:    events.ReviewRequested,
			Payload: payload,
		})
	case "closed":
		if e.PullRequest.GetMerged() {
			payload, _ := json.Marshal(map[string]interface{}{
				"pr_number": e.PullRequest.GetNumber(),
				"merged":    true,
			})
			a.bus.Publish(ctx, events.Event{
				Type:    events.TaskCompleted,
				Payload: payload,
			})
		}
	}
}

func (a *GitHubAdapter) handleReview(ctx context.Context, e *github.PullRequestReviewEvent) {
	state := e.Review.GetState()
	payload, _ := json.Marshal(map[string]interface{}{
		"pr_number": e.PullRequest.GetNumber(),
		"reviewer":  e.Review.User.GetLogin(),
		"state":     state,
	})

	switch state {
	case "approved":
		a.bus.Publish(ctx, events.Event{Type: events.ReviewApproved, Payload: payload})
	case "changes_requested":
		a.bus.Publish(ctx, events.Event{Type: events.ReviewRejected, Payload: payload})
	}
}

func (a *GitHubAdapter) handleCheckSuite(ctx context.Context, e *github.CheckSuiteEvent) {
	// Handle CI results
}

func (a *GitHubAdapter) HandleBranches(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Owner  string `json:"owner"`
		Repo   string `json:"repo"`
		Branch string `json:"branch"`
		Base   string `json:"base"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	baseRef, _, err := a.client.Git.GetRef(ctx, req.Owner, req.Repo, "refs/heads/"+req.Base)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	newRef := &github.Reference{
		Ref:    github.String("refs/heads/" + req.Branch),
		Object: &github.GitObject{SHA: baseRef.Object.SHA},
	}
	ref, _, err := a.client.Git.CreateRef(ctx, req.Owner, req.Repo, newRef)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(ref)
}

func (a *GitHubAdapter) HandlePullRequests(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Owner string `json:"owner"`
		Repo  string `json:"repo"`
		Title string `json:"title"`
		Body  string `json:"body"`
		Head  string `json:"head"`
		Base  string `json:"base"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	pr, _, err := a.client.PullRequests.Create(ctx, req.Owner, req.Repo, &github.NewPullRequest{
		Title: github.String(req.Title),
		Body:  github.String(req.Body),
		Head:  github.String(req.Head),
		Base:  github.String(req.Base),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(pr)
}

func (a *GitHubAdapter) HandleCommits(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
