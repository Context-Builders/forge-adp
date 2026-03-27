package main

import (
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
	"crypto/sha1"
	"encoding/hex"
)

const atlasAPIBase = "https://cloud.mongodb.com/api/atlas/v2"

type MongoDBAtlasAdapter struct {
	publicKey  string
	privateKey string
	projectID  string
	bus        events.Bus
	httpClient *http.Client
}

type atlasAlert struct {
	ID          string `json:"id"`
	EventType   string `json:"eventTypeName"`
	Status      string `json:"status"`
	AlertConfig struct {
		Matchers []interface{} `json:"matchers"`
	} `json:"alertConfigId"`
ClusterName string `json:"clusterName"`
}

type atlasWebhookPayload struct {
	ID        string     `json:"id"`
	EventType string     `json:"eventTypeName"`
	Alert     atlasAlert `json:"alert"`
}

func main() {
	publicKey := os.Getenv("MONGODB_ATLAS_PUBLIC_KEY")
	privateKey := os.Getenv("MONGODB_ATLAS_PRIVATE_KEY")
	if publicKey == "" || privateKey == "" {
		log.Fatal("MONGODB_ATLAS_PUBLIC_KEY and MONGODB_ATLAS_PRIVATE_KEY are required")
	}
bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
if err != nil {
	log.Fatalf("failed to create event bus: %v", err)
}
adapter := &MongoDBAtlasAdapter{
	publicKey:  publicKey,
	privateKey: privateKey,
	projectID:  os.Getenv("MONGODB_ATLAS_PROJECT_ID"),
	bus:        bus,
	httpClient: &http.Client{},
}
go adapter.subscribeToEvents()
mux := http.NewServeMux()
mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})
mux.HandleFunc("/webhook", adapter.HandleWebhook)
mux.HandleFunc("/api/v1/alerts", adapter.HandleAlerts)
mux.HandleFunc("/api/v1/clusters", adapter.HandleClusters)
log.Printf("MongoDB Atlas adapter listening on :19106")
http.ListenAndServe(":19106", mux)
}

func (a *MongoDBAtlasAdapter) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
body, err := io.ReadAll(r.Body)
if err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}
secret := os.Getenv("MONGODB_ATLAS_WEBHOOK_SECRET")
if secret != "" {
	sig := r.Header.Get("X-MMS-Event-Subscription-Signature")
	if !validateHMACSHA1(secret, body, sig) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
}

var payload atlasWebhookPayload
if err := json.Unmarshal(body, &payload); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}
switch payload.EventType {
	case "ALERT_OPENED", "ALERT_CREATED":
	a.handleAlertOpened(r.Context(), payload)
	case "ALERT_CLOSED":
	a.handleAlertClosed(r.Context(), payload)
}
w.WriteHeader(http.StatusOK)
}

func (a *MongoDBAtlasAdapter) handleAlertOpened(ctx context.Context, p atlasWebhookPayload) {
	eventPayload, _ := json.Marshal(map[string]interface{}{
		"alert_id":     p.ID,
		"event_type":   p.EventType,
		"cluster_name": p.Alert.ClusterName,
		"source":       "mongodbatlas",
})
if err := a.bus.Publish(ctx, events.Event{
	Type:    events.EscalationCreated,
	Payload: eventPayload,
}); err != nil {
log.Printf("failed to publish escalation event: %v", err)
}
}

func (a *MongoDBAtlasAdapter) handleAlertClosed(ctx context.Context, p atlasWebhookPayload) {
	eventPayload, _ := json.Marshal(map[string]interface{}{
		"alert_id":   p.ID,
		"event_type": p.EventType,
		"source":     "mongodbatlas",
})
if err := a.bus.Publish(ctx, events.Event{
	Type:    events.TaskCompleted,
	Payload: eventPayload,
}); err != nil {
log.Printf("failed to publish task completed event: %v", err)
}
}

func (a *MongoDBAtlasAdapter) HandleAlerts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
		case http.MethodGet:

var result map[string]interface{}
path := fmt.Sprintf("/groups/%s/alerts", a.projectID)
if err := a.atlasRequest(r.Context(), http.MethodGet, path, nil, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
default:
http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
}

func (a *MongoDBAtlasAdapter) HandleClusters(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
		case http.MethodGet:

var result map[string]interface{}
path := fmt.Sprintf("/groups/%s/clusters", a.projectID)
if err := a.atlasRequest(r.Context(), http.MethodGet, path, nil, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
default:
http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
}

func (a *MongoDBAtlasAdapter) subscribeToEvents() {
	ctx := context.Background()
	a.bus.Subscribe(ctx, []events.EventType{
		events.TaskCompleted,
	}, func(e events.Event) error {
	return nil
})
}

func (a *MongoDBAtlasAdapter) atlasRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {

var bodyReader io.Reader
if body != nil {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
bodyReader = strings.NewReader(string(b))
}
req, err := http.NewRequestWithContext(ctx, method, atlasAPIBase+path, bodyReader)
if err != nil {
	return fmt.Errorf("create request: %w", err)
}
req.SetBasicAuth(a.publicKey, a.privateKey)
req.Header.Set("Content-Type", "application/json")
req.Header.Set("Accept", "application/vnd.atlas.2023-01-01+json")
resp, err := a.httpClient.Do(req)
if err != nil {
	return fmt.Errorf("execute request: %w", err)
}
defer resp.Body.Close()
if resp.StatusCode >= 300 {
	b, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("atlas API error %d: %s", resp.StatusCode, string(b))
}
if out != nil {
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
}
return nil
}

func validateHMACSHA1(secret string, body []byte, signature string) bool {
	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write(body)
	expected := "sha1=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}
