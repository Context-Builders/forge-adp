package main

import (
	"log"
	"net/http"
	"os"
	"strings"
	"github.com/dotrage/forge-adp/pkg/events"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
)

const shortcutAPIBase = "https://api.app.shortcut.com/api/v3"

type ShortcutAdapter struct {
	token         string
	webhookSecret string
	bus           events.Bus
	httpClient    *http.Client
}

type shortcutStory struct {
	ID          int      `json:"id"`
	Name        string   `json:"name"`
	StoryType   string   `json:"story_type"`
	WorkflowStateID int  `json:"workflow_state_id"`
	Labels      []struct{ Name string `json:"name"` } `json:"labels"`
	AppURL      string   `json:"app_url"`
}

type shortcutWebhookAction struct {
	Action     string        `json:"action"`
	EntityType string        `json:"entity_type"`
	ID         int           `json:"id"`
	Changes    map[string]interface{} `json:"changes"`
}

type shortcutWebhookPayload struct {
	Actions []shortcutWebhookAction `json:"actions"`
}

func main() {
	token := os.Getenv("SHORTCUT_API_TOKEN")
	if token == "" {
		log.Fatal("SHORTCUT_API_TOKEN is required")
	}
bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
if err != nil {
	log.Fatalf("failed to create event bus: %v", err)
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
log.Printf("Shortcut (Clubhouse) adapter listening on :19125")
http.ListenAndServe(":19125", mux)
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
	log.Printf("failed to publish task created event: %v", err)
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
	log.Printf("failed to publish task completed event: %v", err)
}
}
}

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

func (a *ShortcutAdapter) subscribeToEvents() {
	ctx := context.Background()
	a.bus.Subscribe(ctx, []events.EventType{
		events.TaskCompleted,
		events.TaskFailed,
		events.TaskBlocked,
	}, func(e events.Event) error {
	return nil
})
}

func (a *ShortcutAdapter) scRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {

var bodyReader io.Reader
if body != nil {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
bodyReader = strings.NewReader(string(b))
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
