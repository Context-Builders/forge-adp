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

const snykAPIBase = "https://api.snyk.io/v1"

// SnykAdapter bridges Snyk vulnerability events with the Forge event bus.
type SnykAdapter struct {
	apiToken      string
	orgID         string
	webhookSecret string
	httpClient    *http.Client
	bus           events.Bus
}

type snykWebhookPayload struct {
	Project struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"project"`
	Vulnerabilities []struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Severity string `json:"severity"`
		CVSSv3   string `json:"CVSSv3"`
	} `json:"vulnerabilities,omitempty"`
	NewIssues []struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Severity string `json:"severity"`
	} `json:"newIssues,omitempty"`
}

type snykProject struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

type snykProjectsResponse struct {
	Projects []snykProject `json:"projects"`
}

type snykIssuesResponse struct {
	Results []struct {
		Issues struct {
			Vulnerabilities []struct {
				ID       string `json:"id"`
				Title    string `json:"title"`
				Severity string `json:"severity"`
			} `json:"vulnerabilities"`
		} `json:"issues"`
	} `json:"results"`
}

func main() {
	logger.Init("snyk-adapter")

	apiToken := os.Getenv("SNYK_API_TOKEN")
	if apiToken == "" {
		slog.Error("SNYK_API_TOKEN is required")
		os.Exit(1)
	}

	orgID := os.Getenv("SNYK_ORG_ID")
	if orgID == "" {
		slog.Error("SNYK_ORG_ID is required")
		os.Exit(1)
	}

	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		slog.Error("failed to create event bus", slog.Any("error", err))
		os.Exit(1)
	}

	adapter := &SnykAdapter{
		apiToken:      apiToken,
		orgID:         orgID,
		webhookSecret: os.Getenv("SNYK_WEBHOOK_SECRET"),
		httpClient:    &http.Client{},
		bus:           bus,
	}

	go adapter.subscribeToEvents()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/webhook", adapter.HandleWebhook)
	mux.HandleFunc("/api/v1/vulnerabilities", adapter.HandleVulnerabilities)
	mux.HandleFunc("/api/v1/projects", adapter.HandleProjects)

	slog.Info("Snyk adapter listening", slog.String("addr", ":19102"))
	http.ListenAndServe(":19102", logger.HTTPMiddleware("snyk-adapter", mux))
}

func (a *SnykAdapter) verifySignature(r *http.Request, body []byte) bool {
	if a.webhookSecret == "" {
		return true
	}
	sig := r.Header.Get("X-Snyk-Signature")
	if sig == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(a.webhookSecret))
	mac.Write(body)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(sig), []byte(expected))
}

func (a *SnykAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if !a.verifySignature(r, body) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	var payload snykWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var criticalIssues []string
	for _, issue := range payload.NewIssues {
		if issue.Severity == "critical" || issue.Severity == "high" {
			criticalIssues = append(criticalIssues, fmt.Sprintf("%s (%s)", issue.Title, issue.Severity))
		}
	}

	if len(criticalIssues) > 0 {
		ep, _ := json.Marshal(map[string]interface{}{
			"source":      "snyk",
			"project_id":  payload.Project.ID,
			"project":     payload.Project.Name,
			"issues":      criticalIssues,
			"issue_count": len(criticalIssues),
			"reason":      fmt.Sprintf("Snyk detected %d new critical/high vulnerabilities in %s", len(criticalIssues), payload.Project.Name),
		})
		if err := a.bus.Publish(r.Context(), events.Event{Type: events.EscalationCreated, Payload: ep}); err != nil {
			slog.Error("failed to publish escalation event",
				slog.String("project_id", payload.Project.ID),
				slog.Any("error", err))
		}
	}
	w.WriteHeader(http.StatusOK)
}

// HandleVulnerabilities proxies vulnerability queries for a specific project to Snyk's API.
//
//	GET /api/v1/vulnerabilities?project_id=<id>
func (a *SnykAdapter) HandleVulnerabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	projectID := r.URL.Query().Get("project_id")
	if projectID == "" {
		http.Error(w, "project_id query parameter is required", http.StatusBadRequest)
		return
	}

	var result snykIssuesResponse
	if err := a.snykRequest(r.Context(), http.MethodPost,
		fmt.Sprintf("/org/%s/project/%s/aggregated-issues", a.orgID, projectID),
		map[string]interface{}{"includeDescription": false},
		&result,
	); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// HandleProjects lists Snyk projects for the configured organisation.
//
//	GET /api/v1/projects
func (a *SnykAdapter) HandleProjects(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var result snykProjectsResponse
	if err := a.snykRequest(r.Context(), http.MethodGet,
		fmt.Sprintf("/org/%s/projects", a.orgID),
		nil,
		&result,
	); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// subscribeToEvents listens for TaskCreated events and logs when a task is
// associated with a Snyk project so the security agent can trigger a scan.
func (a *SnykAdapter) subscribeToEvents() {
	ctx := context.Background()
	if err := a.bus.Subscribe(ctx, []events.EventType{events.TaskCreated}, func(e events.Event) error {
		var payload struct {
			SnykProjectID string `json:"snyk_project_id"`
		}
		json.Unmarshal(e.Payload, &payload)
		if payload.SnykProjectID != "" {
			slog.Info("new task associated with Snyk project — scan may be triggered",
				slog.String("snyk_project_id", payload.SnykProjectID),
				slog.String("task_id", e.TaskID))
		}
		return nil
	}); err != nil {
		slog.Error("failed to subscribe to task created events", slog.Any("error", err))
	}
}

func (a *SnykAdapter) snykRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, snykAPIBase+path, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "token "+a.apiToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("snyk API error %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
