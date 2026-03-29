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

const zephyrAPIBase = "https://api.zephyrscale.smartbear.com/v2"

// ZephyrAdapter integrates with the Zephyr Scale (TM4J) test management API.
// It receives test cycle and execution result webhooks and exposes a REST bridge
// for the QA agent to create/report test cycles directly.
type ZephyrAdapter struct {
	apiToken   string
	projectKey string
	bus        events.Bus
	httpClient *http.Client
}

type zephyrWebhookPayload struct {
	WebhookEvent string `json:"webhookEvent"`
	TestCycle    struct {
		ID         string `json:"id"`
		Key        string `json:"key"`
		Name       string `json:"name"`
		Status     string `json:"status"`
		ProjectKey string `json:"projectKey"`
	} `json:"testCycle"`
	TestExecution struct {
		ID          string `json:"id"`
		Key         string `json:"key"`
		StatusName  string `json:"statusName"`
		TestCaseKey string `json:"testCaseKey"`
	} `json:"testExecution"`
}

func main() {
	logger.Init("zephyr-adapter")

	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		slog.Error("failed to create event bus", slog.Any("error", err))
		os.Exit(1)
	}

	adapter := &ZephyrAdapter{
		apiToken:   os.Getenv("ZEPHYR_API_TOKEN"),
		projectKey: os.Getenv("ZEPHYR_PROJECT_KEY"),
		bus:        bus,
		httpClient: &http.Client{},
	}

	go adapter.subscribeToEvents()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/webhook", adapter.HandleWebhook)
	mux.HandleFunc("/api/v1/cycles", adapter.HandleCycles)
	mux.HandleFunc("/api/v1/executions", adapter.HandleExecutions)
	mux.HandleFunc("/api/v1/cases", adapter.HandleTestCases)

	slog.Info("Zephyr Scale adapter listening", slog.String("addr", ":19132"))
	http.ListenAndServe(":19132", logger.HTTPMiddleware("zephyr-adapter", mux))
}

