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
	switch r.Method {
	case http.MethodGet:
		e.listPolicies(w, r)
	case http.MethodPost:
		e.createPolicy(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (e *Engine) HandlePolicy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	switch r.Method {
	case http.MethodGet:
		e.getPolicy(w, r, id)
	case http.MethodPut:
		e.updatePolicy(w, r, id)
	case http.MethodDelete:
		e.deletePolicy(w, r, id)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

type Policy struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Scope     string          `json:"scope"`
	CompanyID string          `json:"company_id,omitempty"`
	ProjectID string          `json:"project_id,omitempty"`
	Rules     json.RawMessage `json:"rules"`
	Enabled   bool            `json:"enabled"`
	CreatedAt string          `json:"created_at,omitempty"`
	UpdatedAt string          `json:"updated_at,omitempty"`
}

func (e *Engine) listPolicies(w http.ResponseWriter, r *http.Request) {
	scope := r.URL.Query().Get("scope")
	projectID := r.URL.Query().Get("project_id")

	query := `SELECT id, name, scope, COALESCE(company_id,''), COALESCE(project_id,''),
	                 rules, enabled, created_at, updated_at
	          FROM policies WHERE 1=1`
	args := []interface{}{}
	i := 1
	if scope != "" {
		query += fmt.Sprintf(" AND scope = $%d", i)
		args = append(args, scope)
		i++
	}
	if projectID != "" {
		query += fmt.Sprintf(" AND project_id = $%d", i)
		args = append(args, projectID)
	}
	query += " ORDER BY scope, name"

	rows, err := e.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		http.Error(w, fmt.Sprintf("query policies: %v", err), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	policies := []Policy{}
	for rows.Next() {
		var p Policy
		var createdAt, updatedAt string
		if err := rows.Scan(&p.ID, &p.Name, &p.Scope, &p.CompanyID, &p.ProjectID,
			&p.Rules, &p.Enabled, &createdAt, &updatedAt); err != nil {
			http.Error(w, fmt.Sprintf("scan policy: %v", err), http.StatusInternalServerError)
			return
		}
		p.CreatedAt = createdAt
		p.UpdatedAt = updatedAt
		policies = append(policies, p)
	}
	if err := rows.Err(); err != nil {
		http.Error(w, fmt.Sprintf("iterate policies: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(policies)
}

func (e *Engine) createPolicy(w http.ResponseWriter, r *http.Request) {
	var p Policy
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, fmt.Sprintf("decode body: %v", err), http.StatusBadRequest)
		return
	}
	if p.Name == "" || p.Scope == "" {
		http.Error(w, "name and scope are required", http.StatusBadRequest)
		return
	}
	if len(p.Rules) == 0 {
		p.Rules = json.RawMessage("{}")
	}

	var id string
	err := e.db.QueryRowContext(r.Context(),
		`INSERT INTO policies (name, scope, company_id, project_id, rules, enabled)
		 VALUES ($1, $2, NULLIF($3,''), NULLIF($4,''), $5, $6)
		 ON CONFLICT (name) DO UPDATE
		   SET scope=$2, company_id=NULLIF($3,''), project_id=NULLIF($4,''),
		       rules=$5, enabled=$6, updated_at=NOW()
		 RETURNING id`,
		p.Name, p.Scope, p.CompanyID, p.ProjectID, p.Rules, p.Enabled,
	).Scan(&id)
	if err != nil {
		http.Error(w, fmt.Sprintf("create policy: %v", err), http.StatusInternalServerError)
		return
	}
	p.ID = id
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(p)
}

func (e *Engine) getPolicy(w http.ResponseWriter, r *http.Request, id string) {
	var p Policy
	var createdAt, updatedAt string
	err := e.db.QueryRowContext(r.Context(),
		`SELECT id, name, scope, COALESCE(company_id,''), COALESCE(project_id,''),
		        rules, enabled, created_at, updated_at
		 FROM policies WHERE id = $1`, id).
		Scan(&p.ID, &p.Name, &p.Scope, &p.CompanyID, &p.ProjectID,
			&p.Rules, &p.Enabled, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		http.Error(w, "policy not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, fmt.Sprintf("get policy: %v", err), http.StatusInternalServerError)
		return
	}
	p.CreatedAt = createdAt
	p.UpdatedAt = updatedAt
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(p)
}

func (e *Engine) updatePolicy(w http.ResponseWriter, r *http.Request, id string) {
	var p Policy
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, fmt.Sprintf("decode body: %v", err), http.StatusBadRequest)
		return
	}
	res, err := e.db.ExecContext(r.Context(),
		`UPDATE policies SET name=COALESCE(NULLIF($2,''),name),
		        scope=COALESCE(NULLIF($3,''),scope), rules=COALESCE($4,rules),
		        enabled=$5, updated_at=NOW()
		 WHERE id = $1`,
		id, p.Name, p.Scope, p.Rules, p.Enabled)
	if err != nil {
		http.Error(w, fmt.Sprintf("update policy: %v", err), http.StatusInternalServerError)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		http.Error(w, "policy not found", http.StatusNotFound)
		return
	}
	e.getPolicy(w, r, id)
}

func (e *Engine) deletePolicy(w http.ResponseWriter, r *http.Request, id string) {
	res, err := e.db.ExecContext(r.Context(), `DELETE FROM policies WHERE id = $1`, id)
	if err != nil {
		http.Error(w, fmt.Sprintf("delete policy: %v", err), http.StatusInternalServerError)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		http.Error(w, "policy not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
