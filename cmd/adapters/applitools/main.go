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

const applitoolsAPIBase = "https://eyesapi.applitools.com/api/v1"

type ApplitoolsAdapter struct {
	apiKey     string
	bus        events.Bus
	httpClient *http.Client
}

type applitoolsBatchWebhook struct {
	Event string `json:"event"`
	Batch struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Status string `json:"status"`
		URL    string `json:"url"`
	} `json:"batch"`
	TestResults struct {
		Total    int `json:"total"`
		Passed   int `json:"passed"`
		Failed   int `json:"failed"`
		New      int `json:"new"`
		Modified int `json:"modified"`
		Missing  int `json:"missing"`
	} `json:"testResults"`
}

func main() {
	logger.Init("applitools-adapter")

	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		slog.Error("failed to create event bus", slog.Any("error", err))
		os.Exit(1)
	}

	adapter := &ApplitoolsAdapter{
		apiKey:     os.Getenv("APPLITOOLS_API_KEY"),
		bus:        bus,
		httpClient: &http.Client{},
	}

	go adapter.subscribeToEvents()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/webhook", adapter.HandleWebhook)
	mux.HandleFunc("/api/v1/batches", adapter.HandleBatches)
	mux.HandleFunc("/api/v1/results", adapter.HandleResults)
	mux.HandleFunc("/api/v1/baselines", adapter.HandleBaselines)

	slog.Info("Applitools adapter listening", slog.String("addr", ":19136"))
	http.ListenAndServe(":19136", logger.HTTPMiddleware("applitools-adapter", mux))
}

func (a *ApplitoolsAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if secret := os.Getenv("APPLITOOLS_WEBHOOK_SECRET"); secret != "" {
		if r.Header.Get("X-Applitools-Signature") != secret {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	var payload applitoolsBatchWebhook
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if payload.Event != "batchCompleted" {
		w.WriteHeader(http.StatusOK)
		return
	}

	switch payload.Batch.Status {
	case "Passed":
		ep, _ := json.Marshal(map[string]interface{}{
			"batch_id":   payload.Batch.ID,
			"batch_name": payload.Batch.Name,
			"total":      payload.TestResults.Total,
			"passed":     payload.TestResults.Passed,
			"url":        payload.Batch.URL,
			"source":     "applitools",
		})
		a.bus.Publish(r.Context(), events.Event{Type: events.TaskCompleted, Payload: ep})
	case "Failed":
		ep, _ := json.Marshal(map[string]interface{}{
			"batch_id":   payload.Batch.ID,
			"batch_name": payload.Batch.Name,
			"failed":     payload.TestResults.Failed,
			"new":        payload.TestResults.New,
			"modified":   payload.TestResults.Modified,
			"url":        payload.Batch.URL,
			"source":     "applitools",
		})
		a.bus.Publish(r.Context(), events.Event{Type: events.TaskBlocked, Payload: ep})
	case "Unresolved":
		ep, _ := json.Marshal(map[string]interface{}{
			"batch_id":   payload.Batch.ID,
			"batch_name": payload.Batch.Name,
			"new":        payload.TestResults.New,
			"modified":   payload.TestResults.Modified,
			"url":        payload.Batch.URL,
			"source":     "applitools",
		})
		a.bus.Publish(r.Context(), events.Event{Type: events.ReviewRequested, Payload: ep})
	}
	w.WriteHeader(http.StatusOK)
}

func (a *ApplitoolsAdapter) HandleBatches(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var result interface{}
	if err := a.atRequest(r.Context(), http.MethodGet, "/batches", nil, &result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (a *ApplitoolsAdapter) HandleResults(w http.ResponseWriter, r *http.Request) {
	batchID := r.URL.Query().Get("batch_id")
	if batchID == "" {
		http.Error(w, "batch_id query parameter is required", http.StatusBadRequest)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var result interface{}
	if err := a.atRequest(r.Context(), http.MethodGet, fmt.Sprintf("/batches/%s/results", batchID), nil, &result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (a *ApplitoolsAdapter) HandleBaselines(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		var result interface{}
		if err := a.atRequest(r.Context(), http.MethodGet, "/baselines", nil, &result); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	case http.MethodDelete:
		baselineID := r.URL.Query().Get("id")
		if baselineID == "" {
			http.Error(w, "id query parameter is required", http.StatusBadRequest)
			return
		}
		if err := a.atRequest(r.Context(), http.MethodDelete, fmt.Sprintf("/baselines/%s", baselineID), nil, nil); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// subscribeToEvents listens for ReviewApproved events. When a review is approved
// Forge logs it so visual test baselines can be promoted by operators.
func (a *ApplitoolsAdapter) subscribeToEvents() {
	ctx := context.Background()
	if err := a.bus.Subscribe(ctx, []events.EventType{events.ReviewApproved}, func(e events.Event) error {
		var payload struct {
			BatchID string `json:"applitools_batch_id"`
		}
		json.Unmarshal(e.Payload, &payload)
		slog.Info("review approved — Applitools baselines can be accepted",
			slog.String("batch_id", payload.BatchID),
			slog.String("task_id", e.TaskID))
		return nil
	}); err != nil {
		slog.Error("failed to subscribe to review approved events", slog.Any("error", err))
	}
}

func (a *ApplitoolsAdapter) atRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, applitoolsAPIBase+path, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("X-Eyes-Api-Key", a.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("applitools API error %d: %s", resp.StatusCode, string(b))
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
