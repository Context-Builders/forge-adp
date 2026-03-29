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

const adoBoardsAPIBase = "https://dev.azure.com"

// AzureDevOpsBoardsAdapter handles Azure Boards work item webhooks and exposes
// endpoints to create/update work items (the Boards-only counterpart to the
// azuredevopsrepos adapter).
type AzureDevOpsBoardsAdapter struct {
	organization string
	project      string
	pat          string
	bus          events.Bus
	httpClient   *http.Client
}

type adoWorkItemEvent struct {
	EventType string `json:"eventType"`
	Resource  struct {
		ID     int `json:"id"`
		Rev    int `json:"rev"`
		Fields struct {
			Title      struct{ NewValue string `json:"newValue"` } `json:"System.Title"`
			State      struct{ NewValue string `json:"newValue"` } `json:"System.State"`
			WorkType   struct{ Value string `json:"$value"` } `json:"System.WorkItemType"`
			AssignedTo struct{ NewValue string `json:"newValue"` } `json:"System.AssignedTo"`
			Tags       struct{ NewValue string `json:"newValue"` } `json:"System.Tags"`
		} `json:"fields"`
		URL string `json:"url"`
	} `json:"resource"`
}

func main() {
	logger.Init("azuredevopsboards-adapter")

	org := os.Getenv("AZURE_DEVOPS_ORG")
	pat := os.Getenv("AZURE_DEVOPS_PAT")
	if org == "" || pat == "" {
		slog.Error("AZURE_DEVOPS_ORG and AZURE_DEVOPS_PAT are required")
		os.Exit(1)
	}

	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		slog.Error("failed to create event bus", slog.Any("error", err))
		os.Exit(1)
	}

	adapter := &AzureDevOpsBoardsAdapter{
		organization: org,
		project:      os.Getenv("AZURE_DEVOPS_PROJECT"),
		pat:          pat,
		bus:          bus,
		httpClient:   &http.Client{},
	}

	go adapter.subscribeToEvents()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/webhook", adapter.HandleWebhook)
	mux.HandleFunc("/api/v1/workitems", adapter.HandleWorkItems)
	mux.HandleFunc("/api/v1/transitions", adapter.HandleTransitions)

	slog.Info("Azure DevOps Boards adapter listening", slog.String("addr", ":19124"))
	http.ListenAndServe(":19124", logger.HTTPMiddleware("azuredevopsboards-adapter", mux))
}

func (a *AzureDevOpsBoardsAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload adoWorkItemEvent
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	switch payload.EventType {
	case "workitem.created":
		tags := payload.Resource.Fields.Tags.NewValue
		if strings.Contains(tags, "forge") {
			a.handleWorkItemCreated(r.Context(), payload)
		}
	case "workitem.updated":
		switch payload.Resource.Fields.State.NewValue {
		case "Done", "Closed", "Resolved":
			a.handleWorkItemCompleted(r.Context(), payload)
		case "Removed":
			a.handleWorkItemRemoved(r.Context(), payload)
		}
	}
	w.WriteHeader(http.StatusOK)
}

func (a *AzureDevOpsBoardsAdapter) handleWorkItemCreated(ctx context.Context, p adoWorkItemEvent) {
	ep, _ := json.Marshal(map[string]interface{}{
		"work_item_id": p.Resource.ID,
		"title":        p.Resource.Fields.Title.NewValue,
		"url":          p.Resource.URL,
		"source":       "azuredevopsboards",
	})
	if err := a.bus.Publish(ctx, events.Event{Type: events.TaskCreated, Payload: ep}); err != nil {
		slog.Error("failed to publish task created event",
			slog.Int("work_item_id", p.Resource.ID),
			slog.Any("error", err))
	}
}

func (a *AzureDevOpsBoardsAdapter) handleWorkItemCompleted(ctx context.Context, p adoWorkItemEvent) {
	ep, _ := json.Marshal(map[string]interface{}{
		"work_item_id": p.Resource.ID,
		"state":        p.Resource.Fields.State.NewValue,
		"source":       "azuredevopsboards",
	})
	if err := a.bus.Publish(ctx, events.Event{Type: events.TaskCompleted, Payload: ep}); err != nil {
		slog.Error("failed to publish task completed event",
			slog.Int("work_item_id", p.Resource.ID),
			slog.Any("error", err))
	}
}

