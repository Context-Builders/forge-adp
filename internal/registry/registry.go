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

func (r *Registry) HandleAgents(w http.ResponseWriter, req *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

func (r *Registry) HandleSkills(w http.ResponseWriter, req *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

func (r *Registry) HandleLLMProviders(w http.ResponseWriter, req *http.Request) {
	providers := catalog.KnownProviders()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(providers); err != nil {
		http.Error(w, fmt.Sprintf("encode response: %v", err), http.StatusInternalServerError)
	}
}
