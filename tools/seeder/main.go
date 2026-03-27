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

// SiblingRepos returns platform repos excluding the current GitHubRepo.
func (c ProjectConfig) SiblingRepos() []PlatformRepo {
	if c.Platform == nil {
		return nil
	}
	var siblings []PlatformRepo
	for _, r := range c.Platform.Repos {
		if r.Repo != c.GitHubRepo {
			siblings = append(siblings, r)
		}
	}
	return siblings
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
