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
)

const argoCDAPIBase = "/api/v1"

type ArgoCDAdapter struct {
	baseURL         string
	token           string
	orchestratorURL string
	bus             events.Bus
	httpClient      *http.Client
}

type argoCDAppStatus struct {
	Phase   string `json:"phase"`
	Message string `json:"message"`
}

type argoCDApp struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Status struct {
		OperationState *argoCDAppStatus `json:"operationState"`
		Sync           struct {
			Status string `json:"status"`
		} `json:"sync"`
		Health struct {
			Status string `json:"status"`
		} `json:"health"`
	} `json:"status"`
}

type argoCDWebhookPayload struct {
	Application argoCDApp `json:"application"`
}

func main() {
	logger.Init("argocd-adapter")

	baseURL := os.Getenv("ARGOCD_URL")
	token := os.Getenv("ARGOCD_TOKEN")
	if baseURL == "" || token == "" {
		slog.Error("ARGOCD_URL and ARGOCD_TOKEN are required")
		os.Exit(1)
	}

	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		slog.Error("failed to create event bus", slog.Any("error", err))
		os.Exit(1)
	}

	adapter := &ArgoCDAdapter{
		baseURL:         strings.TrimRight(baseURL, "/"),
		token:           token,
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
	mux.HandleFunc("/api/v1/apps", adapter.HandleApps)
	mux.HandleFunc("/api/v1/sync", adapter.HandleSync)
	mux.HandleFunc("/api/v1/rollback", adapter.HandleRollback)

	slog.Info("ArgoCD adapter listening", slog.String("addr", ":19113"))
	http.ListenAndServe(":19113", logger.HTTPMiddleware("argocd-adapter", mux))
}

func (a *ArgoCDAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload argoCDWebhookPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	app := payload.Application
	if app.Status.OperationState != nil {
		switch app.Status.OperationState.Phase {
		case "Succeeded":
			a.handleSyncSucceeded(r.Context(), app)
		case "Failed", "Error":
			a.handleSyncFailed(r.Context(), app)
		case "Running":
			a.handleSyncRunning(r.Context(), app)
		}
	}

	w.WriteHeader(http.StatusOK)
}

func (a *ArgoCDAdapter) handleSyncSucceeded(ctx context.Context, app argoCDApp) {
	ep, _ := json.Marshal(map[string]interface{}{
		"app_name":    app.Metadata.Name,
		"sync_status": app.Status.Sync.Status,
		"health":      app.Status.Health.Status,
		"source":      "argocd",
	})
	if err := a.bus.Publish(ctx, events.Event{Type: events.DeploymentApproved, Payload: ep}); err != nil {
		slog.Error("failed to publish deployment approved event",
			slog.String("app_name", app.Metadata.Name),
			slog.Any("error", err))
	}
}

func (a *ArgoCDAdapter) handleSyncFailed(ctx context.Context, app argoCDApp) {
	ep, _ := json.Marshal(map[string]interface{}{
		"app_name": app.Metadata.Name,
		"message":  app.Status.OperationState.Message,
		"source":   "argocd",
	})
	if err := a.bus.Publish(ctx, events.Event{Type: events.TaskFailed, Payload: ep}); err != nil {
		slog.Error("failed to publish task failed event",
			slog.String("app_name", app.Metadata.Name),
			slog.Any("error", err))
	}
}

func (a *ArgoCDAdapter) handleSyncRunning(ctx context.Context, app argoCDApp) {
	ep, _ := json.Marshal(map[string]interface{}{
		"app_name": app.Metadata.Name,
		"source":   "argocd",
	})
	if err := a.bus.Publish(ctx, events.Event{Type: events.TaskStarted, Payload: ep}); err != nil {
		slog.Error("failed to publish task started event",
			slog.String("app_name", app.Metadata.Name),
			slog.Any("error", err))
	}
}

// HandleApps lists all ArgoCD applications or fetches a single app by name.
//
//	GET /api/v1/apps
//	GET /api/v1/apps?name=<app-name>
func (a *ArgoCDAdapter) HandleApps(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := "/applications"
	if name := r.URL.Query().Get("name"); name != "" {
		path = fmt.Sprintf("/applications/%s", name)
	}

	var result map[string]interface{}
	if err := a.argoRequest(r.Context(), http.MethodGet, path, nil, &result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// HandleSync triggers a sync for the specified ArgoCD application.
//
//	POST /api/v1/sync?app=<app-name>
//	Body (optional): {"revision":"HEAD","prune":false,"dryRun":false}
func (a *ArgoCDAdapter) HandleSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	appName := r.URL.Query().Get("app")
	if appName == "" {
		http.Error(w, "app query parameter is required", http.StatusBadRequest)
		return
	}

	var syncOpts map[string]interface{}
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&syncOpts); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	if syncOpts == nil {
		syncOpts = map[string]interface{}{}
	}

	var result map[string]interface{}
	if err := a.argoRequest(r.Context(), http.MethodPost,
		fmt.Sprintf("/applications/%s/sync", appName), syncOpts, &result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// HandleRollback rolls back an ArgoCD application to a previous deployment ID.
//
//	POST /api/v1/rollback?app=<app-name>  {"id":<deployment-id>}
func (a *ArgoCDAdapter) HandleRollback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	appName := r.URL.Query().Get("app")
	if appName == "" {
		http.Error(w, "app query parameter is required", http.StatusBadRequest)
		return
	}

	var req struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.ID == 0 {
		http.Error(w, "deployment id is required", http.StatusBadRequest)
		return
	}

	var result map[string]interface{}
	if err := a.argoRequest(r.Context(), http.MethodPost,
		fmt.Sprintf("/applications/%s/rollback", appName),
		map[string]interface{}{"id": req.ID}, &result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	slog.Info("rollback triggered",
		slog.String("app_name", appName),
		slog.Int64("deployment_id", req.ID))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// subscribeToEvents listens for DeploymentRequested events and triggers an
// ArgoCD sync for the target application named in the payload.
func (a *ArgoCDAdapter) subscribeToEvents() {
	ctx := context.Background()
	if err := a.bus.Subscribe(ctx, []events.EventType{events.DeploymentRequested}, func(e events.Event) error {
		var payload map[string]interface{}
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			return fmt.Errorf("unmarshal deployment payload: %w", err)
		}

		appName, _ := payload["app_name"].(string)
		if appName == "" {
			slog.Warn("DeploymentRequested missing app_name — skipping ArgoCD sync")
			return nil
		}

		slog.Info("triggering ArgoCD sync from deployment event",
			slog.String("app_name", appName),
			slog.String("task_id", e.TaskID))

		var result map[string]interface{}
		if err := a.argoRequest(ctx, http.MethodPost,
			fmt.Sprintf("/applications/%s/sync", appName),
			map[string]interface{}{}, &result); err != nil {
			return fmt.Errorf("trigger argocd sync for %s: %w", appName, err)
		}
		return nil
	}); err != nil {
		slog.Error("failed to subscribe to deployment events", slog.Any("error", err))
	}
}

func (a *ArgoCDAdapter) argoRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.baseURL+argoCDAPIBase+path, bodyReader)
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
		return fmt.Errorf("argocd API error %d: %s", resp.StatusCode, string(b))
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
