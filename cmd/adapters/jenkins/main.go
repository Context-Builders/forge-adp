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

const jenkinsAPIBase = "/api/json"

type JenkinsAdapter struct {
	baseURL       string
	user          string
	apiToken      string
	webhookSecret string
	bus           events.Bus
	httpClient    *http.Client
}

type jenkinsBuildEvent struct {
	Name  string `json:"name"`
	Build struct {
		Number  int    `json:"number"`
		Phase   string `json:"phase"`
		Status  string `json:"status"`
		URL     string `json:"url"`
		FullURL string `json:"full_url"`
	} `json:"build"`
}

func main() {
	logger.Init("jenkins-adapter")

	baseURL := os.Getenv("JENKINS_URL")
	user := os.Getenv("JENKINS_USER")
	apiToken := os.Getenv("JENKINS_API_TOKEN")
	if baseURL == "" || user == "" || apiToken == "" {
		slog.Error("JENKINS_URL, JENKINS_USER, and JENKINS_API_TOKEN are required")
		os.Exit(1)
	}

	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		slog.Error("failed to create event bus", slog.Any("error", err))
		os.Exit(1)
	}

	adapter := &JenkinsAdapter{
		baseURL:       strings.TrimRight(baseURL, "/"),
		user:          user,
		apiToken:      apiToken,
		webhookSecret: os.Getenv("JENKINS_WEBHOOK_SECRET"),
		bus:           bus,
		httpClient:    &http.Client{},
	}

	go adapter.subscribeToEvents()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/webhook", adapter.HandleWebhook)
	mux.HandleFunc("/api/v1/builds", adapter.HandleBuilds)
	mux.HandleFunc("/api/v1/jobs", adapter.HandleJobs)

	slog.Info("Jenkins adapter listening", slog.String("addr", ":19111"))
	http.ListenAndServe(":19111", logger.HTTPMiddleware("jenkins-adapter", mux))
}

func (a *JenkinsAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
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
		sig := r.Header.Get("X-Jenkins-Signature")
		mac := hmac.New(sha256.New, []byte(a.webhookSecret))
		mac.Write(body)
		expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(expected), []byte(sig)) {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
	}

	var payload jenkinsBuildEvent
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	switch payload.Build.Phase {
	case "COMPLETED":
		switch payload.Build.Status {
		case "SUCCESS":
			a.handleBuildSuccess(r.Context(), payload)
		case "FAILURE", "ABORTED", "UNSTABLE":
			a.handleBuildFailed(r.Context(), payload)
		}
	case "STARTED":
		a.handleBuildStarted(r.Context(), payload)
	}
	w.WriteHeader(http.StatusOK)
}

func (a *JenkinsAdapter) handleBuildStarted(ctx context.Context, p jenkinsBuildEvent) {
	ep, _ := json.Marshal(map[string]interface{}{
		"job":    p.Name,
		"build":  p.Build.Number,
		"url":    p.Build.FullURL,
		"source": "jenkins",
	})
	if err := a.bus.Publish(ctx, events.Event{Type: events.TaskStarted, Payload: ep}); err != nil {
		slog.Error("failed to publish task started event",
			slog.String("job", p.Name),
			slog.Any("error", err))
	}
}

func (a *JenkinsAdapter) handleBuildSuccess(ctx context.Context, p jenkinsBuildEvent) {
	ep, _ := json.Marshal(map[string]interface{}{
		"job":    p.Name,
		"build":  p.Build.Number,
		"url":    p.Build.FullURL,
		"source": "jenkins",
	})
	if err := a.bus.Publish(ctx, events.Event{Type: events.TaskCompleted, Payload: ep}); err != nil {
		slog.Error("failed to publish task completed event",
			slog.String("job", p.Name),
			slog.Any("error", err))
	}
}

func (a *JenkinsAdapter) handleBuildFailed(ctx context.Context, p jenkinsBuildEvent) {
	ep, _ := json.Marshal(map[string]interface{}{
		"job":    p.Name,
		"build":  p.Build.Number,
		"status": p.Build.Status,
		"url":    p.Build.FullURL,
		"source": "jenkins",
	})
	if err := a.bus.Publish(ctx, events.Event{Type: events.TaskFailed, Payload: ep}); err != nil {
		slog.Error("failed to publish task failed event",
			slog.String("job", p.Name),
			slog.Any("error", err))
	}
}

// HandleBuilds lists recent builds or triggers a new build for a job.
//
//	GET  /api/v1/builds?job=<name>
//	POST /api/v1/builds?job=<name>
func (a *JenkinsAdapter) HandleBuilds(w http.ResponseWriter, r *http.Request) {
	job := r.URL.Query().Get("job")
	if job == "" {
		http.Error(w, "job query parameter is required", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		var result map[string]interface{}
		path := fmt.Sprintf("/job/%s%s", job, jenkinsAPIBase)
		if err := a.jenkinsRequest(r.Context(), http.MethodGet, path, nil, &result); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	case http.MethodPost:
		path := fmt.Sprintf("/job/%s/build", job)
		if err := a.jenkinsRequest(r.Context(), http.MethodPost, path, nil, nil); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// HandleJobs lists all Jenkins jobs.
//
//	GET /api/v1/jobs
func (a *JenkinsAdapter) HandleJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var result map[string]interface{}
	if err := a.jenkinsRequest(r.Context(), http.MethodGet, jenkinsAPIBase, nil, &result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// subscribeToEvents listens for DeploymentRequested events and triggers the
// configured Jenkins job if jenkins_job is present in the payload.
func (a *JenkinsAdapter) subscribeToEvents() {
	ctx := context.Background()
	if err := a.bus.Subscribe(ctx, []events.EventType{events.DeploymentRequested}, func(e events.Event) error {
		var payload struct {
			Job string `json:"jenkins_job"`
		}
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			return fmt.Errorf("unmarshal deployment requested payload: %w", err)
		}
		if payload.Job == "" {
			return nil
		}

		slog.Info("triggering Jenkins build for deployment",
			slog.String("job", payload.Job),
			slog.String("task_id", e.TaskID))

		path := fmt.Sprintf("/job/%s/build", payload.Job)
		if err := a.jenkinsRequest(ctx, http.MethodPost, path, nil, nil); err != nil {
			slog.Warn("failed to trigger Jenkins build",
				slog.String("job", payload.Job),
				slog.Any("error", err))
		}
		return nil
	}); err != nil {
		slog.Error("failed to subscribe to deployment requested events", slog.Any("error", err))
	}
}

func (a *JenkinsAdapter) jenkinsRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.baseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.SetBasicAuth(a.user, a.apiToken)
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
		return fmt.Errorf("jenkins API error %d: %s", resp.StatusCode, string(b))
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