func (a *AzureDevOpsBoardsAdapter) handleWorkItemRemoved(ctx context.Context, p adoWorkItemEvent) {
	ep, _ := json.Marshal(map[string]interface{}{
		"work_item_id": p.Resource.ID,
		"source":       "azuredevopsboards",
	})
	if err := a.bus.Publish(ctx, events.Event{Type: events.TaskFailed, Payload: ep}); err != nil {
		slog.Error("failed to publish task failed event",
			slog.Int("work_item_id", p.Resource.ID),
			slog.Any("error", err))
	}
}

// HandleWorkItems lists or creates Azure Boards work items.
//
//	GET  /api/v1/workitems[?id=<id>]
//	POST /api/v1/workitems  (JSON-patch body)
func (a *AzureDevOpsBoardsAdapter) HandleWorkItems(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		id := r.URL.Query().Get("id")
		path := fmt.Sprintf("/%s/%s/_apis/wit/workitems", a.organization, a.project)
		if id != "" {
			path += "/" + id
		}
		path += "?api-version=7.0"
		var result map[string]interface{}
		if err := a.adoRequest(r.Context(), http.MethodGet, path, nil, &result); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)

	case http.MethodPost:
		var req interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		path := fmt.Sprintf("/%s/%s/_apis/wit/workitems/$Task?api-version=7.0", a.organization, a.project)
		var result map[string]interface{}
		if err := a.adoRequest(r.Context(), http.MethodPost, path, req, &result); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// HandleTransitions patches a work item's fields (e.g., state transition).
//
//	POST /api/v1/transitions?id=<id>  (JSON-patch body)
func (a *AzureDevOpsBoardsAdapter) HandleTransitions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "id query parameter is required", http.StatusBadRequest)
		return
	}

	var req interface{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	path := fmt.Sprintf("/%s/%s/_apis/wit/workitems/%s?api-version=7.0", a.organization, a.project, id)
	var result map[string]interface{}
	if err := a.adoRequest(r.Context(), http.MethodPatch, path, req, &result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// subscribeToEvents listens for Forge task events and updates the corresponding
// Azure Boards work item state to keep them in sync.
func (a *AzureDevOpsBoardsAdapter) subscribeToEvents() {
	ctx := context.Background()
	if err := a.bus.Subscribe(ctx, []events.EventType{
		events.TaskCompleted,
		events.TaskFailed,
		events.TaskBlocked,
	}, func(e events.Event) error {
		var payload struct {
			WorkItemID string `json:"work_item_id"`
		}
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			return fmt.Errorf("unmarshal payload: %w", err)
		}
		if payload.WorkItemID == "" {
			return nil // no work item ID in event — nothing to update
		}

		var newState string
		switch e.Type {
		case events.TaskCompleted:
			newState = "Done"
		case events.TaskFailed:
			newState = "Removed"
		case events.TaskBlocked:
			newState = "Active" // "Active" keeps it visible but signals it needs attention
		}

		slog.Info("updating ADO work item state",
			slog.String("work_item_id", payload.WorkItemID),
			slog.String("state", newState),
			slog.String("task_id", e.TaskID))

		patch := []map[string]interface{}{
			{
				"op":    "add",
				"path":  "/fields/System.State",
				"value": newState,
			},
		}
		path := fmt.Sprintf("/%s/%s/_apis/wit/workitems/%s?api-version=7.0",
			a.organization, a.project, payload.WorkItemID)
		if err := a.adoRequest(ctx, http.MethodPatch, path, patch, nil); err != nil {
			slog.Warn("failed to update work item state",
				slog.String("work_item_id", payload.WorkItemID),
				slog.Any("error", err))
		}
		return nil
	}); err != nil {
		slog.Error("failed to subscribe to task events", slog.Any("error", err))
	}
}

func (a *AzureDevOpsBoardsAdapter) adoRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, adoBoardsAPIBase+path, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.SetBasicAuth("", a.pat)
	req.Header.Set("Content-Type", "application/json-patch+json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("azure devops boards API error %d: %s", resp.StatusCode, string(b))
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
