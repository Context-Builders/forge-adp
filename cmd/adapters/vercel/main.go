package main

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"github.com/dotrage/forge-adp/pkg/events"
	"context"
	"crypto/hmac"
)

const vercelAPIBase = "https://api.vercel.com"

type VercelAdapter struct {
	token      string
	teamID     string
	webhookSecret string
	bus        events.Bus
	httpClient *http.Client
}

type vercelDeployment struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	URL   string `json:"url"`
	State string `json:"state"`
	Meta  map[string]string `json:"meta"`
}

type vercelWebhookPayload struct {
	Type       string           `json:"type"`
	Payload    vercelDeployment `json:"payload"`
}

func main() {
	token := os.Getenv("VERCEL_TOKEN")
	if token == "" {
		log.Fatal("VERCEL_TOKEN is required")
	}
bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
if err != nil {
	log.Fatalf("failed to create event bus: %v", err)
}
adapter := &VercelAdapter{
	token:         token,
	teamID:        os.Getenv("VERCEL_TEAM_ID"),
	webhookSecret: os.Getenv("VERCEL_WEBHOOK_SECRET"),
	bus:           bus,
	httpClient:    &http.Client{},
}
go adapter.subscribeToEvents()
mux := http.NewServeMux()
mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})
mux.HandleFunc("/webhook", adapter.HandleWebhook)
mux.HandleFunc("/api/v1/deployments", adapter.HandleDeployments)
mux.HandleFunc("/api/v1/projects", adapter.HandleProjects)
log.Printf("Vercel adapter listening on :19108")
http.ListenAndServe(":19108", mux)
}

func (a *VercelAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
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
	sig := r.Header.Get("X-Vercel-Signature")
	mac := hmac.New(sha1.New, []byte(a.webhookSecret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(sig)) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
}

var payload vercelWebhookPayload
if err := json.Unmarshal(body, &payload); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}
switch payload.Type {
	case "deployment.succeeded":
	a.handleDeploymentSucceeded(r.Context(), payload.Payload)
	case "deployment.error", "deployment.canceled":
	a.handleDeploymentFailed(r.Context(), payload.Payload)
	case "deployment.ready":
	a.handleDeploymentReady(r.Context(), payload.Payload)
}
w.WriteHeader(http.StatusOK)
}

func (a *VercelAdapter) handleDeploymentSucceeded(ctx context.Context, d vercelDeployment) {
	ep, _ := json.Marshal(map[string]interface{}{
		"deployment_id": d.ID,
		"name":          d.Name,
		"url":           d.URL,
		"state":         d.State,
		"source":        "vercel",
})
if err := a.bus.Publish(ctx, events.Event{Type: events.TaskCompleted, Payload: ep}); err != nil {
	log.Printf("failed to publish task completed event: %v", err)
}
}

func (a *VercelAdapter) handleDeploymentReady(ctx context.Context, d vercelDeployment) {
	ep, _ := json.Marshal(map[string]interface{}{
		"deployment_id": d.ID,
		"name":          d.Name,
		"url":           d.URL,
		"source":        "vercel",
})
if err := a.bus.Publish(ctx, events.Event{Type: events.DeploymentApproved, Payload: ep}); err != nil {
	log.Printf("failed to publish deployment approved event: %v", err)
}
}

func (a *VercelAdapter) handleDeploymentFailed(ctx context.Context, d vercelDeployment) {
	ep, _ := json.Marshal(map[string]interface{}{
		"deployment_id": d.ID,
		"name":          d.Name,
		"state":         d.State,
		"source":        "vercel",
})
if err := a.bus.Publish(ctx, events.Event{Type: events.TaskFailed, Payload: ep}); err != nil {
	log.Printf("failed to publish task failed event: %v", err)
}
}

func (a *VercelAdapter) HandleDeployments(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
		case http.MethodGet:
		path := "/v6/deployments"
		if a.teamID != "" {
			path += "?teamId=" + a.teamID
		}

var result map[string]interface{}
if err := a.vercelRequest(r.Context(), http.MethodGet, path, nil, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
default:
http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
}

func (a *VercelAdapter) HandleProjects(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
		case http.MethodGet:
		path := "/v9/projects"
		if a.teamID != "" {
			path += "?teamId=" + a.teamID
		}

var result map[string]interface{}
if err := a.vercelRequest(r.Context(), http.MethodGet, path, nil, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
default:
http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
}

func (a *VercelAdapter) subscribeToEvents() {
	ctx := context.Background()
	a.bus.Subscribe(ctx, []events.EventType{events.DeploymentRequested}, func(e events.Event) error {
		return nil
})
}

func (a *VercelAdapter) vercelRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {

var bodyReader io.Reader
if body != nil {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
bodyReader = strings.NewReader(string(b))
}
req, err := http.NewRequestWithContext(ctx, method, vercelAPIBase+path, bodyReader)
if err != nil {
	return fmt.Errorf("create request: %w", err)
}
req.Header.Set("Authorization", "Bearer "+a.token)
req.Header.Set("Content-Type", "application/json")
resp, err := a.httpClient.Do(req)
if err != nil {
	return fmt.Errorf("execute request: %w", err)
}
defer resp.Body.Close()
if resp.StatusCode >= 300 {
	b, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("vercel API error %d: %s", resp.StatusCode, string(b))
}
if out != nil {
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
}
return nil
}
