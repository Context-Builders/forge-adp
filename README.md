# Forge Agent Development Protocol

[![Build Status](https://img.shields.io/github/actions/workflow/status/dotrage/forge/ci.yml?branch=main&style=flat-square)](https://github.com/dotrage/forge/actions)
[![Go Version](https://img.shields.io/badge/go-1.22-00ADD8?style=flat-square&logo=go)](https://go.dev/)
[![Python Version](https://img.shields.io/badge/python-3.12-3776AB?style=flat-square&logo=python)](https://www.python.org/)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue?style=flat-square)](LICENSE)

> **An enterprise agent orchestration protocol that transforms product requirements into shipped software.**

Forge ADP connects product owners to a fleet of specialized AI agents — each responsible for a distinct software development function — through a structured workflow of planning, ticketing, execution, review, and deployment. It is designed for private deployment within any company, is not tied to a single product or tech stack, and puts human oversight at the center of every consequential action.

---

## Table of Contents

- [Overview](#overview)
- [Architecture](#architecture)
- [Prerequisites](#prerequisites)
- [Installation](#installation)
- [Usage](#usage)
  - [Triggering via Jira](#triggering-agent-work)
  - [Triggering via Slack](#triggering-via-slack)
  - [MCP Server — Claude Code](#using-the-mcp-server--claude-code)
  - [VS Code Extension](#using-the-vs-code-extension)
- [Configuration](#configuration)
- [API & Command Reference](#api--command-reference)
- [Contributing](#contributing)
- [License](#license)

---

## Overview

Forge is built around four core principles:

- **Ticket-Driven Execution** — Every unit of agent work traces back to a Jira ticket. No ticket, no work.
- **Plan-Aware Context** — Agents read structured plan documents in each repository to understand the product, architecture, and constraints before acting.
- **Skills Over Prompts** — Each agent type loads a versioned, testable skill package rather than relying on ad hoc prompting.
- **Human-in-the-Loop by Default** — Agents propose. Humans approve. Every PR, schema migration, and deployment requires explicit human sign-off unless a team opts into progressive autonomy for specific low-risk action classes.

### Agent Roster

| Agent | Primary Function |
|---|---|
| **PM** | Decompose requirements into structured Jira tickets, bootstrap project plan documents |
| **Architect** | Analyse requirements, design system architecture, data models, and API contracts |
| **Frontend Developer** | Implement UI features from UX specs and API contracts |
| **Backend Developer** | Implement APIs, business logic, and service integrations |
| **DBA** | Design schemas, generate migrations, optimize queries |
| **DevOps** | Manage IaC, CI/CD pipelines, and deployment configs |
| **SRE** | Monitor health, define SLOs, manage incident response |
| **SecOps** | Security reviews, vulnerability assessments, compliance checks |
| **QA** | Test planning, test execution, and release validation |
| **Data Science** | Data pipelines, ML models, analytics, A/B tests |

---

## Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                        FORGE CONTROL PLANE                          │
│   ┌──────────────┐  ┌──────────────┐  ┌──────────────────────────┐  │
│   │  Orchestrator │  │   Registry   │  │     Policy Engine         │  │
│   │    Engine     │  │   Service    │  │  (permissions, autonomy,  │  │
│   └──────┬───────┘  └──────┬───────┘  │   approval workflows)    │  │
│          │                 │           └───────────┬──────────────┘  │
│   ┌──────┴─────────────────┴──────────────────────┴───────────────┐  │
│   │                    MESSAGE BUS (Event Backbone)                │  │
│   └──────┬──────────┬──────────┬──────────────────────────────────┘  │
└──────────┼──────────┼──────────┼──────────────────────────────────────┘
           │          │          │
    ┌──────┴───┐ ┌───┴────┐ ┌──┴──────┐    ...
    │  Agent   │ │ Agent  │ │  Agent  │
    │ Runtime  │ │Runtime │ │ Runtime │
    └──────────┘ └────────┘ └─────────┘
           │          │          │
    ┌──────┴──────────┴──────────┴─────────────────────────────────┐
    │                    INTEGRATION LAYER                          │
    │  Jira · GitHub · GitLab · Slack · Teams · Google Chat        │
    │  Confluence · Linear · PagerDuty · Opsgenie                  │
    │  Datadog · Grafana · Snyk · SonarQube                        │
    │  Terraform Cloud · Atlantis · MongoDB Atlas · Databricks     │
    │  Vercel · Bitbucket · Azure DevOps · Jenkins · CircleCI      │
    │  ArgoCD · GitHub Actions · GitLab CI · Sentry · New Relic   │
    │  Splunk · AWS · Azure Monitor · GCP · HashiCorp Vault        │
    │  Wiz · Prisma Cloud · Checkov · Trivy · Shortcut · Notion   │
    │  LaunchDarkly · Split.io · Backstage · TestRail · Xray       │
    │  BrowserStack · Sauce Labs · Zephyr Scale · Postman          │
    │  Newman · Cypress Cloud · k6 Cloud · Applitools              │
    └──────────────────────────────────────────────────────────────┘
```

**Control Plane** — Written in Go. Three services: Orchestrator (task routing and sequencing), Registry (agent/skill/plan catalog), Policy Engine (permissions and approval workflows).

**Agent Runtimes** — Python (3.12). Each agent runs in an isolated container with scoped LLM access, a skill loader, a plan reader, and tool access governed by the Policy Engine.

**Message Bus** — Redis Streams (default) or Apache Kafka / NATS for larger deployments.

**Storage** — PostgreSQL 16 for all persistent state (tasks, audit log, agent memory, LLM cost tracking). S3-compatible object store (MinIO locally) for skill packages and artifacts.

---

## Prerequisites

### Tools

```bash
# Go (Control Plane)
brew install go@1.22

# Python (Agent Runtimes)
brew install python@3.12
pip install poetry

# Node.js (MCP Server + VS Code Extension)
brew install node@20   # requires Node ≥ 18

# Containers and orchestration
brew install docker
brew install kubectl helm k9s

# Infrastructure
brew install terraform

# Database
brew install postgresql@16

# Message queue (local dev)
brew install redis

# Database migrations
go install github.com/golang-migrate/migrate/v4/cmd/migrate@latest

# Code quality
brew install golangci-lint
pip install ruff black mypy
```

### API Keys and Credentials

| Service | Purpose | Environment Variable |
|---|---|---|
| Anthropic (Claude) | LLM provider | `ANTHROPIC_API_KEY` |
| GitHub | Source control | `GITHUB_TOKEN`, `GITHUB_WEBHOOK_SECRET` |
| GitLab | Source control (self-hosted or GitLab.com) | `GITLAB_TOKEN`, `GITLAB_BASE_URL`, `GITLAB_WEBHOOK_SECRET` |
| Jira (Atlassian) | Project management | `JIRA_API_TOKEN`, `JIRA_BASE_URL`, `JIRA_USER_EMAIL` |
| Confluence (Atlassian) | Documentation & specs | `CONFLUENCE_BASE_URL`, `CONFLUENCE_USERNAME`, `CONFLUENCE_API_TOKEN` |
| Linear | Issue tracking | `LINEAR_API_KEY`, `LINEAR_WEBHOOK_SECRET` |
| Slack | Team communication | `SLACK_BOT_TOKEN`, `SLACK_APP_TOKEN` |
| Microsoft Teams | Team communication | `TEAMS_WEBHOOK_URL`, `TEAMS_HMAC_SECRET` |
| Google Chat | Team communication | `GOOGLE_CHAT_WEBHOOK_URL`, `GOOGLE_CHAT_VERIFICATION_TOKEN` |
| PagerDuty | Incident management | `PAGERDUTY_API_KEY`, `PAGERDUTY_SERVICE_ID`, `PAGERDUTY_FROM_EMAIL` |
| Opsgenie | Alert management | `OPSGENIE_API_KEY` |
| Datadog | Metrics & monitoring | `DATADOG_API_KEY`, `DATADOG_APP_KEY` |
| Grafana | Dashboards & alerting | `GRAFANA_URL`, `GRAFANA_API_KEY` |
| Snyk | Vulnerability scanning | `SNYK_API_TOKEN`, `SNYK_ORG_ID`, `SNYK_WEBHOOK_SECRET` |
| SonarQube | Static code analysis | `SONARQUBE_URL`, `SONARQUBE_TOKEN`, `SONARQUBE_WEBHOOK_SECRET` |
| Terraform Cloud | IaC plan & apply automation | `TFC_TOKEN`, `TFC_ORGANIZATION`, `TFC_WEBHOOK_HMAC_KEY` |
| Atlantis | GitOps Terraform automation | `ATLANTIS_URL`, `ATLANTIS_TOKEN`, `ATLANTIS_WEBHOOK_SECRET` |
| MongoDB Atlas | Database monitoring & alerts | `MONGODB_ATLAS_PUBLIC_KEY`, `MONGODB_ATLAS_PRIVATE_KEY`, `MONGODB_ATLAS_PROJECT_ID`, `MONGODB_ATLAS_WEBHOOK_SECRET` |
| Databricks | Unified data & AI platform | `DATABRICKS_HOST`, `DATABRICKS_TOKEN` |
| Vercel | Frontend deployment platform | `VERCEL_TOKEN`, `VERCEL_TEAM_ID`, `VERCEL_WEBHOOK_SECRET` |
| Bitbucket | Source control (Atlassian) | `BITBUCKET_URL`, `BITBUCKET_USERNAME`, `BITBUCKET_APP_PASSWORD`, `BITBUCKET_WEBHOOK_SECRET` |
| Azure DevOps Repos | Source control (Microsoft) | `AZDO_ORG_URL`, `AZDO_TOKEN` |
| Jenkins | CI/CD automation server | `JENKINS_URL`, `JENKINS_USER`, `JENKINS_API_TOKEN`, `JENKINS_WEBHOOK_SECRET` |
| CircleCI | Cloud CI/CD | `CIRCLECI_TOKEN`, `CIRCLECI_WEBHOOK_SECRET` |
| ArgoCD | GitOps delivery (Kubernetes) | `ARGOCD_URL`, `ARGOCD_TOKEN` |
| GitHub Actions | CI/CD built into GitHub | `GITHUB_TOKEN`, `GITHUB_WEBHOOK_SECRET` (shared with GitHub adapter) |
| GitLab CI | CI/CD built into GitLab | `GITLAB_TOKEN`, `GITLAB_BASE_URL`, `GITLAB_WEBHOOK_SECRET` (shared with GitLab adapter) |
| Sentry | Error tracking & performance | `SENTRY_DSN`, `SENTRY_AUTH_TOKEN`, `SENTRY_ORG`, `SENTRY_WEBHOOK_SECRET` |
| New Relic | Observability platform | `NEW_RELIC_API_KEY`, `NEW_RELIC_ACCOUNT_ID` |
| Splunk | SIEM & log analytics | `SPLUNK_URL`, `SPLUNK_HEC_TOKEN` |
| AWS | Cloud infrastructure | `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_REGION` |
| Azure Monitor | Azure observability | `AZURE_TENANT_ID`, `AZURE_CLIENT_ID`, `AZURE_CLIENT_SECRET`, `AZURE_SUBSCRIPTION_ID` |
| GCP | Google Cloud infrastructure | `GCP_PROJECT_ID`, `GCP_PUBSUB_SUBSCRIPTION` |
| HashiCorp Vault | Secrets management | `VAULT_ADDR`, `VAULT_TOKEN`, `VAULT_NAMESPACE` |
| Wiz | Cloud security posture | `WIZ_CLIENT_ID`, `WIZ_CLIENT_SECRET` |
| Prisma Cloud | Cloud-native security | `PRISMA_ACCESS_KEY`, `PRISMA_SECRET_KEY`, `PRISMA_API_URL` |
| Checkov | IaC static analysis | `BRIDGECREW_API_TOKEN` |
| Trivy | Container vulnerability scanner | (no auth required for webhook receiver) |
| Azure DevOps Boards | Work item tracking (Microsoft) | `AZDO_ORG_URL`, `AZDO_TOKEN` (shared with Azure DevOps Repos) |
| Shortcut | Issue tracking (fmr. Clubhouse) | `SHORTCUT_API_TOKEN`, `SHORTCUT_WEBHOOK_SECRET` |
| Notion | Docs & project management | `NOTION_TOKEN` |
| LaunchDarkly | Feature flag management | `LAUNCHDARKLY_API_KEY`, `LAUNCHDARKLY_SDK_KEY` |
| Split.io | Feature flags & experimentation | `SPLIT_API_KEY` |
| Backstage | Internal developer portal | `BACKSTAGE_URL`, `BACKSTAGE_TOKEN` |
| TestRail | Test case management | `TESTRAIL_URL`, `TESTRAIL_USER`, `TESTRAIL_API_KEY` |
| Xray | Test management (Jira plugin) | `XRAY_CLIENT_ID`, `XRAY_CLIENT_SECRET` |
| BrowserStack | Cross-browser test automation | `BROWSERSTACK_USER`, `BROWSERSTACK_ACCESS_KEY` |
| Sauce Labs | Cross-browser test automation | `SAUCE_USERNAME`, `SAUCE_ACCESS_KEY` |
| Zephyr Scale | Jira-native test management | `ZEPHYR_API_TOKEN`, `ZEPHYR_PROJECT_KEY`, `ZEPHYR_WEBHOOK_SECRET` |
| Postman | API test collections & monitors | `POSTMAN_API_KEY` |
| Cypress Cloud | E2E test recording & results | `CYPRESS_API_KEY`, `CYPRESS_PROJECT_ID`, `CYPRESS_WEBHOOK_SECRET` |
| k6 Cloud / Grafana k6 | Performance & load testing | `K6_CLOUD_API_TOKEN`, `K6_PROJECT_ID` |
| Applitools | Visual regression testing | `APPLITOOLS_API_KEY`, `APPLITOOLS_WEBHOOK_SECRET` |
| AWS / GCP / Azure | Cloud infrastructure | Provider-specific credentials |

---

## Installation

### 1. Clone the Repository

```bash
git clone https://github.com/dotrage/forge.git
cd forge
```

### 2. Start Local Dependencies

```bash
docker-compose -f docker-compose.dev.yml up -d
```

This starts PostgreSQL 16, Redis 7, and MinIO locally.

### 3. Run Database Migrations

```bash
export DATABASE_URL="postgres://forge:forge_dev@localhost:5432/forge?sslmode=disable"
migrate -database "$DATABASE_URL" -path internal/db/migrations up
```

### 4. Build the Control Plane

```bash
make build
```

This compiles the Orchestrator, Registry, Policy Engine, and all integration adapters into the `bin/` directory.

### 5. Run Locally

```bash
make run-local
```

The Orchestrator starts on `:19080`, the Registry on `:19081`, and the Policy Engine on `:19082`.

### 6. (Optional) Seed a Project Repository

```bash
./bin/forge-seeder \
  -name "My Project" \
  -company acme \
  -project my-project \
  -output ./seeded
```

This generates a `.forge/` directory with all plan document stubs for the target repository.

### Kubernetes Deployment

```bash
# Apply namespaces
kubectl apply -f deployments/kubernetes/namespaces.yaml

# Deploy via Helm
helm install forge deployments/helm/forge \
  --namespace forge-control-plane \
  --set global.anthropicApiKey="${ANTHROPIC_API_KEY}" \
  --set global.databaseUrl="${DATABASE_URL}"
```

See `deployments/` for the full production deployment configuration including Terraform (EKS), Helm charts, and Kubernetes manifests.

---

## Usage

### Triggering Agent Work

Forge is ticket-driven. The primary way to initiate work is through Jira:

1. Create a Jira ticket with the label `forge` and assign it to the appropriate agent role.
2. The Jira Adapter polls for new tickets and emits a `task.created` event onto the message bus.
3. The Orchestrator picks up the event, resolves dependencies, and dispatches the task to the correct agent runtime.
4. The agent loads its skill, reads the project plan documents from the repository, executes, and opens a PR.
5. A `review.requested` event is emitted; the human reviewer is notified in Slack.

### Triggering via Slack

Send a message to the Forge bot in your configured Slack channel:

```
@forge implement the user authentication endpoint from ticket AUTH-42
```

The Slack Adapter parses the message, creates or links a Jira ticket, and hands off to the Orchestrator.

### Using the MCP Server — Claude Code

The Forge MCP server exposes the Orchestrator and Registry APIs as tools that any [Model Context Protocol](https://modelcontextprotocol.io) client can call — including **Claude Code**, Claude Desktop, and other MCP-compatible environments.

#### Setup

```bash
# Install dependencies and build
make build-mcp

# Or manually:
cd tools/mcp-server
npm install
npm run build
```

#### Claude Code configuration

Add the following to your Claude Code MCP config (`~/.claude/claude_desktop_config.json` or the workspace `.claude/settings.json`):

```json
{
  "mcpServers": {
    "forge": {
      "command": "node",
      "args": ["/path/to/forge-adp/tools/mcp-server/dist/index.js"],
      "env": {
        "FORGE_ORCHESTRATOR_URL": "http://localhost:19080",
        "FORGE_REGISTRY_URL": "http://localhost:19081",
        "FORGE_API_TOKEN": ""
      }
    }
  }
}
```

Once configured, Claude Code can call agents directly from chat or agentic workflows:

| Tool | Description |
|---|---|
| `forge_submit_task` | Submit a new task to any agent role |
| `forge_get_task` | Get status and output of a task by ID |
| `forge_list_tasks` | List tasks with optional role/status filters |
| `forge_approve_task` | Approve a human-in-the-loop checkpoint |
| `forge_reject_task` | Reject a checkpoint with a reason |
| `forge_list_agents` | List all registered agent instances |
| `forge_list_skills` | List available skills (optionally by role) |
| `forge_get_skill` | Get the manifest for a specific skill |
| `forge_list_plans` | List indexed project plan documents |
| `forge_health` | Check health of all control plane services |

#### Example Claude Code prompt

```
Use the forge_submit_task tool to ask the backend-developer agent to implement
the POST /api/v1/auth/refresh endpoint described in ticket AUTH-43.
```

---

### Using the VS Code Extension

The Forge VS Code extension adds an **Agent Tasks** panel to the activity bar and a set of commands for submitting tasks, checking status, and approving/rejecting checkpoints — without leaving your editor.

#### Installation

```bash
# Build and install in one step
make install-vscode-ext
```

Or build manually and install the generated `.vsix`:

```bash
cd tools/vscode-extension
npm install
npm run build
npx @vscode/vsce package --no-dependencies
code --install-extension forge-adp-*.vsix
```

#### Configuration

| Setting | Default | Description |
|---|---|---|
| `forge.orchestratorUrl` | `http://localhost:19080` | Orchestrator base URL |
| `forge.registryUrl` | `http://localhost:19081` | Registry base URL |
| `forge.apiToken` | _(empty)_ | Bearer token for auth (optional for local dev) |
| `forge.pollIntervalSeconds` | `15` | How often to auto-refresh the task list |

#### Commands

| Command | Description |
|---|---|
| **Forge: Submit Task** | Multi-step wizard to submit a task to any agent |
| **Forge: Get Task Status** | Show full task detail in a side panel |
| **Forge: List Tasks** | Refresh and focus the task tree view |
| **Forge: Approve Task** | Approve a pending checkpoint (also available inline in the tree) |
| **Forge: Reject Task** | Reject a pending checkpoint with a reason |
| **Forge: Refresh Task List** | Manually refresh the task list |
| **Forge: Check Service Health** | Ping the Orchestrator health endpoint |

The status bar item shows a live count of tasks needing attention (awaiting approval or blocked) and pulses while agents are running.

---

### Watching Activity

```bash
# Tail Orchestrator logs
kubectl logs -f -l app=forge-orchestrator -n forge-control-plane

# Watch all agent pods
kubectl get pods -n forge-agents -w
```

### Example Workflow

```
Product owner drops a PRD in Slack
    → PM Agent creates Jira epics and stories
    → Backend Developer Agent implements the API (opens PR)
    → QA Agent generates test plan and automated tests
    → SecOps Agent reviews the PR for security issues
    → Human engineer approves the PR
    → DevOps Agent updates deployment manifests
    → SRE Agent adds SLO and alert definitions
```

---

## Configuration

### Environment Variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `DATABASE_URL` | Yes | — | PostgreSQL connection string |
| `REDIS_URL` | Yes | `redis://localhost:6379` | Redis / message bus URL |
| `ANTHROPIC_API_KEY` | Yes | — | Anthropic Claude API key |
| `GITHUB_TOKEN` | Yes | — | GitHub personal access token |
| `GITHUB_WEBHOOK_SECRET` | Yes | — | Secret used to validate GitHub webhook payloads |
| `GITLAB_TOKEN` | No¹ | — | GitLab personal access token |
| `GITLAB_BASE_URL` | No¹ | `https://gitlab.com` | GitLab instance base URL (override for self-hosted) |
| `GITLAB_WEBHOOK_SECRET` | No¹ | — | Secret used to validate GitLab webhook `X-Gitlab-Token` header |
| `JIRA_BASE_URL` | Yes | — | Jira instance URL (e.g. `https://acme.atlassian.net`) |
| `JIRA_USER_EMAIL` | Yes | — | Jira account email |
| `JIRA_API_TOKEN` | Yes | — | Jira API token |
| `CONFLUENCE_BASE_URL` | No¹ | — | Confluence instance URL (e.g. `https://acme.atlassian.net`) |
| `CONFLUENCE_USERNAME` | No¹ | — | Confluence account email |
| `CONFLUENCE_API_TOKEN` | No¹ | — | Confluence API token |
| `LINEAR_API_KEY` | No¹ | — | Linear API key |
| `LINEAR_WEBHOOK_SECRET` | No¹ | — | Secret used to validate Linear webhook HMAC-SHA256 signatures |
| `SLACK_BOT_TOKEN` | No¹ | — | Slack bot OAuth token |
| `SLACK_APP_TOKEN` | No¹ | — | Slack app-level token (socket mode) |
| `TEAMS_WEBHOOK_URL` | No¹ | — | Microsoft Teams incoming webhook URL |
| `TEAMS_HMAC_SECRET` | No¹ | — | HMAC secret for validating Teams bot activity payloads |
| `GOOGLE_CHAT_WEBHOOK_URL` | No¹ | — | Google Chat space webhook URL |
| `GOOGLE_CHAT_VERIFICATION_TOKEN` | No¹ | — | Token for verifying inbound Google Chat events |
| `PAGERDUTY_API_KEY` | No¹ | — | PagerDuty REST API key (v2) |
| `PAGERDUTY_SERVICE_ID` | No¹ | — | PagerDuty service ID used when creating incidents |
| `PAGERDUTY_FROM_EMAIL` | No¹ | — | Email address for PagerDuty `From` header (required by some accounts) |
| `OPSGENIE_API_KEY` | No¹ | — | Opsgenie API key |
| `DATADOG_API_KEY` | No¹ | — | Datadog API key |
| `DATADOG_APP_KEY` | No¹ | — | Datadog application key (required for monitor management) |
| `GRAFANA_URL` | No¹ | — | Grafana instance base URL (e.g. `https://grafana.acme.com`) |
| `GRAFANA_API_KEY` | No¹ | — | Grafana service account token or API key |
| `SNYK_API_TOKEN` | No¹ | — | Snyk personal or service account API token |
| `SNYK_ORG_ID` | No¹ | — | Snyk organisation ID |
| `SNYK_WEBHOOK_SECRET` | No¹ | — | Secret used to validate Snyk webhook HMAC-SHA256 signatures |
| `SONARQUBE_URL` | No¹ | — | SonarQube instance base URL (e.g. `https://sonar.acme.com`) |
| `SONARQUBE_TOKEN` | No¹ | — | SonarQube user or service account token |
| `SONARQUBE_WEBHOOK_SECRET` | No¹ | — | Secret used to validate SonarQube webhook HMAC-SHA256 signatures |
| `TFC_TOKEN` | No¹ | — | Terraform Cloud API token |
| `TFC_ORGANIZATION` | No¹ | — | Terraform Cloud organisation name |
| `TFC_WEBHOOK_HMAC_KEY` | No¹ | — | HMAC key for validating Terraform Cloud notification signatures |
| `ATLANTIS_URL` | No¹ | — | Atlantis server base URL (e.g. `https://atlantis.acme.com`) |
| `ATLANTIS_TOKEN` | No¹ | — | Atlantis API token (`X-Atlantis-Token`) |
| `ATLANTIS_WEBHOOK_SECRET` | No¹ | — | Secret used to validate Atlantis webhook HMAC-SHA256 signatures |

> ¹ Required only when the corresponding adapter is enabled for your deployment.
| `FORGE_LOG_LEVEL` | No | `info` | Log level: `debug`, `info`, `warn`, `error` |
| `FORGE_AUTONOMY_LEVEL` | No | `0` | Default autonomy level (0–3) for all agents |
| `FORGE_COMPANY_ID` | Yes | — | Tenant/company identifier |

### Project Repository Configuration (`.forge/config.yaml`)

Each project repository that Forge manages must contain a `.forge/` directory. The `config.yaml` controls per-project agent behavior:

```yaml
project_id: my-project
company_id: acme

agents:
  backend-developer:
    autonomy_level: 1       # Requires human PR approval
    llm_model: claude-3-5-sonnet-20241022
    skills:
      - api-implementation
      - business-logic
      - test-generation
  qa:
    autonomy_level: 1
    skills:
      - test-plan-generation
      - bug-reporting

integrations:
  jira:
    project_key: ACME
    agent_label: forge
  github:
    repo: acme-corp/my-project
    base_branch: main
  # gitlab:                        # Use instead of (or alongside) github
  #   project_id: "12345"
  #   base_branch: main
  confluence:
    space_key: ACME
    agent_label: forge             # Pages with this label trigger task.created
  linear:
    team_id: "abc123"
    agent_label: forge             # Issues with this label trigger task.created
  slack:
    notification_channel: "#eng-forge"
    escalation_channel: "#eng-leads"
  # teams:
  #   notification_channel: forge-notifications
  # google_chat:
  #   space_name: spaces/XXXXXXXX

policy:
  require_secops_review: true
  require_dba_review_for_migrations: true
  deployment_approval_required: true
```

### Plan Documents

The full plan document structure for a seeded repository:

```
.forge/
├── PRODUCT.md          # Product vision, target users, core value prop
├── ARCHITECTURE.md     # System architecture, service boundaries, tech stack
├── API_CONTRACTS.md    # API specifications (or pointer to OpenAPI files)
├── DATA_MODEL.md       # Database schema, entity relationships, data flow
├── UX_GUIDELINES.md    # Design system, component library, interaction patterns
├── SECURITY_POLICY.md  # Auth model, data classification, compliance requirements
├── INFRASTRUCTURE.md   # Cloud architecture, deployment topology, environments
├── TEST_STRATEGY.md    # Testing philosophy, coverage targets, tool choices
├── CONTRIBUTING.md     # Code standards, branch strategy, review requirements
├── GLOSSARY.md         # Domain-specific terminology
└── config.yaml         # Forge-specific configuration for this project
```

### Autonomy Levels

| Level | Behavior |
|---|---|
| `0` | Full human approval required for all agent outputs |
| `1` | Agent opens PRs; humans must approve and merge |
| `2` | Agent can merge PRs that pass all automated checks on low-risk change types |
| `3` | Agent can merge low-risk PRs (docs, tests, style) automatically after CI passes |

---

## API & Command Reference

### Control Plane HTTP API

All services expose a JSON REST API. The Orchestrator is the primary entry point.

#### Orchestrator (`:19080`)

| Method | Path | Description |
|---|---|---|
| `POST` | `/v1/tasks` | Submit a new task directly (bypasses Jira) |
| `GET` | `/v1/tasks/{id}` | Get task status and output |
| `GET` | `/v1/tasks?agent_role=&status=` | List tasks with filters |
| `POST` | `/v1/tasks/{id}/approve` | Approve a pending human-in-the-loop checkpoint |
| `POST` | `/v1/tasks/{id}/reject` | Reject a pending checkpoint with a reason |
| `GET` | `/v1/health` | Health check |

#### Registry (`:19081`)

| Method | Path | Description |
|---|---|---|
| `GET` | `/v1/agents` | List all registered agent instances |
| `GET` | `/v1/skills` | List available skills |
| `GET` | `/v1/skills/{role}/{name}` | Get a specific skill manifest |
| `POST` | `/v1/skills` | Register a new skill version |
| `GET` | `/v1/plans` | List indexed plan documents |

#### Policy Engine (`:19082`)

| Method | Path | Description |
|---|---|---|
| `POST` | `/v1/evaluate` | Evaluate a proposed agent action against policies |
| `GET` | `/v1/policies` | List active policies |
| `PUT` | `/v1/policies/{name}` | Update a policy definition |

### Integration Adapters

Each adapter runs as an independent service and communicates with the rest of the system via the message bus. All adapters expose a `/health` endpoint and a `/webhook` endpoint for inbound events from the external platform.

| Adapter | Port | Inbound (webhooks) | Outbound (API) |
|---|---|---|---|
| **Jira** | `:19090` | `jira:issue_created` → `task.created`; `jira:issue_updated` | `GET/POST /api/v1/tickets`, `POST /api/v1/transitions` |
| **GitHub** | `:19091` | `PullRequestEvent` → `review.requested`/`task.completed`; `CheckSuiteEvent` | `POST /api/v1/branches`, `POST /api/v1/pulls`, `/api/v1/commits` |
| **Slack** | `:19092` | Slash commands, interactive payloads | Notifies on `task.completed`, `review.requested`, `escalation.created` |
| **Teams** | `:19093` | Bot Framework activity endpoint | Notifies on `task.completed`, `review.requested`, `escalation.created` |
| **Google Chat** | `:19094` | Chat event endpoint | Notifies on `task.completed`, `review.requested`, `escalation.created` |
| **GitLab** | `:19095` | `MergeEvent` → `review.requested`/`task.completed`; `PipelineEvent` → `deployment.approved`/`task.failed` | `POST /api/v1/branches`, `POST /api/v1/mergerequests`, `/api/v1/commits` |
| **Confluence** | `:19096` | `page_created` → `task.created` (when page carries `forge` label) | `GET/POST/PUT /api/v1/pages`, `GET /api/v1/spaces` |
| **Linear** | `:19097` | `Issue create` → `task.created`; `Issue update` → `task.completed`; `Issue remove` → `task.failed` | `GET/POST /api/v1/issues`, `POST /api/v1/transitions` |
| **PagerDuty** | `:19098` | `incident.trigger` → `escalation.created`; `incident.resolve` → `task.completed` | `POST /api/v1/incidents`, `PUT /api/v1/incidents?id=` |
| **Opsgenie** | `:19099` | `Create` → `escalation.created`; `Close` → `task.completed` | `POST /api/v1/alerts`, `DELETE /api/v1/alerts?id=`, `PATCH /api/v1/alerts?id=` |
| **Datadog** | `:19100` | `Triggered`/`No Data` → `escalation.created`; `Recovered` → `task.completed` | `POST /api/v1/events` |
| **Grafana** | `:19101` | `firing` → `escalation.created`; `resolved` → `task.completed` | `POST /api/v1/annotations`, `POST /api/v1/silences` |
| **Snyk** | `:19102` | `newIssues` (critical/high) → `escalation.created` | `GET /api/v1/vulnerabilities`, `GET /api/v1/projects` |
| **SonarQube** | `:19103` | `analysis.completed` quality gate `ERROR` → `escalation.created`; `FAILED`/`CANCELLED` → `task.failed` | `GET /api/v1/issues`, `GET /api/v1/qualitygates` |
| **Terraform Cloud** | `:19104` | `applied` → `task.completed`; `planned_and_finished` → `deployment.approved`; `errored`/`canceled` → `task.failed` | `GET/POST /api/v1/runs`, `GET /api/v1/workspaces` |
| **Atlantis** | `:19105` | `plan success` → `deployment.requested`; `apply success` → `task.completed`; `failure`/`error` → `task.failed` | `POST /api/v1/plan`, `POST /api/v1/apply` |
| **MongoDB Atlas** | `:19106` | `ALERT_OPENED` → `escalation.created`; `ALERT_CLOSED` → `task.completed` | `GET /api/v1/alerts`, `GET /api/v1/clusters` |
| **Databricks** | `:19107` | `TERMINATED/SUCCESS` → `task.completed`; `FAILURE` → `task.failed` | `GET /api/v1/jobs`, `GET /api/v1/runs` |
| **Vercel** | `:19108` | `deployment.succeeded` → `task.completed`; `deployment.error` → `task.failed`; `deployment.ready` → `deployment.approved` | `GET /api/v1/deployments`, `GET /api/v1/projects` |
| **Bitbucket** | `:19109` | `pullrequest:created` → `review.requested`; `fulfilled` → `task.completed`; `rejected` → `review.rejected` | `GET /api/v1/pullrequests`, `POST /api/v1/comments` |
| **Azure DevOps Repos** | `:19110` | `git.pullrequest.created` → `review.requested`; merged → `task.completed` | `GET /api/v1/pullrequests`, `GET /api/v1/repos` |
| **Jenkins** | `:19111` | `STARTED` → `task.started`; `COMPLETED/SUCCESS` → `task.completed`; `FAILURE` → `task.failed` | `POST /api/v1/jobs/build`, `GET /api/v1/jobs` |
| **CircleCI** | `:19112` | `workflow-completed/success` → `task.completed`; `failed` → `task.failed` | `GET /api/v1/pipelines`, `GET /api/v1/workflows` |
| **ArgoCD** | `:19113` | `Succeeded` → `deployment.approved`; `Failed` → `task.failed`; `Running` → `task.started` | `GET /api/v1/applications`, `POST /api/v1/sync` |
| **GitHub Actions / GitLab CI** | `:19114` | `workflow_run completed` → `task.completed`/`task.failed`; GitLab pipeline success/failure | (webhook only) |
| **Sentry** | `:19115` | `created` (error/fatal) → `escalation.created`; `resolved` → `task.completed` | `GET /api/v1/issues`, `POST /api/v1/comments` |
| **New Relic** | `:19116` | `open` (CRITICAL) → `escalation.created`; `closed` → `task.completed` | `GET /api/v1/alerts`, `POST /api/v1/events` |
| **Splunk** | `:19117` | Alert webhook → `escalation.created`; republishes bus events to Splunk HEC | `POST /api/v1/search`, `GET /api/v1/jobs` |
| **AWS** | `:19118` | SNS `ALARM` → `escalation.created`; `OK` → `task.completed`; auto-confirms SNS subscriptions | (webhook only) |
| **Azure Monitor / Azure DevOps** | `:19119` | `Fired` → `escalation.created`; `Resolved` → `task.completed`; ADO `build.complete` → `task.completed`/`task.failed` | `GET /api/v1/alerts`, `GET /api/v1/builds` |
| **GCP** | `:19120` | Pub/Sub Cloud Build `SUCCESS` → `task.completed`; `FAILURE` → `task.failed`; Cloud Monitoring open → `escalation.created` | (Pub/Sub push only) |
| **HashiCorp Vault** | `:19121` | Audit log errors → `escalation.created` | `GET /api/v1/secrets`, `POST /api/v1/leases` |
| **Wiz / Prisma Cloud** | `:19122` | OPEN CRITICAL/HIGH → `escalation.created`; RESOLVED → `task.completed` (both platforms) | `GET /api/v1/issues` |
| **Checkov / Trivy** | `:19123` | Checkov violations → `escalation.created`/`task.blocked`; Trivy SARIF errors → `escalation.created` | (webhook only) |
| **Azure DevOps Boards** | `:19124` | `workitem.created` (tagged `forge`) → `task.created`; state `Done` → `task.completed`; `Removed` → `task.failed` | `GET /api/v1/workitems`, `PATCH /api/v1/workitems` |
| **Shortcut** | `:19125` | `story create` → `task.created`; `completed_at` set → `task.completed` | `GET /api/v1/stories`, `POST /api/v1/stories` |
| **Notion** | `:19126` | `page_created` → `task.created`; `page_updated` → `task.completed` | `GET/POST /api/v1/pages`, `GET /api/v1/databases` |
| **LaunchDarkly** | `:19127` | Flag changed → `task.completed` | `GET /api/v1/flags`, `GET /api/v1/environments` |
| **Split.io** | `:19128` | `SPLIT_KILLED` → `escalation.created`; `SPLIT_UPDATED` → `task.completed` | `GET /api/v1/splits`, `POST /api/v1/toggles` |
| **Backstage** | `:19129` | Scaffolder `completed` → `task.completed`; `failed` → `task.failed` | `GET /api/v1/entities`, `GET /api/v1/components` |
| **TestRail / Xray** | `:19130` | Test run completed (failures) → `task.blocked`; all pass → `task.completed` (both platforms) | `GET /api/v1/runs`, `GET /api/v1/results` |
| **BrowserStack / Sauce Labs** | `:19131` | Build `done`/`complete` with failures → `task.blocked`; all pass → `task.completed` (both platforms) | `GET /api/v1/builds`, `GET /api/v1/sessions` |
| **Zephyr Scale** | `:19132` | `testCycle_updated` DONE/PASSED → `task.completed`; FAILED → `task.blocked`; `testExecution_updated` FAIL → `escalation.created` | `GET/POST /api/v1/cycles`, `GET/POST /api/v1/executions`, `GET /api/v1/cases` |
| **Postman / Newman** | `:19133` | Monitor `passed` → `task.completed`; `failed` → `escalation.created`; Newman report failures → `task.blocked` | `GET /api/v1/collections`, `GET /api/v1/monitors`, `POST /api/v1/runs` |
| **Cypress Cloud** | `:19134` | `RUN_COMPLETED` passed → `task.completed`; failed/errored → `task.blocked` | `GET /api/v1/runs`, `GET /api/v1/instances` |
| **k6 Cloud** | `:19135` | `TEST_FINISHED` passed → `task.completed`; threshold breach → `escalation.created`; `TEST_ABORTED` → `task.failed` | `GET/POST /api/v1/runs`, `GET /api/v1/thresholds` |
| **Applitools** | `:19136` | `batchCompleted` Passed → `task.completed`; Failed → `task.blocked`; Unresolved → `review.requested` | `GET /api/v1/batches`, `GET /api/v1/results`, `GET/DELETE /api/v1/baselines` |

Webhook security:
- **GitHub** — HMAC-SHA256 (`GITHUB_WEBHOOK_SECRET`)
- **GitLab** — Static token header (`GITLAB_WEBHOOK_SECRET` → `X-Gitlab-Token`)
- **Linear** — HMAC-SHA256 (`LINEAR_WEBHOOK_SECRET` → `Linear-Signature`)
- **Teams** — HMAC-SHA256 (`TEAMS_HMAC_SECRET`)
- **Google Chat** — Verification token (`GOOGLE_CHAT_VERIFICATION_TOKEN`)
- **PagerDuty** — Webhook signature validation via PagerDuty's `X-PagerDuty-Signature` header (configure in PagerDuty webhook settings)
- **Opsgenie** — Webhook validation via Opsgenie's HMAC-SHA256 signature on the request body
- **Datadog** — Webhook validation via Datadog's `X-Datadog-Signature` header (configure shared secret in Datadog webhook integration settings)
- **Grafana** — Webhook validation via Grafana's `X-Grafana-Signature` header (configure shared secret in Grafana contact point settings)
- **Snyk** — HMAC-SHA256 (`SNYK_WEBHOOK_SECRET` → `X-Snyk-Signature`)
- **SonarQube** — HMAC-SHA256 (`SONARQUBE_WEBHOOK_SECRET` → `X-Sonar-Webhook-HMAC-SHA256`)
- **Terraform Cloud** — HMAC-SHA512 (`TFC_WEBHOOK_HMAC_KEY` → `X-TFE-Notification-Signature`)
- **Atlantis** — HMAC-SHA256 (`ATLANTIS_WEBHOOK_SECRET` → `X-Atlantis-Signature`)
- **MongoDB Atlas** — HMAC-SHA1 (`MONGODB_ATLAS_WEBHOOK_SECRET` → `X-MMS-Signature`)
- **Databricks** — Bearer token (`DATABRICKS_TOKEN` → `Authorization`)
- **Vercel** — HMAC-SHA1 (`VERCEL_WEBHOOK_SECRET` → `X-Vercel-Signature`)
- **Bitbucket** — HMAC-SHA256 (`BITBUCKET_WEBHOOK_SECRET` → `X-Hub-Signature`)
- **Azure DevOps Repos** — HMAC-SHA256 (`AZDO_TOKEN` → `X-Hub-Signature-256`) + Basic auth
- **Jenkins** — HMAC-SHA256 (`JENKINS_WEBHOOK_SECRET` → `X-Jenkins-Signature`)
- **CircleCI** — HMAC-SHA256 (`CIRCLECI_WEBHOOK_SECRET` → `Circleci-Signature`, `v1=` prefix)
- **ArgoCD** — Bearer token (`ARGOCD_TOKEN` → `Authorization`)
- **GitHub Actions** — HMAC-SHA256 (`GITHUB_WEBHOOK_SECRET` → `X-Hub-Signature-256`, shared with GitHub adapter)
- **GitLab CI** — Static token (`GITLAB_WEBHOOK_SECRET` → `X-Gitlab-Token`, shared with GitLab adapter)
- **Sentry** — HMAC-SHA256 (`SENTRY_WEBHOOK_SECRET` → `Sentry-Hook-Signature`)
- **New Relic** — API key header (`NEW_RELIC_API_KEY` → `X-Api-Key`)
- **Splunk** — HEC token (`SPLUNK_HEC_TOKEN` → `Authorization: Splunk <token>`)
- **AWS** — SNS topic subscription confirmation; no additional HMAC (SNS signs messages with X.509)
- **Azure Monitor** — Basic auth with PAT (`AZURE_CLIENT_ID`/`AZURE_CLIENT_SECRET`)
- **GCP** — Pub/Sub push token verification (`GCP_PUBSUB_PUSH_TOKEN`)
- **HashiCorp Vault** — `VAULT_TOKEN` → `X-Vault-Token` header; optional namespace
- **Wiz / Prisma Cloud** — OAuth2 client credentials (`WIZ_CLIENT_ID`/`WIZ_CLIENT_SECRET`; `PRISMA_ACCESS_KEY`/`PRISMA_SECRET_KEY`)
- **Checkov / Trivy** — Bridgecrew API token (`BRIDGECREW_API_TOKEN` → `Authorization`)
- **Azure DevOps Boards** — Basic auth with PAT (`AZDO_TOKEN`, shared with Azure DevOps Repos)
- **Shortcut** — HMAC-SHA256 (`SHORTCUT_WEBHOOK_SECRET` → `Shortcut-Signature`)
- **Notion** — Bearer token (`NOTION_TOKEN` → `Authorization`), `Notion-Version: 2022-06-28`
- **LaunchDarkly** — API key (`LAUNCHDARKLY_API_KEY` → `Authorization`)
- **Split.io** — Bearer token (`SPLIT_API_KEY` → `Authorization`)
- **Backstage** — Bearer token (`BACKSTAGE_TOKEN` → `Authorization`)
- **TestRail** — Basic auth (`TESTRAIL_USER`/`TESTRAIL_API_KEY`)
- **Xray** — OAuth2 client credentials (`XRAY_CLIENT_ID`/`XRAY_CLIENT_SECRET`)
- **BrowserStack** — Basic auth (`BROWSERSTACK_USER`/`BROWSERSTACK_ACCESS_KEY`)
- **Sauce Labs** — Basic auth (`SAUCE_USERNAME`/`SAUCE_ACCESS_KEY`)
- **Zephyr Scale** — Shared secret header (`ZEPHYR_WEBHOOK_SECRET` → `X-Zephyr-Secret`)
- **Postman** — API key header (`POSTMAN_API_KEY` → `X-Api-Key`)
- **Cypress Cloud** — Shared secret header (`CYPRESS_WEBHOOK_SECRET` → `X-Cypress-Secret`)
- **k6 Cloud** — Bearer token (`K6_CLOUD_API_TOKEN` → `Authorization`)
- **Applitools** — Shared secret header (`APPLITOOLS_WEBHOOK_SECRET` → `X-Applitools-Signature`)

### Makefile Commands

```bash
make setup              # Install all dependencies (Go, Python, Node.js tools)
make migrate            # Run database migrations
make build              # Build all Go binaries, Python package, MCP server, and VS Code extension
make build-mcp          # Build only the MCP server (tools/mcp-server/)
make build-vscode-ext   # Build only the VS Code extension (tools/vscode-extension/)
make install-vscode-ext # Build and install the VS Code extension via `code --install-extension`
make run-local          # Start control plane services locally
make test               # Run all tests (Go + Python)
make lint               # Run golangci-lint and ruff
make docker-build       # Build Docker images for all services
make seed               # Seed a local test project
```

### CLI Tools

#### Seeder

Generates `.forge/` plan documents for a new project repository:

```bash
./bin/forge-seeder \
  -name    "Project Name" \
  -company "acme" \
  -project "project-slug" \
  -output  "/path/to/repo"
```

#### Tenant Onboarding

Provisions a new enterprise tenant (namespace, database, secrets, Helm release):

```bash
./tools/tenant-onboard/main.sh <tenant-id> <tenant-name> [namespace|cluster]
# Example:
./tools/tenant-onboard/main.sh acme-corp "Acme Corporation" namespace
```

### Message Bus Events

Agents communicate through structured events on the message bus:

| Event | Description |
|---|---|
| `task.created` | New task assigned to an agent |
| `task.started` | Agent has begun execution |
| `task.blocked` | Agent cannot proceed; needs input or upstream dependency |
| `task.completed` | Agent finished; output ready for review |
| `task.failed` | Unrecoverable agent error |
| `review.requested` | Agent requests human or peer-agent review |
| `review.approved` / `review.rejected` | Review outcome |
| `deployment.requested` / `deployment.approved` | Deployment lifecycle |
| `escalation.created` | Issue surfaced to human decision-maker |

---

## Contributing

### Getting Started

1. Fork the repository and create a feature branch from `main`:
   ```bash
   git checkout -b feat/my-feature
   ```
2. Make your changes, write tests, and ensure everything passes:
   ```bash
   make test
   make lint
   ```
3. Open a pull request against `main`. All PRs require at least one reviewer approval and a passing CI build.

### Branch Strategy

| Branch prefix | Purpose |
|---|---|
| `feat/` | New features |
| `fix/` | Bug fixes |
| `chore/` | Maintenance, dependency updates |
| `docs/` | Documentation only |

### Running Tests

```bash
# Go unit + integration tests
go test ./...

# Python agent runtime tests
cd pkg/agents && poetry run pytest tests/ -v

# Load tests (requires a running local stack)
cd tests/load && k6 run orchestrator.js
```

### Code Standards

- **Go** — Follow `gofmt` and `golangci-lint` defaults. Use standard library error wrapping (`fmt.Errorf("...: %w", err)`).
- **Python** — `ruff` for linting, `black` for formatting, `mypy` for type checking (strict mode).
- **Skills** — All new skills must include a `MANIFEST.yaml`, `SKILL.md`, at least one example, and a validator.
- **Commits** — Use [Conventional Commits](https://www.conventionalcommits.org/) (`feat:`, `fix:`, `docs:`, etc.).

### Adding a New Agent Skill

1. Create the skill directory under `skills/{agent-role}/{skill-name}/`.
2. Add `MANIFEST.yaml` with the skill metadata and autonomy level.
3. Write `SKILL.md` with execution steps, quality gates, and escalation triggers.
4. Add example input/output pairs under `examples/`.
5. Write a skill validator under `validators/`.
6. Register the skill via the Registry API or `make seed`.

See [skills/backend-developer/api-implementation/](skills/backend-developer/api-implementation/) for a reference implementation.

### Submitting a PR

- Link the relevant Jira ticket in the PR description.
- Describe what changed and why.
- Include test output showing the change works.
- Schema migrations require DBA lead + backend lead sign-off before merge.
- Any change touching authentication, secrets handling, or the Policy Engine requires a SecOps review.

---

## License

This project is licensed under the **Apache License 2.0**. See [LICENSE](LICENSE) for the full text.

---

<p align="center">
  Built with ❤️ for engineering teams who want their AI to do the work, not just talk about it.
</p>
