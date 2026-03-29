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

const postmanAPIBase = "https://api.getpostman.com"

type PostmanAdapter struct {
	apiKey     string
	bus        events.Bus
	httpClient *http.Client
}

type postmanMonitorWebhook struct {
	Monitor struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"monitor"`
	Run struct {
		Status string `json:"status"` // "passed" | "failed"
		Stats  struct {
			Assertions struct {
				Total  int `json:"total"`
				Failed int `json:"failed"`
			} `json:"assertions"`
			Requests struct {
				Total  int `json:"total"`
				Failed int `json:"failed"`
			} `json:"requests"`
		} `json:"stats"`
		Failures []struct {
			Source struct {
				Name string `json:"name"`
			} `json:"source"`
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		} `json:"failures"`
	} `json:"run"`
}

type newmanReport struct {
	Run struct {
		Stats struct {
			Assertions struct {
				Total  int `json:"total"`
				Failed int `json:"failed"`
			} `json:"assertions"`
		} `json:"stats"`
		Collection struct {
			Info struct {
				Name string `json:"name"`
			} `json:"info"`
		} `json:"collection"`
		Failures []struct {
			Error  struct{ Message string } `json:"error"`
			Source struct{ Name string }    `json:"source"`
		} `json:"failures"`
	} `json:"run"`
}

func main() {
	logger.Init("postman-adapter")

	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		slog.Error("failed to create event bus", slog.Any("error", err))
		os.Exit(1)
	}

	adapter := &PostmanAdapter{
		apiKey:     os.Getenv("POSTMAN_API_KEY"),
		bus:        bus,
		httpClient: &http.Client{},
	}

	go adapter.subscribeToEvents()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/webhook/monitor", adapter.HandleMonitorWebhook)
	mux.HandleFunc("/webhook/newman", adapter.HandleNewmanWebhook)
	mux.HandleFunc("/api/v1/collections", adapter.HandleCollections)
	mux.HandleFunc("/api/v1/monitors", adapter.HandleMonitors)
	mux.HandleFunc("/api/v1/runs", adapter.HandleRuns)

	slog.Info("Postman / Newman adapter listening", slog.String("addr", ":19133"))
	http.ListenAndServe(":19133", logger.HTTPMiddleware("postman-adapter", mux))
}

func (a *PostmanAdapter) HandleMonitorWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload postmanMonitorWebhook
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	switch payload.Run.Status {
	case "failed":
		ep, _ := json.Marshal(map[string]interface{}{
			"monitor_id":        payload.Monitor.ID,
			"monitor_name":      payload.Monitor.Name,
			"failed_assertions": payload.Run.Stats.Assertions.Failed,
			"total_assertions":  payload.Run.Stats.Assertions.Total,
			"failure_count":     len(payload.Run.Failures),
			"source":            "postman",
		})
		a.bus.Publish(r.Context(), events.Event{Type: events.EscalationCreated, Payload: ep})
	case "passed":
		ep, _ := json.Marshal(map[string]interface{}{
			"monitor_id":   payload.Monitor.ID,
			"monitor_name": payload.Monitor.Name,
			"total_passed": payload.Run.Stats.Assertions.Total,
			"source":       "postman",
		})
		a.bus.Publish(r.Context(), events.Event{Type: events.TaskCompleted, Payload: ep})
	}
	w.WriteHeader(http.StatusOK)
}

func (a *PostmanAdapter) HandleNewmanWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var report newmanReport
	if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if report.Run.Stats.Assertions.Failed > 0 {
		ep, _ := json.Marshal(map[string]interface{}{
			"collection":        report.Run.Collection.Info.Name,
			"failed_assertions": report.Run.Stats.Assertions.Failed,
			"total_assertions":  report.Run.Stats.Assertions.Total,
			"failures":          report.Run.Failures,
			"source":            "newman",
		})
		a.bus.Publish(r.Context(), events.Event{Type: events.TaskBlocked, Payload: ep})
	} else {
		ep, _ := json.Marshal(map[string]interface{}{
			"collection":   report.Run.Collection.Info.Name,
			"total_passed": report.Run.Stats.Assertions.Total,
			"source":       "newman",
		})
		a.bus.Publish(r.Context(), events.Event{Type: events.TaskCompleted, Payload: ep})
	}
	w.WriteHeader(http.StatusOK)
}

// HandleCollections lists Postman collections.
//
//	GET /api/v1/collections
func (a *PostmanAdapter) HandleCollections(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var result interface{}
	if err := a.pmRequest(r.Context(), http.MethodGet, "/collections", nil, &result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// HandleMonitors lists Postman monitors.
//
//	GET /api/v1/monitors
func (a *PostmanAdapter) HandleMonitors(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var result interface{}
	if err := a.pmRequest(r.Context(), http.MethodGet, "/monitors", nil, &result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// HandleRuns triggers a Postman collection run via Postman Cloud.
//
//	POST /api/v1/runs  {"collection_id":"...","environment_id":"..."}
func (a *PostmanAdapter) HandleRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		CollectionID  string `json:"collection_id"`
		EnvironmentID string `json:"environment_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	payload := map[string]interface{}{"collection": body.CollectionID}
	if body.EnvironmentID != "" {
		payload["environment"] = body.EnvironmentID
	}
	var result interface{}
	if err := a.pmRequest(r.Context(), http.MethodPost,
		"/collections/"+body.CollectionID+"/runs", payload, &result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(result)
}

// subscribeToEvents listens for ReviewApproved events and triggers a Postman
// collection run so API contracts are verified before deployment.
func (a *PostmanAdapter) subscribeToEvents() {
	ctx := context.Background()
	if err := a.bus.Subscribe(ctx, []events.EventType{events.ReviewApproved}, func(e events.Event) error {
		var payload struct {
			CollectionID  string `json:"postman_collection_id"`
			EnvironmentID string `json:"postman_environment_id"`
		}
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			return fmt.Errorf("unmarshal review approved payload: %w", err)
		}
		if payload.CollectionID == "" || a.apiKey == "" {
			return nil // no collection to run
		}

		slog.Info("triggering Postman collection run from review approved event",
			slog.String("collection_id", payload.CollectionID),
			slog.String("task_id", e.TaskID))

		body := map[string]interface{}{"collection": payload.CollectionID}
		if payload.EnvironmentID != "" {
			body["environment"] = payload.EnvironmentID
		}
		var result interface{}
		if err := a.pmRequest(ctx, http.MethodPost,
			"/collections/"+payload.CollectionID+"/runs", body, &result); err != nil {
			slog.Warn("failed to trigger Postman collection run",
				slog.String("collection_id", payload.CollectionID),
				slog.Any("error", err))
		}
		return nil
	}); err != nil {
		slog.Error("failed to subscribe to review approved events", slog.Any("error", err))
	}
}

func (a *PostmanAdapter) pmRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, postmanAPIBase+path, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("X-Api-Key", a.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("postman API error %d: %s", resp.StatusCode, string(b))
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
