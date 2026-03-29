package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"

	"github.com/dotrage/forge-adp/pkg/events"
	"github.com/dotrage/forge-adp/pkg/logger"
)

const shortcutAPIBase = "https://api.app.shortcut.com/api/v3"

type ShortcutAdapter struct {
	token         string
	webhookSecret string
	bus           events.Bus
	httpClient    *http.Client
}

type shortcutStory struct {
	ID              int    `json:"id"`
	Name            string `json:"name"`
	StoryType       string `json:"story_type"`
	WorkflowStateID int    `json:"workflow_state_id"`
	Labels          []struct {
		Name string `json:"name"`
	} `json:"labels"`
	AppURL string `json:"app_url"`
}

type shortcutWebhookAction struct {
	Action     string                 `json:"action"`
	EntityType string                 `json:"entity_type"`
	ID         int                    `json:"id"`
	Changes    map[string]interface{} `json:"changes"`
}

type shortcutWebhookPayload struct {
	Actions []shortcutWebhookAction `json:"actions"`
}

func main() {
	logger.Init("shortcut-adapter")

	token := os.Getenv("SHORTCUT_API_TOKEN")
	if token == "" {
		slog.Error("SHORTCUT_API_TOKEN is required")
		os.Exit(1)
	}

	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		slog.Error("failed to create event bus", slog.Any("error", err))
		os.Exit(1)
	}

	adapter := &ShortcutAdapter{
		token:         token,
		webhookSecret: os.Getenv("SHORTCUT_WEBHOOK_SECRET"),
		bus:           bus,
		httpClient:    &http.Client{},
	}

	go adapter.subscribeToEvents()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/webhook", adapter.HandleWebhook)
	mux.HandleFunc("/api/v1/stories", adapter.HandleStories)
	mux.HandleFunc("/api/v1/transitions", adapter.HandleTransitions)

	slog.Info("Shortcut adapter listening", slog.String("addr", ":19125"))
	http.ListenAndServe(":19125", logger.HTTPMiddleware("shortcut-adapter", mux))
}

func (a *ShortcutAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if a.webhookSecret != "" {
		sig := r.Header.Get("Shortcut-Signature")
		mac := hmac.New(sha256.New, []byte(a.webhookSecret))
		mac.Write(body)
		expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(expected), []byte(sig)) {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
	}

	var payload shortcutWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	for _, action := range payload.Actions {
		if action.EntityType != "story" {
			continue
		}
		switch action.Action {
		case "create":
			a.handleStoryCreated(r.Context(), action)
		case "update":
			a.handleStoryUpdated(r.Context(), action)
		}
	}
	w.WriteHeader(http.StatusOK)
}

func (a *ShortcutAdapter) handleStoryCreated(ctx context.Context, action shortcutWebhookAction) {
	ep, _ := json.Marshal(map[string]interface{}{
		"story_id": action.ID,
		"source":   "shortcut",
	})
	if err := a.bus.Publish(ctx, events.Event{Type: events.TaskCreated, Payload: ep}); err != nil {
		slog.Error("failed to publish task created event",
			slog.Int("story_id", action.ID),
			slog.Any("error", err))
	}
}

func (a *ShortcutAdapter) handleStoryUpdated(ctx context.Context, action shortcutWebhookAction) {
	changes := action.Changes
	if completedAt, ok := changes["completed_at"]; ok && completedAt != nil {
		ep, _ := json.Marshal(map[string]interface{}{
			"story_id": action.ID,
			"source":   "shortcut",
		})
		if err := a.bus.Publish(ctx, events.Event{Type: events.TaskCompleted, Payload: ep}); err != nil {
			slog.Error("failed to publish task completed event",
				slog.Int("story_id", action.ID),
				slog.Any("error", err))
		}
	}
}

// HandleStories fetches or creates Shortcut stories.
//
//	GET  /api/v1/stories?id=<id>
//	POST /api/v1/stories
func (a *ShortcutAdapter) HandleStories(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		id := r.URL.Query().Get("id")
		if id == "" {
			http.Error(w, "id query parameter is required", http.StatusBadRequest)
			return
		}
		var result map[string]interface{}
		if err := a.scRequest(r.Context(), http.MethodGet, "/stories/"+id, nil, &result); err != nil {
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
		var result map[string]interface{}
		if err := a.scRequest(r.Context(), http.MethodPost, "/stories", req, &result); err != nil {
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

// HandleTransitions updates a story's workflow state.
//
//	POST /api/v1/transitions?id=<story_id>
func (a *ShortcutAdapter) HandleTransitions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "id query parameter is required", http.StatusBadRequest)
		return
	}

	var req map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var result map[string]interface{}
	if err := a.scRequest(r.Context(), http.MethodPut, "/stories/"+id, req, &result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// subscribeToEvents listens for TaskCompleted, TaskFailed, and TaskBlocked events
// and updates the associated Shortcut story's workflow state if shortcut_story_id
// and shortcut_workflow_state_id are present in the payload.
func (a *ShortcutAdapter) subscribeToEvents() {
	ctx := context.Background()
	if err := a.bus.Subscribe(ctx, []events.EventType{
		events.TaskCompleted,
		events.TaskFailed,
		events.TaskBlocked,
	}, func(e events.Event) error {
		var payload struct {
			StoryID         int `json:"shortcut_story_id"`
			WorkflowStateID int `json:"shortcut_workflow_state_id"`
		}
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			return fmt.Errorf("unmarshal payload: %w", err)
		}
		if payload.StoryID == 0 || payload.WorkflowStateID == 0 {
			return nil
		}

		slog.Info("updating Shortcut story workflow state",
			slog.Int("story_id", payload.StoryID),
			slog.Int("workflow_state_id", payload.WorkflowStateID),
			slog.String("event_type", string(e.Type)),
			slog.String("task_id", e.TaskID))

		body := map[string]interface{}{"workflow_state_id": payload.WorkflowStateID}
		if err := a.scRequest(ctx, http.MethodPut, fmt.Sprintf("/stories/%d", payload.StoryID), body, nil); err != nil {
			slog.Warn("failed to update Shortcut story",
				slog.Int("story_id", payload.StoryID),
				slog.Any("error", err))
		}
		return nil
	}); err != nil {
		slog.Error("failed to subscribe to task events", slog.Any("error", err))
	}
}

func (a *ShortcutAdapter) scRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, shortcutAPIBase+path, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Shortcut-Token", a.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("shortcut API error %d: %s", resp.StatusCode, string(b))
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
