package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

//go:embed templates/*
var templates embed.FS

type ProjectConfig struct {
	ProjectName    string
	CompanyID      string
	ProjectID      string
	JiraProjectKey string
	GitHubRepo     string
	SlackChannel   string
	TechStack      TechStack
	Agents         []string
	Platform       *PlatformConfig
	Profile        *Profile
}

type TechStack struct {
	Frontend       string
	Backend        string
	Database       string
	Infrastructure string
	CICD           string
}

// PlatformConfig represents a multi-repo platform where several repositories
// (API, workers, UI, etc.) collaborate as a single product.
type PlatformConfig struct {
	ID    string
	Repos []PlatformRepo
}

type PlatformRepo struct {
	Repo      string
	Role      string
	LocalPath string
}

// Profile holds org-level standards for a named project type.
// When a profile is selected via --profile, the seeder stamps these
// standards into DB_CONVENTIONS.md, QA_STANDARDS.md, and OBSERVABILITY.md
// so agents follow consistent conventions across all projects.
type Profile struct {
	Name          string
	Description   string
	QA            QAStack
	Observability ObservabilityStack
	DB            DBConventions
}

// QAStack defines the testing and linting toolchain for a profile.
type QAStack struct {
	UnitFrontend      string
	UnitBackend       string
	Integration       string
	E2E               string
	CoverageThreshold int
	LintFrontend      string
	LintBackend       string
}

// ObservabilityStack defines the monitoring and alerting toolchain.
type ObservabilityStack struct {
	APM          string
	Logs         string
	Metrics      string
	Synthetics   string
	MetricNaming string
	SLOEnabled   bool
}

// DBConventions defines org-level rules for database schema design.
type DBConventions struct {
	PrimaryKey   string
	SoftDeletes  bool
	AuditColumns []string
	Naming       string
	Migrations   string
	IndexNaming  string
	EnumStrategy string
}

// builtinProfiles are the named profiles available via --profile.
// Each profile pre-fills QA, observability, and DB standards so agents
// produce consistent architecture across projects of the same type.
var builtinProfiles = map[string]Profile{
	"web-fullstack": {
		Name:        "web-fullstack",
		Description: "Full-stack web application (Next.js + Go + PostgreSQL)",
		QA: QAStack{
			UnitFrontend:      "Jest + React Testing Library",
			UnitBackend:       "Go test + testify",
			Integration:       "Testcontainers",
			E2E:               "Playwright",
			CoverageThreshold: 80,
			LintFrontend:      "ESLint + Prettier",
			LintBackend:       "golangci-lint",
		},
		Observability: ObservabilityStack{
			APM:          "Datadog APM",
			Logs:         "Datadog Logs (structured JSON via zerolog)",
			Metrics:      "Datadog Metrics (StatsD)",
			Synthetics:   "Datadog Synthetics",
			MetricNaming: "service.operation.result",
			SLOEnabled:   true,
		},
		DB: DBConventions{
			PrimaryKey:   "UUID v7 (github.com/google/uuid)",
			SoftDeletes:  true,
			AuditColumns: []string{"id", "created_at", "updated_at", "deleted_at"},
			Naming:       "snake_case — tables plural, columns snake_case",
			Migrations:   "golang-migrate (migrate/v4, SQL files)",
			IndexNaming:  "idx_{table}_{columns}",
			EnumStrategy: "PostgreSQL native enum types",
		},
	},
	"api-service": {
		Name:        "api-service",
		Description: "Backend API service (Go + PostgreSQL), no frontend",
		QA: QAStack{
			UnitBackend:       "Go test + testify",
			Integration:       "Testcontainers",
			E2E:               "k6 (load and smoke tests)",
			CoverageThreshold: 80,
			LintBackend:       "golangci-lint",
		},
		Observability: ObservabilityStack{
			APM:          "Datadog APM",
			Logs:         "Datadog Logs (structured JSON via zerolog)",
			Metrics:      "Datadog Metrics (StatsD)",
			MetricNaming: "service.operation.result",
			SLOEnabled:   true,
		},
		DB: DBConventions{
			PrimaryKey:   "UUID v7 (github.com/google/uuid)",
			SoftDeletes:  true,
			AuditColumns: []string{"id", "created_at", "updated_at", "deleted_at"},
			Naming:       "snake_case — tables plural, columns snake_case",
			Migrations:   "golang-migrate (migrate/v4, SQL files)",
			IndexNaming:  "idx_{table}_{columns}",
			EnumStrategy: "PostgreSQL native enum types",
		},
	},
	"data-pipeline": {
		Name:        "data-pipeline",
		Description: "Data processing pipeline (Python + PostgreSQL + async workers)",
		QA: QAStack{
			UnitBackend:       "pytest + pytest-asyncio",
			Integration:       "Testcontainers (pytest-testcontainers)",
			E2E:               "pytest (end-to-end pipeline tests with real data fixtures)",
			CoverageThreshold: 75,
			LintBackend:       "ruff + mypy",
		},
		Observability: ObservabilityStack{
			APM:          "Datadog APM (ddtrace)",
			Logs:         "Datadog Logs (structlog, JSON format)",
			Metrics:      "Datadog Metrics (DogStatsD)",
			MetricNaming: "pipeline.stage.result",
			SLOEnabled:   false,
		},
		DB: DBConventions{
			PrimaryKey:   "UUID v7 (python-uuid-utils)",
			SoftDeletes:  false,
			AuditColumns: []string{"id", "created_at", "updated_at"},
			Naming:       "snake_case — tables plural, columns snake_case",
			Migrations:   "Alembic (auto-generated migrations)",
			IndexNaming:  "idx_{table}_{columns}",
			EnumStrategy: "Python Enum classes (stored as VARCHAR)",
		},
	},
}

