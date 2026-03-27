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
	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		log.Fatalf("failed to create event bus: %v", err)
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
log.Printf("Postman / Newman adapter listening on :19133")
http.ListenAndServe(":19133", mux)
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
		"monitor_id":         payload.Monitor.ID,
		"monitor_name":       payload.Monitor.Name,
		"failed_assertions":  payload.Run.Stats.Assertions.Failed,
		"total_assertions":   payload.Run.Stats.Assertions.Total,
		"failure_count":      len(payload.Run.Failures),
		"source":             "postman",
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

func (a *PostmanAdapter) HandleCollections(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
		case http.MethodGet:

var result interface{}
if err := a.pmRequest(r.Context(), http.MethodGet, "/collections", nil, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
default:
http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
}

func (a *PostmanAdapter) HandleMonitors(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
		case http.MethodGet:

var result interface{}
if err := a.pmRequest(r.Context(), http.MethodGet, "/monitors", nil, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
default:
http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
}

func (a *PostmanAdapter) HandleRuns(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
		case http.MethodPost:
// Trigger a collection run via Postman Cloud

var body struct {
	CollectionID string `json:"collection_id"`
	EnvironmentID string `json:"environment_id,omitempty"`
}
if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}
payload := map[string]interface{}{
	"collection": body.CollectionID,
}
if body.EnvironmentID != "" {
	payload["environment"] = body.EnvironmentID
}

var result interface{}
if err := a.pmRequest(r.Context(), http.MethodPost, "/collections/"+body.CollectionID+"/runs", payload, &result); err != nil {
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

func (a *PostmanAdapter) subscribeToEvents() {
	ctx := context.Background()
	a.bus.Subscribe(ctx, []events.EventType{events.ReviewApproved}, func(e events.Event) error {
		return nil
})
}

func (a *PostmanAdapter) pmRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {

var bodyReader io.Reader
if body != nil {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
bodyReader = strings.NewReader(string(b))
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
