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

	"github.com/dotrage/forge-adp/pkg/events"
	"github.com/dotrage/forge-adp/pkg/logger"
)

const splitAPIBase = "https://api.split.io/internal/api/v2"

type SplitIOAdapter struct {
	apiKey      string
	environment string
	workspace   string
	bus         events.Bus
	httpClient  *http.Client
}

type splitWebhookPayload struct {
	Type    string `json:"type"`
	Feature struct {
		Name        string `json:"name"`
		Environment string `json:"environment"`
		Killed      bool   `json:"killed"`
		Treatment   string `json:"defaultTreatment"`
	} `json:"feature"`
}

func main() {
	logger.Init("splitio-adapter")

	apiKey := os.Getenv("SPLIT_API_KEY")
	if apiKey == "" {
		slog.Error("SPLIT_API_KEY is required")
		os.Exit(1)
	}

	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		slog.Error("failed to create event bus", slog.Any("error", err))
		os.Exit(1)
	}

	adapter := &SplitIOAdapter{
		apiKey:      apiKey,
		environment: os.Getenv("SPLIT_ENVIRONMENT"),
		workspace:   os.Getenv("SPLIT_WORKSPACE_ID"),
		bus:         bus,
		httpClient:  &http.Client{},
	}

	go adapter.subscribeToEvents()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/webhook", adapter.HandleWebhook)
	mux.HandleFunc("/api/v1/splits", adapter.HandleSplits)
	mux.HandleFunc("/api/v1/toggles", adapter.HandleToggles)

	slog.Info("Split.io adapter listening", slog.String("addr", ":19128"))
	http.ListenAndServe(":19128", logger.HTTPMiddleware("splitio-adapter", mux))
}

func (a *SplitIOAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload splitWebhookPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	switch payload.Type {
	case "SPLIT_KILLED":
		ep, _ := json.Marshal(map[string]interface{}{
			"split_name":  payload.Feature.Name,
			"environment": payload.Feature.Environment,
			"source":      "splitio",
		})
		if err := a.bus.Publish(r.Context(), events.Event{Type: events.EscalationCreated, Payload: ep}); err != nil {
			slog.Error("failed to publish escalation event",
				slog.String("split_name", payload.Feature.Name),
				slog.Any("error", err))
		}
	case "SPLIT_UPDATED":
		ep, _ := json.Marshal(map[string]interface{}{
			"split_name":  payload.Feature.Name,
			"environment": payload.Feature.Environment,
			"treatment":   payload.Feature.Treatment,
			"source":      "splitio",
		})
		if err := a.bus.Publish(r.Context(), events.Event{Type: events.TaskCompleted, Payload: ep}); err != nil {
			slog.Error("failed to publish task completed event",
				slog.String("split_name", payload.Feature.Name),
				slog.Any("error", err))
		}
	}
	w.WriteHeader(http.StatusOK)
}

// HandleSplits lists or creates splits in the configured workspace.
//
//	GET  /api/v1/splits[?name=<split>]
//	POST /api/v1/splits
func (a *SplitIOAdapter) HandleSplits(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		splitName := r.URL.Query().Get("name")
		path := fmt.Sprintf("/splits/ws/%s", a.workspace)
		if splitName != "" {
			path += "/" + splitName
		}
		var result interface{}
		if err := a.splitRequest(r.Context(), http.MethodGet, path, nil, &result); err != nil {
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
		path := fmt.Sprintf("/splits/ws/%s", a.workspace)
		var result interface{}
		if err := a.splitRequest(r.Context(), http.MethodPost, path, req, &result); err != nil {
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

// HandleToggles updates the treatment configuration for a split in an environment.
//
//	POST /api/v1/toggles?name=<split>[&env=<env>]
func (a *SplitIOAdapter) HandleToggles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	splitName := r.URL.Query().Get("name")
	if splitName == "" {
		http.Error(w, "name query parameter is required", http.StatusBadRequest)
		return
	}
	env := r.URL.Query().Get("env")
	if env == "" {
		env = a.environment
	}

	var req map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	path := fmt.Sprintf("/splits/ws/%s/%s/environments/%s", a.workspace, splitName, env)
	var result interface{}
	if err := a.splitRequest(r.Context(), http.MethodPatch, path, req, &result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// subscribeToEvents listens for DeploymentApproved events. When a deployment is approved
// with a split_name in the payload, the split's treatment is updated to "on".
func (a *SplitIOAdapter) subscribeToEvents() {
	ctx := context.Background()
	if err := a.bus.Subscribe(ctx, []events.EventType{events.DeploymentApproved}, func(e events.Event) error {
		var payload struct {
			SplitName   string `json:"split_name"`
			Environment string `json:"split_environment"`
			Treatment   string `json:"split_treatment"`
		}
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			return fmt.Errorf("unmarshal deployment approved payload: %w", err)
		}
		if payload.SplitName == "" {
			return nil
		}
		env := payload.Environment
		if env == "" {
			env = a.environment
		}
		treatment := payload.Treatment
		if treatment == "" {
			treatment = "on"
		}

		slog.Info("updating Split.io feature flag for approved deployment",
			slog.String("split_name", payload.SplitName),
			slog.String("environment", env),
			slog.String("task_id", e.TaskID))

		req := map[string]interface{}{"treatment": treatment}
		path := fmt.Sprintf("/splits/ws/%s/%s/environments/%s", a.workspace, payload.SplitName, env)
		if err := a.splitRequest(ctx, http.MethodPatch, path, req, nil); err != nil {
			slog.Warn("failed to update Split.io treatment",
				slog.String("split_name", payload.SplitName),
				slog.Any("error", err))
		}
		return nil
	}); err != nil {
		slog.Error("failed to subscribe to deployment approved events", slog.Any("error", err))
	}
}

func (a *SplitIOAdapter) splitRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, splitAPIBase+path, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("split.io API error %d: %s", resp.StatusCode, string(b))
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
