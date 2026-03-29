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

const k6APIBase = "https://api.k6.io/v3"

type K6Adapter struct {
	apiToken   string
	projectID  string
	bus        events.Bus
	httpClient *http.Client
}

type k6RunWebhook struct {
	Event struct {
		Type    string `json:"type"`
		TestRun struct {
			ID           int    `json:"id"`
			Name         string `json:"name"`
			Status       string `json:"status"`
			ResultStatus string `json:"result_status"`
			ProjectID    int    `json:"project_id"`
			ThresholdsResults []struct {
				Name   string `json:"name"`
				Passed bool   `json:"passed"`
			} `json:"thresholds_results"`
		} `json:"test_run"`
	} `json:"event"`
}

func main() {
	logger.Init("k6cloud-adapter")

	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		slog.Error("failed to create event bus", slog.Any("error", err))
		os.Exit(1)
	}

	adapter := &K6Adapter{
		apiToken:   os.Getenv("K6_CLOUD_API_TOKEN"),
		projectID:  os.Getenv("K6_PROJECT_ID"),
		bus:        bus,
		httpClient: &http.Client{},
	}

	go adapter.subscribeToEvents()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/webhook", adapter.HandleWebhook)
	mux.HandleFunc("/api/v1/runs", adapter.HandleRuns)
	mux.HandleFunc("/api/v1/thresholds", adapter.HandleThresholds)

	slog.Info("k6 Cloud adapter listening", slog.String("addr", ":19135"))
	http.ListenAndServe(":19135", logger.HTTPMiddleware("k6cloud-adapter", mux))
}

func (a *K6Adapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload k6RunWebhook
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	switch payload.Event.Type {
	case "TEST_FINISHED":
		run := payload.Event.TestRun
		var breached []string
		for _, t := range run.ThresholdsResults {
			if !t.Passed {
				breached = append(breached, t.Name)
			}
		}
		if run.ResultStatus == "passed" && len(breached) == 0 {
			ep, _ := json.Marshal(map[string]interface{}{
				"run_id":     run.ID,
				"run_name":   run.Name,
				"project_id": run.ProjectID,
				"source":     "k6cloud",
			})
			if err := a.bus.Publish(r.Context(), events.Event{Type: events.TaskCompleted, Payload: ep}); err != nil {
				slog.Error("failed to publish task completed event",
					slog.Int("run_id", run.ID),
					slog.Any("error", err))
			}
		} else {
			ep, _ := json.Marshal(map[string]interface{}{
				"run_id":              run.ID,
				"run_name":            run.Name,
				"project_id":          run.ProjectID,
				"result_status":       run.ResultStatus,
				"breached_thresholds": breached,
				"source":              "k6cloud",
			})
			if err := a.bus.Publish(r.Context(), events.Event{Type: events.EscalationCreated, Payload: ep}); err != nil {
				slog.Error("failed to publish escalation event",
					slog.Int("run_id", run.ID),
					slog.Any("error", err))
			}
		}
	case "TEST_ABORTED":
		run := payload.Event.TestRun
		ep, _ := json.Marshal(map[string]interface{}{
			"run_id":     run.ID,
			"run_name":   run.Name,
			"project_id": run.ProjectID,
			"source":     "k6cloud",
		})
		if err := a.bus.Publish(r.Context(), events.Event{Type: events.TaskFailed, Payload: ep}); err != nil {
			slog.Error("failed to publish task failed event",
				slog.Int("run_id", run.ID),
				slog.Any("error", err))
		}
	}
	w.WriteHeader(http.StatusOK)
}

// HandleRuns lists recent test runs or triggers a new one.
//
//	GET  /api/v1/runs[?project_id=<id>]
//	POST /api/v1/runs
func (a *K6Adapter) HandleRuns(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		projectID := r.URL.Query().Get("project_id")
		if projectID == "" {
			projectID = a.projectID
		}
		var result interface{}
		if err := a.k6Request(r.Context(), http.MethodGet,
			fmt.Sprintf("/test-runs?project_id=%s", projectID), nil, &result); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	case http.MethodPost:
		var body interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var result interface{}
		if err := a.k6Request(r.Context(), http.MethodPost, "/test-runs", body, &result); err != nil {
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

// HandleThresholds returns threshold results for a specific test run.
//
//	GET /api/v1/thresholds?run_id=<id>
func (a *K6Adapter) HandleThresholds(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	runID := r.URL.Query().Get("run_id")
	if runID == "" {
		http.Error(w, "run_id query parameter is required", http.StatusBadRequest)
		return
	}

	var result interface{}
	if err := a.k6Request(r.Context(), http.MethodGet,
		fmt.Sprintf("/test-runs/%s/thresholds", runID), nil, &result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// subscribeToEvents listens for DeploymentApproved events and triggers a k6 Cloud
// test run if k6_test_id is present in the payload.
func (a *K6Adapter) subscribeToEvents() {
	ctx := context.Background()
	if err := a.bus.Subscribe(ctx, []events.EventType{events.DeploymentApproved}, func(e events.Event) error {
		var payload struct {
			TestID    int    `json:"k6_test_id"`
			ProjectID string `json:"k6_project_id"`
		}
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			return fmt.Errorf("unmarshal deployment approved payload: %w", err)
		}
		if payload.TestID == 0 {
			return nil
		}

		slog.Info("triggering k6 Cloud load test for deployment",
			slog.Int("test_id", payload.TestID),
			slog.String("task_id", e.TaskID))

		body := map[string]interface{}{"test_id": payload.TestID}
		if err := a.k6Request(ctx, http.MethodPost, "/test-runs", body, nil); err != nil {
			slog.Warn("failed to trigger k6 test run",
				slog.Int("test_id", payload.TestID),
				slog.Any("error", err))
		}
		return nil
	}); err != nil {
		slog.Error("failed to subscribe to deployment approved events", slog.Any("error", err))
	}
}

func (a *K6Adapter) k6Request(ctx context.Context, method, path string, body interface{}, out interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, k6APIBase+path, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+a.apiToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("k6 cloud API error %d: %s", resp.StatusCode, string(b))
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