func (a *ZephyrAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if secret := os.Getenv("ZEPHYR_WEBHOOK_SECRET"); secret != "" {
		if r.Header.Get("X-Zephyr-Secret") != secret {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	var payload zephyrWebhookPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	switch payload.WebhookEvent {
	case "testCycle_updated":
		switch strings.ToUpper(payload.TestCycle.Status) {
		case "DONE", "PASSED":
			ep, _ := json.Marshal(map[string]interface{}{
				"cycle_key":   payload.TestCycle.Key,
				"cycle_name":  payload.TestCycle.Name,
				"project_key": payload.TestCycle.ProjectKey,
				"source":      "zephyr",
			})
			if err := a.bus.Publish(r.Context(), events.Event{Type: events.TaskCompleted, Payload: ep}); err != nil {
				slog.Error("failed to publish task completed event",
					slog.String("cycle_key", payload.TestCycle.Key),
					slog.Any("error", err))
			}
		case "FAILED":
			ep, _ := json.Marshal(map[string]interface{}{
				"cycle_key":   payload.TestCycle.Key,
				"cycle_name":  payload.TestCycle.Name,
				"project_key": payload.TestCycle.ProjectKey,
				"source":      "zephyr",
			})
			if err := a.bus.Publish(r.Context(), events.Event{Type: events.TaskBlocked, Payload: ep}); err != nil {
				slog.Error("failed to publish task blocked event",
					slog.String("cycle_key", payload.TestCycle.Key),
					slog.Any("error", err))
			}
		}
	case "testExecution_updated":
		if strings.ToUpper(payload.TestExecution.StatusName) == "FAIL" {
			ep, _ := json.Marshal(map[string]interface{}{
				"execution_key": payload.TestExecution.Key,
				"test_case_key": payload.TestExecution.TestCaseKey,
				"source":        "zephyr",
			})
			if err := a.bus.Publish(r.Context(), events.Event{Type: events.EscalationCreated, Payload: ep}); err != nil {
				slog.Error("failed to publish escalation event",
					slog.String("execution_key", payload.TestExecution.Key),
					slog.Any("error", err))
			}
		}
	}
	w.WriteHeader(http.StatusOK)
}

// HandleCycles lists or creates Zephyr test cycles.
//
//	GET  /api/v1/cycles[?project_key=<key>]
//	POST /api/v1/cycles
func (a *ZephyrAdapter) HandleCycles(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		projectKey := r.URL.Query().Get("project_key")
		if projectKey == "" {
			projectKey = a.projectKey
		}
		var result interface{}
		if err := a.zephyrRequest(r.Context(), http.MethodGet,
			fmt.Sprintf("/testcycles?projectKey=%s", projectKey), nil, &result); err != nil {
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
		if err := a.zephyrRequest(r.Context(), http.MethodPost, "/testcycles", body, &result); err != nil {
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

// HandleExecutions lists or creates test executions.
//
//	GET  /api/v1/executions?cycle_key=<key>
//	POST /api/v1/executions
func (a *ZephyrAdapter) HandleExecutions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cycleKey := r.URL.Query().Get("cycle_key")
		if cycleKey == "" {
			http.Error(w, "cycle_key query parameter is required", http.StatusBadRequest)
			return
		}
		var result interface{}
		if err := a.zephyrRequest(r.Context(), http.MethodGet,
			fmt.Sprintf("/testexecutions?testCycle=%s", cycleKey), nil, &result); err != nil {
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
		if err := a.zephyrRequest(r.Context(), http.MethodPost, "/testexecutions", body, &result); err != nil {
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

// HandleTestCases lists test cases for a project.
//
//	GET /api/v1/cases[?project_key=<key>]
func (a *ZephyrAdapter) HandleTestCases(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	projectKey := r.URL.Query().Get("project_key")
	if projectKey == "" {
		projectKey = a.projectKey
	}

	var result interface{}
	if err := a.zephyrRequest(r.Context(), http.MethodGet,
		fmt.Sprintf("/testcases?projectKey=%s", projectKey), nil, &result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// subscribeToEvents listens for ReviewApproved events and creates a new Zephyr
// test cycle if jira_key and zephyr_project_key are present in the payload.
func (a *ZephyrAdapter) subscribeToEvents() {
	ctx := context.Background()
	if err := a.bus.Subscribe(ctx, []events.EventType{events.ReviewApproved}, func(e events.Event) error {
		var payload struct {
			JiraKey    string `json:"jira_key"`
			ProjectKey string `json:"zephyr_project_key"`
			CycleName  string `json:"zephyr_cycle_name"`
		}
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			return fmt.Errorf("unmarshal review approved payload: %w", err)
		}
		projectKey := payload.ProjectKey
		if projectKey == "" {
			projectKey = a.projectKey
		}
		if projectKey == "" {
			return nil
		}

		cycleName := payload.CycleName
		if cycleName == "" && payload.JiraKey != "" {
			cycleName = "Regression - " + payload.JiraKey
		}
		if cycleName == "" {
			return nil
		}

		slog.Info("creating Zephyr test cycle for approved review",
			slog.String("project_key", projectKey),
			slog.String("cycle_name", cycleName),
			slog.String("task_id", e.TaskID))

		body := map[string]interface{}{
			"name":       cycleName,
			"projectKey": projectKey,
		}
		var result interface{}
		if err := a.zephyrRequest(ctx, http.MethodPost, "/testcycles", body, &result); err != nil {
			slog.Warn("failed to create Zephyr test cycle",
				slog.String("cycle_name", cycleName),
				slog.Any("error", err))
		}
		return nil
	}); err != nil {
		slog.Error("failed to subscribe to review approved events", slog.Any("error", err))
	}
}

func (a *ZephyrAdapter) zephyrRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, zephyrAPIBase+path, bodyReader)
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
		return fmt.Errorf("zephyr API error %d: %s", resp.StatusCode, string(b))
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
