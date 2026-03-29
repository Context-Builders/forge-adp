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

// GCPAdapter integrates with Google Cloud via Pub/Sub push subscriptions.
// Cloud Build status changes and Cloud Monitoring alert incidents are delivered
// as Pub/Sub push messages to /webhook/pubsub/build and /webhook/pubsub/monitoring.
// Cloud Build REST API calls use a short-lived Bearer token set via GCP_SERVICE_ACCOUNT_TOKEN.
// For production, use google.golang.org/api to exchange the service account JSON key for tokens.

type GCPAdapter struct {
	projectID       string
	serviceToken    string // short-lived access token for Cloud Build REST API
	orchestratorURL string
	bus             events.Bus
	httpClient      *http.Client
}

type pubSubMessage struct {
	Message struct {
		Data       []byte            `json:"data"`
		Attributes map[string]string `json:"attributes"`
		MessageID  string            `json:"messageId"`
	} `json:"message"`
	Subscription string `json:"subscription"`
}

type cloudBuildStatus struct {
	ID            string            `json:"id"`
	Status        string            `json:"status"`
	LogURL        string            `json:"logUrl"`
	Substitutions map[string]string `json:"substitutions"`
}

type monitoringAlert struct {
	Incident struct {
		IncidentID   string `json:"incident_id"`
		PolicyName   string `json:"policy_name"`
		State        string `json:"state"`
		Condition    struct {
			Name string `json:"name"`
		} `json:"condition"`
		ResourceName string `json:"resource_name"`
		URL          string `json:"url"`
	} `json:"incident"`
	Version string `json:"version"`
}

func main() {
	logger.Init("gcp-adapter")

	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		slog.Error("failed to create event bus", slog.Any("error", err))
		os.Exit(1)
	}

	adapter := &GCPAdapter{
		projectID:       os.Getenv("GCP_PROJECT_ID"),
		serviceToken:    os.Getenv("GCP_SERVICE_ACCOUNT_TOKEN"),
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
	mux.HandleFunc("/webhook/pubsub/build", adapter.HandleCloudBuild)
	mux.HandleFunc("/webhook/pubsub/monitoring", adapter.HandleCloudMonitoring)
	mux.HandleFunc("/api/v1/builds", adapter.HandleBuilds)

	slog.Info("GCP adapter listening", slog.String("addr", ":19120"))
	http.ListenAndServe(":19120", logger.HTTPMiddleware("gcp-adapter", mux))
}

func (a *GCPAdapter) HandleCloudBuild(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var msg pubSubMessage
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var build cloudBuildStatus
	if err := json.Unmarshal(msg.Message.Data, &build); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	switch build.Status {
	case "SUCCESS":
		ep, _ := json.Marshal(map[string]interface{}{
			"build_id": build.ID,
			"log_url":  build.LogURL,
			"source":   "gcp_cloud_build",
		})
		if err := a.bus.Publish(r.Context(), events.Event{Type: events.TaskCompleted, Payload: ep}); err != nil {
			slog.Error("failed to publish task completed event",
				slog.String("build_id", build.ID),
				slog.Any("error", err))
		}
	case "FAILURE", "INTERNAL_ERROR", "TIMEOUT", "CANCELLED":
		ep, _ := json.Marshal(map[string]interface{}{
			"build_id": build.ID,
			"status":   build.Status,
			"log_url":  build.LogURL,
			"source":   "gcp_cloud_build",
		})
		if err := a.bus.Publish(r.Context(), events.Event{Type: events.TaskFailed, Payload: ep}); err != nil {
			slog.Error("failed to publish task failed event",
				slog.String("build_id", build.ID),
				slog.Any("error", err))
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

func (a *GCPAdapter) HandleCloudMonitoring(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var msg pubSubMessage
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var alert monitoringAlert
	if err := json.Unmarshal(msg.Message.Data, &alert); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	switch alert.Incident.State {
	case "open":
		ep, _ := json.Marshal(map[string]interface{}{
			"incident_id": alert.Incident.IncidentID,
			"policy":      alert.Incident.PolicyName,
			"condition":   alert.Incident.Condition.Name,
			"resource":    alert.Incident.ResourceName,
			"url":         alert.Incident.URL,
			"source":      "gcp_monitoring",
		})
		if err := a.bus.Publish(r.Context(), events.Event{Type: events.EscalationCreated, Payload: ep}); err != nil {
			slog.Error("failed to publish escalation event",
				slog.String("incident_id", alert.Incident.IncidentID),
				slog.Any("error", err))
		}
	case "closed":
		ep, _ := json.Marshal(map[string]interface{}{
			"incident_id": alert.Incident.IncidentID,
			"policy":      alert.Incident.PolicyName,
			"source":      "gcp_monitoring",
		})
		if err := a.bus.Publish(r.Context(), events.Event{Type: events.TaskCompleted, Payload: ep}); err != nil {
			slog.Error("failed to publish incident resolved event",
				slog.String("incident_id", alert.Incident.IncidentID),
				slog.Any("error", err))
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

// HandleBuilds lists recent Cloud Build executions or triggers a new build via a trigger.
//
//	GET  /api/v1/builds[?filter=<build-filter>]
//	POST /api/v1/builds  {"trigger_id":"<id>","substitutions":{"_ENV":"prod"}}
func (a *GCPAdapter) HandleBuilds(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		path := fmt.Sprintf("https://cloudbuild.googleapis.com/v1/projects/%s/builds", a.projectID)
		if f := r.URL.Query().Get("filter"); f != "" {
			path += "?filter=" + f
		}
		var result map[string]interface{}
		if err := a.gcpRequest(r.Context(), http.MethodGet, path, nil, &result); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)

	case http.MethodPost:
		var req struct {
			TriggerID     string            `json:"trigger_id"`
			Substitutions map[string]string `json:"substitutions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.TriggerID == "" {
			http.Error(w, "trigger_id is required", http.StatusBadRequest)
			return
		}
		path := fmt.Sprintf("https://cloudbuild.googleapis.com/v1/projects/%s/triggers/%s:run",
			a.projectID, req.TriggerID)
		body, _ := json.Marshal(map[string]interface{}{
			"substitutions": req.Substitutions,
		})
		var result map[string]interface{}
		if err := a.gcpRequest(r.Context(), http.MethodPost, path, body, &result); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(result)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// subscribeToEvents listens for DeploymentRequested events and forwards them
// to the orchestrator tagged with provider=gcp.
func (a *GCPAdapter) subscribeToEvents() {
	ctx := context.Background()
	if err := a.bus.Subscribe(ctx, []events.EventType{events.DeploymentRequested}, func(e events.Event) error {
		var payload map[string]interface{}
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			return fmt.Errorf("unmarshal deployment payload: %w", err)
		}
		payload["provider"] = "gcp"
		payload["project_id"] = a.projectID

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
		slog.Info("forwarded GCP deployment to orchestrator", slog.Int("status", resp.StatusCode))
		return nil
	}); err != nil {
		slog.Error("failed to subscribe to deployment events", slog.Any("error", err))
	}
}

func (a *GCPAdapter) gcpRequest(ctx context.Context, method, url string, body []byte, out interface{}) error {
	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if a.serviceToken != "" {
		req.Header.Set("Authorization", "Bearer "+a.serviceToken)
	}
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
		return fmt.Errorf("GCP API error %d: %s", resp.StatusCode, string(b))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
