package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/dotrage/forge-adp/pkg/events"
)

// GCP adapter integrates with Google Cloud Pub/Sub push subscriptions for
// Cloud Monitoring alert notifications and Cloud Build events.

type GCPAdapter struct {
	projectID  string
	serviceKey string
	bus        events.Bus
	httpClient *http.Client
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
		IncidentID string `json:"incident_id"`
		PolicyName string `json:"policy_name"`
		State      string `json:"state"`
		Condition  struct {
			Name string `json:"name"`
		} `json:"condition"`
		ResourceName string `json:"resource_name"`
		URL          string `json:"url"`
	} `json:"incident"`
	Version string `json:"version"`
}

func main() {
	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		log.Fatalf("failed to create event bus: %v", err)
	}

	adapter := &GCPAdapter{
		projectID:  os.Getenv("GCP_PROJECT_ID"),
		serviceKey: os.Getenv("GCP_SERVICE_ACCOUNT_KEY"),
		bus:        bus,
		httpClient: &http.Client{},
	}

	go adapter.subscribeToEvents()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/webhook/pubsub/build", adapter.HandleCloudBuild)
	mux.HandleFunc("/webhook/pubsub/monitoring", adapter.HandleCloudMonitoring)
	mux.HandleFunc("/api/v1/builds", adapter.HandleBuilds)

	log.Printf("GCP adapter listening on :19120")
	http.ListenAndServe(":19120", mux)
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
		a.bus.Publish(r.Context(), events.Event{Type: events.TaskCompleted, Payload: ep})
	case "FAILURE", "INTERNAL_ERROR", "TIMEOUT", "CANCELLED":
		ep, _ := json.Marshal(map[string]interface{}{
			"build_id": build.ID,
			"status":   build.Status,
			"log_url":  build.LogURL,
			"source":   "gcp_cloud_build",
		})
		a.bus.Publish(r.Context(), events.Event{Type: events.TaskFailed, Payload: ep})
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
		a.bus.Publish(r.Context(), events.Event{Type: events.EscalationCreated, Payload: ep})
	case "closed":
		ep, _ := json.Marshal(map[string]interface{}{
			"incident_id": alert.Incident.IncidentID,
			"policy":      alert.Incident.PolicyName,
			"source":      "gcp_monitoring",
		})
		a.bus.Publish(r.Context(), events.Event{Type: events.TaskCompleted, Payload: ep})
	}

	w.WriteHeader(http.StatusNoContent)
}

func (a *GCPAdapter) HandleBuilds(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		result := map[string]interface{}{
			"message": fmt.Sprintf("Cloud Build API — use GCP SDK for project %s", a.projectID),
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *GCPAdapter) subscribeToEvents() {
	ctx := context.Background()
	a.bus.Subscribe(ctx, []events.EventType{events.DeploymentRequested}, func(e events.Event) error {
		return nil
	})
}

// placeholder to prevent unused import error
var _ = strings.TrimRight
var _ = io.ReadAll
