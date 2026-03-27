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

const vaultAPIBase = "/v1"

type VaultAdapter struct {
	baseURL    string
	token      string
	namespace  string
	bus        events.Bus
	httpClient *http.Client
}

type vaultAuditLog struct {
	Type    string `json:"type"`
	Time    string `json:"time"`
	Auth    struct {
		ClientToken string `json:"client_token"`
		Accessor    string `json:"accessor"`
		Policies    []string `json:"policies"`
		DisplayName string `json:"display_name"`
	} `json:"auth"`
Request struct {
	ID        string `json:"id"`
	Operation string `json:"operation"`
	Path      string `json:"path"`
} `json:"request"`
Error string `json:"error"`
}

func main() {
	baseURL := os.Getenv("VAULT_ADDR")
	token := os.Getenv("VAULT_TOKEN")
	if baseURL == "" || token == "" {
		log.Fatal("VAULT_ADDR and VAULT_TOKEN are required")
	}
bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
if err != nil {
	log.Fatalf("failed to create event bus: %v", err)
}
adapter := &VaultAdapter{
	baseURL:    strings.TrimRight(baseURL, "/"),
	token:      token,
	namespace:  os.Getenv("VAULT_NAMESPACE"),
	bus:        bus,
	httpClient: &http.Client{},
}
go adapter.subscribeToEvents()
mux := http.NewServeMux()
mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})
mux.HandleFunc("/webhook/audit", adapter.HandleAuditWebhook)
mux.HandleFunc("/api/v1/secrets", adapter.HandleSecrets)
mux.HandleFunc("/api/v1/lease/renew", adapter.HandleLeaseRenew)
mux.HandleFunc("/api/v1/lease/revoke", adapter.HandleLeaseRevoke)
log.Printf("HashiCorp Vault adapter listening on :19121")
http.ListenAndServe(":19121", mux)
}

func (a *VaultAdapter) HandleAuditWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

var log_ vaultAuditLog
if err := json.NewDecoder(r.Body).Decode(&log_); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}
// Surface denied operations as escalations
if log_.Error != "" {
	ep, _ := json.Marshal(map[string]interface{}{
		"request_id": log_.Request.ID,
		"operation":  log_.Request.Operation,
		"path":       log_.Request.Path,
		"error":      log_.Error,
		"source":     "vault",
})
if err := a.bus.Publish(r.Context(), events.Event{Type: events.EscalationCreated, Payload: ep}); err != nil {
	log.Printf("failed to publish escalation event: %v", err)
}
}
w.WriteHeader(http.StatusOK)
}

func (a *VaultAdapter) HandleSecrets(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "path query parameter is required", http.StatusBadRequest)
		return
	}
switch r.Method {
	case http.MethodGet:

var result map[string]interface{}
if err := a.vaultRequest(r.Context(), http.MethodGet, "/secret/data/"+path, nil, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
default:
http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
}

func (a *VaultAdapter) HandleLeaseRenew(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

var req map[string]interface{}
if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}

var result map[string]interface{}
if err := a.vaultRequest(r.Context(), http.MethodPut, "/sys/leases/renew", req, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
}

func (a *VaultAdapter) HandleLeaseRevoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

var req map[string]interface{}
if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
	http.Error(w, err.Error(), http.StatusBadRequest)
	return
}

var result map[string]interface{}
if err := a.vaultRequest(r.Context(), http.MethodPut, "/sys/leases/revoke", req, &result); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(result)
}

func (a *VaultAdapter) subscribeToEvents() {
	ctx := context.Background()
	a.bus.Subscribe(ctx, []events.EventType{events.TaskCreated}, func(e events.Event) error {
		return nil
})
}

func (a *VaultAdapter) vaultRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {

var bodyReader io.Reader
if body != nil {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
bodyReader = strings.NewReader(string(b))
}
req, err := http.NewRequestWithContext(ctx, method, a.baseURL+vaultAPIBase+path, bodyReader)
if err != nil {
	return fmt.Errorf("create request: %w", err)
}
req.Header.Set("X-Vault-Token", a.token)
if a.namespace != "" {
	req.Header.Set("X-Vault-Namespace", a.namespace)
}
req.Header.Set("Content-Type", "application/json")
resp, err := a.httpClient.Do(req)
if err != nil {
	return fmt.Errorf("execute request: %w", err)
}
defer resp.Body.Close()
if resp.StatusCode >= 300 {
	b, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("vault API error %d: %s", resp.StatusCode, string(b))
}
if out != nil {
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
}
return nil
}