// identifier returns a human-readable label for the repo (path or org/repo).
func (pr PlatformRepo) identifier() string {
	if pr.LocalPath != "" {
		return pr.LocalPath
	}
	return pr.Repo
}

// HasPlatform returns true if the project is part of a multi-repo platform.
func (c ProjectConfig) HasPlatform() bool {
	return c.Platform != nil && len(c.Platform.Repos) > 0
}

// HasProfile returns true if a standards profile was selected.
func (c ProjectConfig) HasProfile() bool {
	return c.Profile != nil
}

func main() {
	var config ProjectConfig

	flag.StringVar(&config.ProjectName, "name", "", "Project name")
	flag.StringVar(&config.CompanyID, "company", "", "Company ID")
	flag.StringVar(&config.ProjectID, "project", "", "Project ID")
	flag.StringVar(&config.JiraProjectKey, "jira-key", "", "Jira project key")
	flag.StringVar(&config.GitHubRepo, "github-repo", "", "GitHub repository (org/repo)")
	flag.StringVar(&config.SlackChannel, "slack-channel", "", "Slack channel name")

	// Tech stack (used for single-repo mode or as defaults)
	flag.StringVar(&config.TechStack.Frontend, "frontend", "Next.js 14, TypeScript", "Frontend stack")
	flag.StringVar(&config.TechStack.Backend, "backend", "Go 1.22", "Backend stack")
	flag.StringVar(&config.TechStack.Database, "database", "PostgreSQL 16", "Database")
	flag.StringVar(&config.TechStack.Infrastructure, "infra", "AWS, Terraform", "Infrastructure")
	flag.StringVar(&config.TechStack.CICD, "cicd", "GitHub Actions", "CI/CD")

	// Agents
	var agents string
	flag.StringVar(&agents, "agents", "pm,architect,backend-developer,frontend-developer,qa,secops", "Comma-separated agent roles")

	// Profile: selects a named set of org standards (QA tools, DB conventions, observability stack).
	// Available profiles: web-fullstack, api-service, data-pipeline
	var profileName string
	flag.StringVar(&profileName, "profile", "", "Org standards profile (web-fullstack, api-service, data-pipeline)")

	// Platform mode: seed multiple repos/directories as a single platform.
	// Use -platform-repos for GitHub/GitLab repos (org/repo format).
	// Use -platform-sources for local directories on disk.
	// Both can be used together to mix remote and local sources.
	platformID := flag.String("platform-id", "", "Platform ID for multi-repo projects (e.g. acme-payments)")
	platformRepos := flag.String("platform-repos", "", "Comma-separated GitHub/GitLab repo definitions: org/repo:role (e.g. acme/api:api,acme/workers:workers,acme/ui:ui)")
	platformSources := flag.String("platform-sources", "", "Comma-separated local directory definitions: path:role (e.g. ./services/api:api,./services/workers:workers,./services/ui:ui)")

	targetDir := flag.String("output", ".", "Output directory")

	flag.Parse()

	if config.ProjectName == "" {
		fmt.Println("Error: -name is required")
		os.Exit(1)
	}

	config.Agents = strings.Split(agents, ",")

	// Resolve profile
	if profileName != "" {
		p, ok := builtinProfiles[profileName]
		if !ok {
			var names []string
			for k := range builtinProfiles {
				names = append(names, k)
			}
			fmt.Printf("Error: unknown profile %q. Available profiles: %s\n", profileName, strings.Join(names, ", "))
			os.Exit(1)
		}
		config.Profile = &p
	}

	// Parse platform configuration if provided
	hasPlatform := *platformID != "" && (*platformRepos != "" || *platformSources != "")
	if hasPlatform {
		platform := &PlatformConfig{ID: *platformID}

		// Parse remote repos (GitHub/GitLab)
		if *platformRepos != "" {
			for _, entry := range strings.Split(*platformRepos, ",") {
				parts := strings.Split(entry, ":")
				if len(parts) < 2 {
					fmt.Printf("Error: invalid platform repo entry: %s (expected org/repo:role)\n", entry)
					os.Exit(1)
				}
				platform.Repos = append(platform.Repos, PlatformRepo{
					Repo: parts[0],
					Role: parts[1],
				})
			}
		}

		// Parse local directories
		if *platformSources != "" {
			for _, entry := range strings.Split(*platformSources, ",") {
				parts := strings.Split(entry, ":")
				if len(parts) < 2 {
					fmt.Printf("Error: invalid platform source entry: %s (expected path:role)\n", entry)
					os.Exit(1)
				}
				absPath, err := filepath.Abs(parts[0])
				if err != nil {
					fmt.Printf("Error resolving path %s: %v\n", parts[0], err)
					os.Exit(1)
				}
				platform.Repos = append(platform.Repos, PlatformRepo{
					LocalPath: absPath,
					Role:      parts[1],
				})
			}
		}
		config.Platform = platform

		// Seed each repo in the platform.
		// Each repo uses the top-level tech stack flags as defaults;
		// the real per-repo stack lives in each repo's own .forge/config.yaml.
		for _, pr := range platform.Repos {
			repoConfig := config
			repoConfig.GitHubRepo = pr.Repo
			repoConfig.Platform = platform

			// For local paths, seed directly into the local directory.
			// For GitHub repos, create a subdirectory under the output dir.
			var repoDir string
			if pr.LocalPath != "" {
				repoDir = pr.LocalPath
			} else {
				repoDir = filepath.Join(*targetDir, filepath.Base(pr.Repo))
			}

			if err := seedProject(repoDir, repoConfig); err != nil {
				fmt.Printf("Error seeding %s: %v\n", pr.identifier(), err)
				os.Exit(1)
			}
			fmt.Printf("✅ Seeded Forge project in %s/.forge/ (role: %s)\n", repoDir, pr.Role)
		}
	} else {
		// Single-repo mode
		if err := seedProject(*targetDir, config); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✅ Seeded Forge project in %s/.forge/\n", *targetDir)
	}

	if config.Profile != nil {
		fmt.Printf("📋 Applied profile: %s (%s)\n", config.Profile.Name, config.Profile.Description)
	}
}

func seedProject(targetDir string, config ProjectConfig) error {
	forgeDir := filepath.Join(targetDir, ".forge")
	if err := os.MkdirAll(forgeDir, 0755); err != nil {
		return fmt.Errorf("create .forge directory: %w", err)
	}

	return fs.WalkDir(templates, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		// Read template
		content, err := templates.ReadFile(path)
		if err != nil {
			return err
		}

		// Parse and execute template
		tmpl, err := template.New(d.Name()).Parse(string(content))
		if err != nil {
			return err
		}

		// Determine output filename (remove .tmpl extension if present)
		outName := strings.TrimPrefix(path, "templates/")
		outName = strings.TrimSuffix(outName, ".tmpl")
		outPath := filepath.Join(forgeDir, outName)

		// Create parent dirs if needed
		if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
			return err
		}

		// Create output file
		outFile, err := os.Create(outPath)
		if err != nil {
			return err
		}
		defer outFile.Close()

		return tmpl.Execute(outFile, config)
	})
}
