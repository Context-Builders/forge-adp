package registry

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"

	_ "github.com/lib/pq"

	"github.com/dotrage/forge-adp/pkg/llm/catalog"
)

type Config struct {
	DatabaseURL string
	S3Bucket    string
	S3Region    string
}

type Registry struct {
	db       *sql.DB
	s3Bucket string
	s3Region string
}

func New(cfg Config) (*Registry, error) {
	db, err := sql.Open("postgres", cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &Registry{
		db:       db,
		s3Bucket: cfg.S3Bucket,
		s3Region: cfg.S3Region,
	}, nil
}

type Agent struct {
	ID     string   `json:"id"`
	Role   string   `json:"role"`
	Status string   `json:"status"`
	Skills []string `json:"skills"`
}

type Skill struct {
	Role          string  `json:"role"`
	Name          string  `json:"name"`
	Version       string  `json:"version"`
	Description   string  `json:"description"`
	AutonomyLevel int     `json:"autonomy_level"`
	S3Path        string  `json:"s3_path,omitempty"`
}

func (r *Registry) HandleAgents(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		r.listAgents(w, req)
	case http.MethodPost:
		r.registerAgent(w, req)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (r *Registry) listAgents(w http.ResponseWriter, req *http.Request) {
	rows, err := r.db.QueryContext(req.Context(),
		`SELECT id, role, COALESCE(status, 'idle') FROM agents ORDER BY role, id`)
	if err != nil {
		http.Error(w, fmt.Sprintf("query agents: %v", err), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	agents := []Agent{}
	for rows.Next() {
		var a Agent
		if err := rows.Scan(&a.ID, &a.Role, &a.Status); err != nil {
			http.Error(w, fmt.Sprintf("scan agent: %v", err), http.StatusInternalServerError)
			return
		}
		// Populate skills from the skills table for this role.
		skillRows, err := r.db.QueryContext(req.Context(),
			`SELECT skill_name FROM skills WHERE agent_role = $1`, a.Role)
		if err == nil {
			defer skillRows.Close()
			for skillRows.Next() {
				var name string
				if skillRows.Scan(&name) == nil {
					a.Skills = append(a.Skills, name)
				}
			}
		}
		if a.Skills == nil {
			a.Skills = []string{}
		}
		agents = append(agents, a)
	}
	if err := rows.Err(); err != nil {
		http.Error(w, fmt.Sprintf("iterate agents: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(agents)
}

func (r *Registry) registerAgent(w http.ResponseWriter, req *http.Request) {
	var a Agent
	if err := json.NewDecoder(req.Body).Decode(&a); err != nil {
		http.Error(w, fmt.Sprintf("decode body: %v", err), http.StatusBadRequest)
		return
	}
	if a.Role == "" {
		http.Error(w, "role is required", http.StatusBadRequest)
		return
	}
	var id string
	err := r.db.QueryRowContext(req.Context(),
		`INSERT INTO agents (role, instance_id, company_id, project_id, status)
		 VALUES ($1, $2, COALESCE($3, ''), COALESCE($4, ''), 'idle')
		 ON CONFLICT (instance_id) DO UPDATE SET status = 'idle', updated_at = NOW()
		 RETURNING id`,
		a.Role, fmt.Sprintf("%s-%s", a.Role, a.ID), nil, nil,
	).Scan(&id)
	if err != nil {
		http.Error(w, fmt.Sprintf("register agent: %v", err), http.StatusInternalServerError)
		return
	}
	a.ID = id
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(a)
}

func (r *Registry) HandleSkills(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		r.listSkills(w, req)
	case http.MethodPost:
		r.registerSkill(w, req)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (r *Registry) listSkills(w http.ResponseWriter, req *http.Request) {
	role := req.URL.Query().Get("role")

	var (
		rows *sql.Rows
		err  error
	)
	if role != "" {
		rows, err = r.db.QueryContext(req.Context(),
			`SELECT agent_role, skill_name, version, COALESCE(manifest, '{}'), COALESCE(s3_path, '')
			 FROM skills WHERE agent_role = $1 ORDER BY skill_name`, role)
	} else {
		rows, err = r.db.QueryContext(req.Context(),
			`SELECT agent_role, skill_name, version, COALESCE(manifest, '{}'), COALESCE(s3_path, '')
			 FROM skills ORDER BY agent_role, skill_name`)
	}
	if err != nil {
		http.Error(w, fmt.Sprintf("query skills: %v", err), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	skills := []Skill{}
	for rows.Next() {
		var s Skill
		var manifestJSON []byte
		if err := rows.Scan(&s.Role, &s.Name, &s.Version, &manifestJSON, &s.S3Path); err != nil {
			http.Error(w, fmt.Sprintf("scan skill: %v", err), http.StatusInternalServerError)
			return
		}
		var manifest struct {
			Description   string `json:"description"`
			AutonomyLevel int    `json:"autonomy_level"`
		}
		if len(manifestJSON) > 0 {
			json.Unmarshal(manifestJSON, &manifest)
		}
		s.Description = manifest.Description
		s.AutonomyLevel = manifest.AutonomyLevel
		skills = append(skills, s)
	}
	if err := rows.Err(); err != nil {
		http.Error(w, fmt.Sprintf("iterate skills: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(skills)
}

func (r *Registry) HandleGetSkill(w http.ResponseWriter, req *http.Request) {
	role := req.PathValue("role")
	name := req.PathValue("name")
	if role == "" || name == "" {
		http.Error(w, "role and name are required", http.StatusBadRequest)
		return
	}

	var s Skill
	var manifestJSON []byte
	err := r.db.QueryRowContext(req.Context(),
		`SELECT agent_role, skill_name, version, COALESCE(manifest, '{}'), COALESCE(s3_path, '')
		 FROM skills WHERE agent_role = $1 AND skill_name = $2`,
		role, name,
	).Scan(&s.Role, &s.Name, &s.Version, &manifestJSON, &s.S3Path)
	if err == sql.ErrNoRows {
		http.Error(w, "skill not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, fmt.Sprintf("query skill: %v", err), http.StatusInternalServerError)
		return
	}
	var manifest struct {
		Description   string `json:"description"`
		AutonomyLevel int    `json:"autonomy_level"`
	}
	if len(manifestJSON) > 0 {
		json.Unmarshal(manifestJSON, &manifest)
	}
	s.Description = manifest.Description
	s.AutonomyLevel = manifest.AutonomyLevel

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s)
}

func (r *Registry) registerSkill(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Role    string          `json:"role"`
		Name    string          `json:"name"`
		Version string          `json:"version"`
		Manifest json.RawMessage `json:"manifest"`
		S3Path  string          `json:"s3_path"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(w, fmt.Sprintf("decode body: %v", err), http.StatusBadRequest)
		return
	}
	if body.Role == "" || body.Name == "" || body.Version == "" {
		http.Error(w, "role, name, and version are required", http.StatusBadRequest)
		return
	}
	if len(body.Manifest) == 0 {
		body.Manifest = json.RawMessage("{}")
	}
	_, err := r.db.ExecContext(req.Context(),
		`INSERT INTO skills (agent_role, skill_name, version, manifest, s3_path)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (agent_role, skill_name, version) DO UPDATE
		   SET manifest = EXCLUDED.manifest, s3_path = EXCLUDED.s3_path`,
		body.Role, body.Name, body.Version, body.Manifest, body.S3Path,
	)
	if err != nil {
		http.Error(w, fmt.Sprintf("register skill: %v", err), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (r *Registry) HandleLLMProviders(w http.ResponseWriter, req *http.Request) {
	providers := catalog.KnownProviders()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(providers); err != nil {
		http.Error(w, fmt.Sprintf("encode response: %v", err), http.StatusInternalServerError)
	}
}
