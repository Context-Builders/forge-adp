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
	"strings"

	"github.com/dotrage/forge-adp/pkg/events"
	"github.com/dotrage/forge-adp/pkg/logger"
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
	Type string `json:"type"`
	Time string `json:"time"`
	Auth struct {
		ClientToken string   `json:"client_token"`
		Accessor    string   `json:"accessor"`
		Policies    []string `json:"policies"`
		DisplayName string   `json:"display_name"`
	} `json:"auth"`
	Request struct {
		ID        string `json:"id"`
		Operation string `json:"operation"`
		Path      string `json:"path"`
	} `json:"request"`
	Error string `json:"error"`
}

func main() {
	logger.Init("vault-adapter")

	baseURL := os.Getenv("VAULT_ADDR")
	token := os.Getenv("VAULT_TOKEN")
	if baseURL == "" || token == "" {
		slog.Error("VAULT_ADDR and VAULT_TOKEN are required")
		os.Exit(1)
	}

	bus, err := events.NewRedisBus(os.Getenv("REDIS_ADDR"), "forge:events")
	if err != nil {
		slog.Error("failed to create event bus", slog.Any("error", err))
		os.Exit(1)
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

	slog.Info("HashiCorp Vault adapter listening", slog.String("addr", ":19121"))
	http.ListenAndServe(":19121", logger.HTTPMiddleware("vault-adapter", mux))
}

func (a *VaultAdapter) HandleAuditWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var auditLog vaultAuditLog
	if err := json.NewDecoder(r.Body).Decode(&auditLog); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Surface denied operations as escalations.
	if auditLog.Error != "" {
		ep, _ := json.Marshal(map[string]interface{}{
			"request_id": auditLog.Request.ID,
			"operation":  auditLog.Request.Operation,
			"path":       auditLog.Request.Path,
			"error":      auditLog.Error,
			"source":     "vault",
		})
		if err := a.bus.Publish(r.Context(), events.Event{Type: events.EscalationCreated, Payload: ep}); err != nil {
			slog.Error("failed to publish escalation event",
				slog.String("request_id", auditLog.Request.ID),
				slog.Any("error", err))
		}
	}
	w.WriteHeader(http.StatusOK)
}

// HandleSecrets reads a KV v2 secret.
//
//	GET /api/v1/secrets?path=<path>
func (a *VaultAdapter) HandleSecrets(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "path query parameter is required", http.StatusBadRequest)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var result map[string]interface{}
	if err := a.vaultRequest(r.Context(), http.MethodGet, "/secret/data/"+path, nil, &result); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// HandleLeaseRenew renews a Vault lease.
//
//	PUT /api/v1/lease/renew
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

// HandleLeaseRevoke revokes a Vault lease.
//
//	PUT /api/v1/lease/revoke
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

// subscribeToEvents listens for EscalationCreated events. Vault audit denials
// already publish escalations inbound; this allows correlating outbound escalations.
func (a *VaultAdapter) subscribeToEvents() {
	ctx := context.Background()
	if err := a.bus.Subscribe(ctx, []events.EventType{events.EscalationCreated}, func(e events.Event) error {
		var payload struct {
			Source string `json:"source"`
			Path   string `json:"path"`
		}
		json.Unmarshal(e.Payload, &payload)
		if payload.Source == "vault" {
			return nil // avoid loops
		}
		slog.Info("escalation received",
			slog.String("task_id", e.TaskID))
		return nil
	}); err != nil {
		slog.Error("failed to subscribe to escalation events", slog.Any("error", err))
	}
}

func (a *VaultAdapter) vaultRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(b)
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
