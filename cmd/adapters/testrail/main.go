package main

import (
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

// TestRail / Xray adapter integrates with TestRail's API for test case management
// and Xray (Jira plugin) for test execution results. Webhooks from both services
// route test run outcomes to Forge's message bus.
const testRailAPIBase = "/index.php?/api/v2"

type TestRailAdapter struct {
	testRailURL      string
	testRailUser     string
	testRailAPIKey   string
	xrayClientID     string
	xrayClientSecret string
	bus              events.Bus
	httpClient       *http.Client
}

type testRailRun struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	PassedCount   int    `json:"passed_count"`
	FailedCount   int    `json:"failed_count"`
	UntestedCount int    `json:"untested_count"`
	IsCompleted   bool   `json:"is_completed"`
}

type testRailWebhookPayload struct {
	Name    string      `json:"name"`
	Payload testRailRun `json:"payload"`
}

type xrayTestExecutionResult struct {
	TestExecKey string `json:"testExecKey"`
	Status      string `json:"status"`
	Tests       []struct {
		Status  string `json:"status"`
		TestKey string `json:"testKey"`
	} `json:"tests"`
}

func main() {
	logger.Init("testrail-adapter")

	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		slog.Error("failed to create event bus", slog.Any("error", err))
		os.Exit(1)
	}

	adapter := &TestRailAdapter{
		testRailURL:      strings.TrimRight(os.Getenv("TESTRAIL_URL"), "/"),
		testRailUser:     os.Getenv("TESTRAIL_USER"),
		testRailAPIKey:   os.Getenv("TESTRAIL_API_KEY"),
		xrayClientID:     os.Getenv("XRAY_CLIENT_ID"),
		xrayClientSecret: os.Getenv("XRAY_CLIENT_SECRET"),
		bus:              bus,
		httpClient:       &http.Client{},
	}

	go adapter.subscribeToEvents()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/webhook/testrail", adapter.HandleTestRailWebhook)
	mux.HandleFunc("/webhook/xray", adapter.HandleXrayWebhook)
	mux.HandleFunc("/api/v1/runs", adapter.HandleRuns)
	mux.HandleFunc("/api/v1/results", adapter.HandleResults)

	slog.Info("TestRail / Xray adapter listening", slog.String("addr", ":19130"))
	http.ListenAndServe(":19130", logger.HTTPMiddleware("testrail-adapter", mux))
}

func (a *TestRailAdapter) HandleTestRailWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload testRailWebhookPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if payload.Name == "close_run" && payload.Payload.IsCompleted {
		if payload.Payload.FailedCount > 0 {
			ep, _ := json.Marshal(map[string]interface{}{
				"run_id":   payload.Payload.ID,
				"run_name": payload.Payload.Name,
				"failed":   payload.Payload.FailedCount,
				"passed":   payload.Payload.PassedCount,
				"source":   "testrail",
			})
			a.bus.Publish(r.Context(), events.Event{Type: events.TaskBlocked, Payload: ep})
		} else {
			ep, _ := json.Marshal(map[string]interface{}{
				"run_id":   payload.Payload.ID,
				"run_name": payload.Payload.Name,
				"passed":   payload.Payload.PassedCount,
				"source":   "testrail",
			})
			a.bus.Publish(r.Context(), events.Event{Type: events.TaskCompleted, Payload: ep})
		}
	}
	w.WriteHeader(http.StatusOK)
}

func (a *TestRailAdapter) HandleXrayWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var result xrayTestExecutionResult
	if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	failedCount := 0
	for _, t := range result.Tests {
		if t.Status == "FAIL" {
			failedCount++
		}
	}
	if failedCount > 0 {
		ep, _ := json.Marshal(map[string]interface{}{
			"exec_key": result.TestExecKey,
			"failed":   failedCount,
			"source":   "xray",
		})
		a.bus.Publish(r.Context(), events.Event{Type: events.TaskBlocked, Payload: ep})
	} else if result.Status == "PASS" {
		ep, _ := json.Marshal(map[string]interface{}{
			"exec_key": result.TestExecKey,
			"source":   "xray",
		})
		a.bus.Publish(r.Context(), events.Event{Type: events.TaskCompleted, Payload: ep})
	}
	w.WriteHeader(http.StatusOK)
}

// HandleRuns lists TestRail runs for a project.
//
//	GET /api/v1/runs?project_id=<id>
func (a *TestRailAdapter) HandleRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	projectID := r.URL.Query().Get("project_id")
	if projectID == "" {
		http.Error(w, "project_id query parameter is required", http.StatusBadRequest)
		return
	}
	var result interface{}
	if err := a.trRequest(r.Context(), http.MethodGet, fmt.Sprintf("/get_runs/%s", projectID), nil, &result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// HandleResults lists test results for a run.
//
//	GET /api/v1/results?run_id=<id>
func (a *TestRailAdapter) HandleResults(w http.ResponseWriter, r *http.Request) {
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
	if err := a.trRequest(r.Context(), http.MethodGet, fmt.Sprintf("/get_results_for_run/%s", runID), nil, &result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// subscribeToEvents listens for ReviewApproved events and triggers a TestRail
// run for the project named in the payload, so tests run before code ships.
func (a *TestRailAdapter) subscribeToEvents() {
	ctx := context.Background()
	if err := a.bus.Subscribe(ctx, []events.EventType{events.ReviewApproved}, func(e events.Event) error {
		var payload struct {
			ProjectID string `json:"testrail_project_id"`
			SuiteID   string `json:"testrail_suite_id"`
		}
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			return fmt.Errorf("unmarshal review approved payload: %w", err)
		}
		if payload.ProjectID == "" || a.testRailURL == "" {
			return nil // no project ID — nothing to run
		}

		slog.Info("triggering TestRail run from review approved event",
			slog.String("project_id", payload.ProjectID),
			slog.String("task_id", e.TaskID))

		body := map[string]interface{}{
			"name": fmt.Sprintf("Forge automated run — task %s", e.TaskID),
		}
		if payload.SuiteID != "" {
			body["suite_id"] = payload.SuiteID
		}
		var result interface{}
		if err := a.trRequest(ctx, http.MethodPost, fmt.Sprintf("/add_run/%s", payload.ProjectID), body, &result); err != nil {
			slog.Warn("failed to create TestRail run",
				slog.String("project_id", payload.ProjectID),
				slog.Any("error", err))
		}
		return nil
	}); err != nil {
		slog.Error("failed to subscribe to review approved events", slog.Any("error", err))
	}
}

func (a *TestRailAdapter) trRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {
	if a.testRailURL == "" {
		return fmt.Errorf("TESTRAIL_URL not configured")
	}

	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, a.testRailURL+testRailAPIBase+path, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.SetBasicAuth(a.testRailUser, a.testRailAPIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("testrail API error %d: %s", resp.StatusCode, string(b))
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
