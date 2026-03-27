package main

import (
	"net/http"
	"os"
	"strings"
	"github.com/dotrage/forge-adp/pkg/events"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
)

const bridgecrewAPIBase = "https://www.bridgecrew.cloud/api/v1"// Trivy results are ingested as SARIF or JSON from CI pipeline artifacts.// Checkov posts results via a webhook or the Bridgecrew platform API.// Checkov / Trivy adapter receives IaC and container security scan results.)

type CheckovAdapter struct {
	bridgecrewToken string
	webhookSecret   string
	bus             events.Bus
	httpClient      *http.Client
}

type checkCovViolation struct {
	PolicyID    string `json:"policy_id"`
	Title       string `json:"title"`
	Severity    string `json:"severity"`
	Resource    string `json:"resource"`
	FilePath    string `json:"file_path"`
	LineNumber  int    `json:"line_from"`
	Description string `json:"description"`
}

type checkCovScanResult struct {
	RepoID     string              `json:"repo_id"`
	Branch     string              `json:"branch"`
	Passed     int                 `json:"passed"`
	Failed     int                 `json:"failed"`
	Violations []checkCovViolation `json:"violations"`
}

type trivySARIFResult struct {
	Schema string `json:"$schema"`
	Runs   []struct {
		Results []struct {
			RuleID  string `json:"ruleId"`
			Level   string `json:"level"`
			Message struct {
				Text string `json:"text"`
			} `json:"message"`
		Locations []struct {
			PhysicalLocation struct {
				ArtifactLocation struct {
					URI string `json:"uri"`
				} `json:"artifactLocation"`
		} `json:"physicalLocation"`
} `json:"locations"`
} `json:"results"`
} `json:"runs"`
}

func main() {
	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		log.Fatalf("failed to create event bus: %v", err)
	}
adapter := &CheckovAdapter{
	bridgecrewToken: os.Getenv("BRIDGECREW_API_TOKEN"),
	webhookSecret:   os.Getenv("CHECKOV_WEBHOOK_SECRET"),
	bus:             bus,
	httpClient:      &http.Client{},
}
mux := http.NewServeMux()
mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})
mux.HandleFunc("/webhook/checkov", adapter.HandleCheckovWebhook)
mux.HandleFunc("/webhook/trivy", adapter.HandleTrivyWebhook)
mux.HandleFunc("/api/v1/violations", adapter.HandleViolations)
mux.HandleFunc("/api/v1/suppressed", adapter.HandleSuppressed)
log.Printf("Checkov / Trivy adapter listening on :19123")
http.ListenAndServe(":19123", mux)
}

func (a *CheckovAdapter) HandleCheckovWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

var result checkCovScanResult
if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}
criticalOrHigh := 0
for _, v := range result.Violations {
	if v.Severity == "CRITICAL" || v.Severity == "HIGH" {
		criticalOrHigh++
	}
}
if criticalOrHigh > 0 {
	ep, _ := json.Marshal(map[string]interface{}{
		"repo":          result.RepoID,
		"branch":        result.Branch,
		"failed":        result.Failed,
		"critical_high": criticalOrHigh,
		"source":        "checkov",
})
if err := a.bus.Publish(r.Context(), events.Event{Type: events.EscalationCreated, Payload: ep}); err != nil {
	log.Printf("failed to publish escalation event: %v", err)
}
} else if result.Failed > 0 {
ep, _ := json.Marshal(map[string]interface{}{
	"repo":   result.RepoID,
	"branch": result.Branch,
	"failed": result.Failed,
	"source": "checkov",
})
if err := a.bus.Publish(r.Context(), events.Event{Type: events.TaskBlocked, Payload: ep}); err != nil {
	log.Printf("failed to publish task blocked event: %v", err)
}
} else {
ep, _ := json.Marshal(map[string]interface{}{
	"repo":   result.RepoID,
	"branch": result.Branch,
	"passed": result.Passed,
	"source": "checkov",
})
if err := a.bus.Publish(r.Context(), events.Event{Type: events.TaskCompleted, Payload: ep}); err != nil {
	log.Printf("failed to publish task completed event: %v", err)
}
}
w.WriteHeader(http.StatusOK)
}

func (a *CheckovAdapter) HandleTrivyWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

var sarif trivySARIFResult
if err := json.NewDecoder(r.Body).Decode(&sarif); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}
errorCount := 0
for _, run := range sarif.Runs {
	for _, result := range run.Results {
		if result.Level == "error" {
			errorCount++
		}
}
}
if errorCount > 0 {
	ep, _ := json.Marshal(map[string]interface{}{
		"error_count": errorCount,
		"source":      "trivy",
})
if err := a.bus.Publish(r.Context(), events.Event{Type: events.EscalationCreated, Payload: ep}); err != nil {
	log.Printf("failed to publish escalation event: %v", err)
}
} else {
ep, _ := json.Marshal(map[string]interface{}{
	"source": "trivy",
	"status": "clean",
})
if err := a.bus.Publish(r.Context(), events.Event{Type: events.TaskCompleted, Payload: ep}); err != nil {
	log.Printf("failed to publish task completed event: %v", err)
}
}
w.WriteHeader(http.StatusOK)
}

func (a *CheckovAdapter) HandleViolations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

var result interface{}
if err := a.bcRequest(r.Context(), http.MethodGet, "/violations/resources", nil, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
}

func (a *CheckovAdapter) HandleSuppressed(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

var result interface{}
if err := a.bcRequest(r.Context(), http.MethodGet, "/suppressions", nil, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
}

func (a *CheckovAdapter) bcRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {

var bodyReader io.Reader
if body != nil {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
bodyReader = strings.NewReader(string(b))
}
req, err := http.NewRequestWithContext(ctx, method, bridgecrewAPIBase+path, bodyReader)
if err != nil {
	return fmt.Errorf("create request: %w", err)
}
req.Header.Set("Authorization", a.bridgecrewToken)
req.Header.Set("Content-Type", "application/json")
resp, err := a.httpClient.Do(req)
if err != nil {
	return fmt.Errorf("execute request: %w", err)
}
defer resp.Body.Close()
if resp.StatusCode >= 300 {
	b, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("bridgecrew API error %d: %s", resp.StatusCode, string(b))
}
if out != nil {
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
}
return nil
}
