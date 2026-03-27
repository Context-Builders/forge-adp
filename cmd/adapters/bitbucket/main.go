package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/dotrage/forge-adp/pkg/events"
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
	ID     int    `json:"id"`
	Title  string `json:"title"`
	State  string `json:"state"`
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
	username := os.Getenv("BITBUCKET_USERNAME")
	appPassword := os.Getenv("BITBUCKET_APP_PASSWORD")
	if username == "" || appPassword == "" {
		log.Fatal("BITBUCKET_USERNAME and BITBUCKET_APP_PASSWORD are required")
	}

	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		log.Fatalf("failed to create event bus: %v", err)
	}

	adapter := &BitbucketAdapter{
		username:      username,
		appPassword:   appPassword,
		webhookSecret: os.Getenv("BITBUCKET_WEBHOOK_SECRET"),
		bus:           bus,
		httpClient:    &http.Client{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/webhook", adapter.HandleWebhook)
	mux.HandleFunc("/api/v1/pulls", adapter.HandlePullRequests)
	mux.HandleFunc("/api/v1/branches", adapter.HandleBranches)

	log.Printf("Bitbucket adapter listening on :19109")
	http.ListenAndServe(":19109", mux)
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
		log.Printf("failed to publish review requested event: %v", err)
	}
}

func (a *BitbucketAdapter) handlePRMerged(ctx context.Context, p bitbucketWebhookPayload) {
	ep, _ := json.Marshal(map[string]interface{}{
		"pr_number": p.PullRequest.ID,
		"repo":      p.Repository.FullName,
		"source":    "bitbucket",
	})
	if err := a.bus.Publish(ctx, events.Event{Type: events.TaskCompleted, Payload: ep}); err != nil {
		log.Printf("failed to publish task completed event: %v", err)
	}
}

func (a *BitbucketAdapter) handlePRDeclined(ctx context.Context, p bitbucketWebhookPayload) {
	ep, _ := json.Marshal(map[string]interface{}{
		"pr_number": p.PullRequest.ID,
		"repo":      p.Repository.FullName,
		"source":    "bitbucket",
	})
	if err := a.bus.Publish(ctx, events.Event{Type: events.ReviewRejected, Payload: ep}); err != nil {
		log.Printf("failed to publish review rejected event: %v", err)
	}
}

func (a *BitbucketAdapter) HandlePullRequests(w http.ResponseWriter, r *http.Request) {
	workspace := r.URL.Query().Get("workspace")
	repo := r.URL.Query().Get("repo")
	if workspace == "" || repo == "" {
		http.Error(w, "workspace and repo query parameters are required", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		var result map[string]interface{}
		path := fmt.Sprintf("/repositories/%s/%s/pullrequests", workspace, repo)
		if err := a.bbRequest(r.Context(), http.MethodGet, path, nil, &result); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *BitbucketAdapter) HandleBranches(w http.ResponseWriter, r *http.Request) {
	workspace := r.URL.Query().Get("workspace")
	repo := r.URL.Query().Get("repo")
	if workspace == "" || repo == "" {
		http.Error(w, "workspace and repo query parameters are required", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		var result map[string]interface{}
		path := fmt.Sprintf("/repositories/%s/%s/refs/branches", workspace, repo)
		if err := a.bbRequest(r.Context(), http.MethodGet, path, nil, &result); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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
