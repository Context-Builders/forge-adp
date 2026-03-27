package policy

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"

	_ "github.com/lib/pq"
	"github.com/open-policy-agent/opa/rego"
)

type Config struct {
	DatabaseURL string
	OPABundle   string
}

type Engine struct {
	db    *sql.DB
	query rego.PreparedEvalQuery
}

type AuthzRequest struct {
	AgentID   string                 `json:"agent_id"`
	Action    string                 `json:"action"`
	Resource  string                 `json:"resource"`
	ProjectID string                 `json:"project_id"`
	Context   map[string]interface{} `json:"context,omitempty"`
}

type AuthzResponse struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason,omitempty"`
}

func NewEngine(cfg Config) (*Engine, error) {
	db, err := sql.Open("postgres", cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	query, err := rego.New(
		rego.Query("data.forge.authz.allow"),
		rego.Load([]string{cfg.OPABundle}, nil),
	).PrepareForEval(ctx)
	if err != nil {
		return nil, fmt.Errorf("prepare OPA query: %w", err)
	}

	return &Engine{db: db, query: query}, nil
}

func (e *Engine) Authorize(ctx context.Context, req AuthzRequest) AuthzResponse {
	var rulesJSON []byte
	e.db.QueryRowContext(ctx,
		`SELECT rules FROM policies WHERE (scope = 'protocol' OR (scope = 'project' AND project_id = $1)) AND enabled = true ORDER BY scope LIMIT 1`,
		req.ProjectID).Scan(&rulesJSON)

	input := map[string]interface{}{
		"agent_id":   req.AgentID,
		"action":     req.Action,
		"resource":   req.Resource,
		"project_id": req.ProjectID,
		"context":    req.Context,
	}

	results, err := e.query.Eval(ctx, rego.EvalInput(input))
	if err != nil || len(results) == 0 {
		return AuthzResponse{Allowed: false, Reason: "policy evaluation failed"}
	}

	if allowed, ok := results[0].Expressions[0].Value.(bool); ok && allowed {
		return AuthzResponse{Allowed: true}
	}

	return AuthzResponse{Allowed: false, Reason: "denied by policy"}
}

func (e *Engine) HandleAuthorize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req AuthzRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	resp := e.Authorize(r.Context(), req)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (e *Engine) HandlePolicies(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
