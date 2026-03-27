# Project Bootstrap Skill

## Purpose

Populate seeded `.forge/` plan document stubs with product-specific content so that all Forge agents have the context they need to begin work:
- Gather product requirements from input brief or Confluence PRD
- Generate PRODUCT.md content (vision, users, value proposition, features, success metrics)
- Generate CONTRIBUTING.md code style rules specific to the chosen tech stack
- Coordinate with the Architect agent for technical document content
- Commit populated documents via GitHub and open a PR for human review
- **Platform mode**: orchestrate bootstrap across multiple repos (API, workers, UI) that form a single platform, ensuring shared contracts and consistent architecture

## Prerequisites

1. Repository (or all platform repos) has been seeded with `.forge/` directory (via `forge-seeder`)
2. Product brief or Confluence PRD link provided as input
3. `GITHUB_TOKEN` with write access to the target repository (or all platform repos)
4. Architect agent available for technical document delegation

## Execution Steps

1. **Load Seeded Stubs**
   - Read existing `.forge/` documents via `plan-reader`
   - Parse `config.yaml` to extract project name, tech stack, and integration settings
   - **Detect platform mode**: check for `platform` section in config.yaml
   - If platform mode, load stubs from all sibling repos via `plan-reader.load_platform_plans()`

2. **Gather Product Context**
   - If `confluence_prd_url` is provided, fetch the PRD from Confluence
   - If `product_brief` is provided inline, use it directly
   - Extract: product purpose, target users, key features, success metrics, constraints
   - **Platform mode**: identify which features belong to which repo role (API, workers, UI)

3. **Generate Product Documents**
   - Populate PRODUCT.md with structured product vision derived from the brief
   - Populate CONTRIBUTING.md code style section based on the tech stack in `config.yaml`
   - Ensure all `<!-- placeholder -->` comments are replaced with substantive content
   - **Platform mode**: generate a shared PRODUCT.md (identical across repos) and repo-specific CONTRIBUTING.md (tailored to each repo's tech stack)

4. **Delegate Technical Documents to Architect**
   - Create tasks for the Architect agent via the orchestrator:
     - `requirements-analysis` — to produce detailed functional and non-functional requirements
     - `architecture-design` — to populate ARCHITECTURE.md and DATA_MODEL.md
     - `api-design` — to populate API_CONTRACTS.md with initial endpoint specifications
   - Pass product context and `config.yaml` tech stack as input to each task
   - Set task dependencies: `requirements-analysis` must complete before `architecture-design` and `api-design`
   - **Platform mode**: pass the full platform config and all repo roles to the Architect so it can:
     - Design a holistic architecture spanning all repos
     - Generate repo-specific ARCHITECTURE.md for each repo with cross-references to siblings
     - Define shared API contracts (what the UI consumes from the API)
     - Define shared event/message schemas (what workers consume from the API)
     - Generate repo-specific DATA_MODEL.md only for repos with a database

5. **Assemble and Commit**
   - Wait for Architect agent tasks to complete
   - Collect all populated documents (PM-authored + Architect-authored)
   - **Single-repo mode**: commit to `forge/pm/bootstrap-plan-docs` branch, open one PR
   - **Platform mode**: commit repo-specific documents to each repo on `forge/pm/bootstrap-plan-docs` branch, open one PR per repo
   - Each PR references the sibling PRs in its description for reviewer context

6. **Communicate**
   - Post a summary to the project Slack channel via `slack-communication`
   - Include links to all PRs and a checklist of documents populated per repo

## Dependencies

- `common/plan-reader` — read seeded stubs and config (including cross-repo platform reads)
- `common/github-interaction` — commit files and open PRs (one per repo in platform mode)
- `common/slack-communication` — notify team
- `common/jira-interaction` — create tracking ticket for bootstrap

## Inputs

```json
{
  "product_brief": "(required) Free-text product description, goals, and constraints",
  "confluence_prd_url": "(optional) URL to a Confluence PRD page for richer context",
  "repo": "(required) Primary GitHub repository in org/repo format. In platform mode, this is any repo in the platform — siblings are discovered from config.yaml",
  "branch": "(optional) Base branch, defaults to main"
}
```

## Outputs

```json
{
  "pr_urls": {
    "acme/api": "https://github.com/acme/api/pull/1",
    "acme/workers": "https://github.com/acme/workers/pull/1",
    "acme/ui": "https://github.com/acme/ui/pull/1"
  },
  "documents_populated": {
    "acme/api": ["PRODUCT.md", "CONTRIBUTING.md", "ARCHITECTURE.md", "DATA_MODEL.md", "API_CONTRACTS.md"],
    "acme/workers": ["PRODUCT.md", "CONTRIBUTING.md", "ARCHITECTURE.md", "DATA_MODEL.md"],
    "acme/ui": ["PRODUCT.md", "CONTRIBUTING.md", "ARCHITECTURE.md"]
  },
  "architect_tasks": ["task-id-1", "task-id-2", "task-id-3"],
  "tracking_ticket": "PROJ-100",
  "platform_id": "acme-payments"
}
```

## Plan References

| Document | Sections to Consult |
|----------|---------------------|
| config.yaml | Project name, tech stack, integrations, agent list, **platform section** |
| PRODUCT.md | Stub to populate with product vision |
| CONTRIBUTING.md | Stub to populate with code style rules |

## Quality Gates

- [ ] All placeholder comments (`<!-- ... -->`) replaced with substantive content
- [ ] PRODUCT.md contains at minimum: overview, target users, key features, success metrics
- [ ] CONTRIBUTING.md code style section references the actual tech stack from config.yaml
- [ ] Architect agent tasks completed successfully before PRs are opened
- [ ] PRs opened with human reviewer assigned
- [ ] No secrets or credentials included in generated content
- [ ] **Platform mode**: PRODUCT.md is consistent across all repos
- [ ] **Platform mode**: API contracts in the API repo match the client expectations in the UI repo
- [ ] **Platform mode**: event schemas in the API repo match the consumer expectations in the workers repo

## Escalation Triggers

- Product brief is too vague to derive meaningful product vision (< 50 words, no clear purpose)
- Confluence PRD URL is inaccessible or returns empty content
- Architect agent tasks fail or time out
- Tech stack in config.yaml is empty or uses only defaults without customization
- **Platform mode**: a sibling repo is missing its `.forge/` directory (not seeded)
- **Platform mode**: sibling repos have conflicting tech stack or infrastructure settings

## Anti-Patterns

- Do not auto-merge the bootstrap PR — always require human review
- Do not generate speculative architecture without Architect agent input
- Do not populate technical documents (ARCHITECTURE.md, DATA_MODEL.md, API_CONTRACTS.md) directly — delegate to Architect
- Do not leave any placeholder comments in the final output
- Do not skip Slack notification — the team must know documents are ready for review
- **Platform mode**: do not design each repo's architecture in isolation — the Architect must see the full platform
- **Platform mode**: do not duplicate API contract definitions — define once in the API repo, reference from others
