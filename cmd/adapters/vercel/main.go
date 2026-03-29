package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
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

const vercelAPIBase = "https://api.vercel.com"

type VercelAdapter struct {
	token           string
	teamID          string
	webhookSecret   string
	orchestratorURL string
	bus             events.Bus
	httpClient      *http.Client
}

type vercelDeployment struct {
	ID    string            `json:"id"`
	Name  string            `json:"name"`
	URL   string            `json:"url"`
	State string            `json:"state"`
	Meta  map[string]string `json:"meta"`
}

type vercelWebhookPayload struct {
	Type    string           `json:"type"`
	Payload vercelDeployment `json:"payload"`
}

func main() {
	logger.Init("vercel-adapter")

	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		slog.Error("failed to create event bus", slog.Any("error", err))
		os.Exit(1)
	}

	token := os.Getenv("VERCEL_TOKEN")
	if token == "" {
		slog.Error("VERCEL_TOKEN is required")
		os.Exit(1)
	}

	adapter := &VercelAdapter{
		token:           token,
		teamID:          os.Getenv("VERCEL_TEAM_ID"),
		webhookSecret:   os.Getenv("VERCEL_WEBHOOK_SECRET"),
		orchestratorURL: os.Getenv("ORCHESTRATOR_URL"),
		bus:             bus,
		httpClient:      &http.Client{},
	}
	if adapter.orchestratorURL == "" {
		adapter.orchestratorURL = "http://localhost:19080"
	}

	go adapter.subscribeToEvents()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/webhook", adapter.HandleWebhook)
	mux.HandleFunc("/api/v1/deployments", adapter.HandleDeployments)
	mux.HandleFunc("/api/v1/projects", adapter.HandleProjects)

	slog.Info("Vercel adapter listening", slog.String("addr", ":19108"))
	http.ListenAndServe(":19108", logger.HTTPMiddleware("vercel-adapter", mux))
}

func (a *VercelAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if a.webhookSecret != "" {
		sig := r.Header.Get("X-Vercel-Signature")
		mac := hmac.New(sha1.New, []byte(a.webhookSecret))
		mac.Write(body)
		expected := hex.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(expected), []byte(sig)) {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
	}

	var payload vercelWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	switch payload.Type {
	case "deployment.succeeded":
		a.handleDeploymentSucceeded(r.Context(), payload.Payload)
	case "deployment.error", "deployment.canceled":
		a.handleDeploymentFailed(r.Context(), payload.Payload)
	case "deployment.ready":
		a.handleDeploymentReady(r.Context(), payload.Payload)
	}

	w.WriteHeader(http.StatusOK)
}

func (a *VercelAdapter) handleDeploymentSucceeded(ctx context.Context, d vercelDeployment) {
	ep, _ := json.Marshal(map[string]interface{}{
		"deployment_id": d.ID,
		"name":          d.Name,
		"url":           d.URL,
		"state":         d.State,
		"source":        "vercel",
	})
	if err := a.bus.Publish(ctx, events.Event{Type: events.TaskCompleted, Payload: ep}); err != nil {
		slog.Error("failed to publish task completed event",
			slog.String("deployment_id", d.ID),
			slog.Any("error", err))
	}
}

func (a *VercelAdapter) handleDeploymentReady(ctx context.Context, d vercelDeployment) {
	ep, _ := json.Marshal(map[string]interface{}{
		"deployment_id": d.ID,
		"name":          d.Name,
		"url":           d.URL,
		"source":        "vercel",
	})
	if err := a.bus.Publish(ctx, events.Event{Type: events.DeploymentApproved, Payload: ep}); err != nil {
		slog.Error("failed to publish deployment approved event",
			slog.String("deployment_id", d.ID),
			slog.Any("error", err))
	}
}

func (a *VercelAdapter) handleDeploymentFailed(ctx context.Context, d vercelDeployment) {
	ep, _ := json.Marshal(map[string]interface{}{
		"deployment_id": d.ID,
		"name":          d.Name,
		"state":         d.State,
		"source":        "vercel",
	})
	if err := a.bus.Publish(ctx, events.Event{Type: events.TaskFailed, Payload: ep}); err != nil {
		slog.Error("failed to publish task failed event",
			slog.String("deployment_id", d.ID),
			slog.Any("error", err))
	}
}

// HandleDeployments lists recent deployments or creates a new one.
//
//	GET  /api/v1/deployments[?project=<name>&limit=20]
//	POST /api/v1/deployments  {"name":"my-app","gitSource":{"ref":"main","type":"github","repoId":"123"}}
func (a *VercelAdapter) HandleDeployments(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		path := "/v6/deployments?"
		if a.teamID != "" {
			path += "teamId=" + a.teamID + "&"
		}
		if proj := r.URL.Query().Get("project"); proj != "" {
			path += "projectId=" + proj + "&"
		}
		if limit := r.URL.Query().Get("limit"); limit != "" {
			path += "limit=" + limit
		}
		var result map[string]interface{}
		if err := a.vercelRequest(r.Context(), http.MethodGet, path, nil, &result); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)

	case http.MethodPost:
		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		path := "/v13/deployments"
		if a.teamID != "" {
			path += "?teamId=" + a.teamID
		}
		var result map[string]interface{}
		if err := a.vercelRequest(r.Context(), http.MethodPost, path, req, &result); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(result)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// HandleProjects lists Vercel projects, optionally filtered by name.
//
//	GET /api/v1/projects[?search=<name>]
func (a *VercelAdapter) HandleProjects(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	path := "/v9/projects?"
	if a.teamID != "" {
		path += "teamId=" + a.teamID + "&"
	}
	if s := r.URL.Query().Get("search"); s != "" {
		path += "search=" + s
	}
	var result map[string]interface{}
	if err := a.vercelRequest(r.Context(), http.MethodGet, path, nil, &result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// subscribeToEvents listens for DeploymentRequested events and forwards them
// to the orchestrator tagged with provider=vercel.
func (a *VercelAdapter) subscribeToEvents() {
	ctx := context.Background()
	if err := a.bus.Subscribe(ctx, []events.EventType{events.DeploymentRequested}, func(e events.Event) error {
		var payload map[string]interface{}
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			return fmt.Errorf("unmarshal deployment payload: %w", err)
		}
		payload["provider"] = "vercel"
		if a.teamID != "" {
			payload["team_id"] = a.teamID
		}

		body, _ := json.Marshal(payload)
		url := fmt.Sprintf("%s/api/v1/deployments", a.orchestratorURL)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("create deployment request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("forward deployment: %w", err)
		}
		defer resp.Body.Close()
		slog.Info("forwarded Vercel deployment to orchestrator", slog.Int("status", resp.StatusCode))
		return nil
	}); err != nil {
		slog.Error("failed to subscribe to deployment events", slog.Any("error", err))
	}
}

func (a *VercelAdapter) vercelRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, vercelAPIBase+path, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
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
		return fmt.Errorf("vercel API error %d: %s", resp.StatusCode, string(b))
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
