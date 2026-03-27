"""
PM Agent implementation.

Four skills:
  - sprint-planning      : decompose goals into sprint-ready Jira tickets
  - status-reporting     : generate sprint status reports
  - ticket-triage        : review and route incoming tickets
  - project-bootstrap    : populate seeded .forge/ plan documents for a new project
"""
import json
import re
from datetime import datetime, timezone

from .runtime import BaseAgent, Skill, SkillContext, LLMProvider


# ---------------------------------------------------------------------------
# project-bootstrap
# ---------------------------------------------------------------------------

class ProjectBootstrapSkill(Skill):
    """Populate seeded .forge/ plan documents with product-specific content."""

    @property
    def name(self) -> str:
        return "project-bootstrap"

    @property
    def description(self) -> str:
        return "Populate seeded plan document stubs with product vision and contributing guidelines"

    def execute(self, context: SkillContext, llm: LLMProvider) -> dict:
        product_brief = context.task.input_payload.get("product_brief", "")
        repo = context.task.input_payload.get("repo", "")
        config_yaml = context.plan_documents.get("config.yaml", "")
        product_stub = context.plan_documents.get("PRODUCT.md", "")
        contributing_stub = context.plan_documents.get("CONTRIBUTING.md", "")

        if not product_brief:
            return {
                "error": "product_brief is required",
                "escalation": "Product brief is too vague or missing"
            }

        # Detect platform mode from config
        config = {}
        if config_yaml:
            try:
                import yaml
                config = yaml.safe_load(config_yaml) or {}
            except Exception:
                pass

        platform = config.get("forge", {}).get("platform")
        is_platform = platform and "repos" in platform and len(platform["repos"]) > 0

        system_prompt = self.build_system_prompt(context)

        if is_platform:
            return self._execute_platform(
                context, llm, system_prompt, product_brief, repo,
                config_yaml, product_stub, contributing_stub, platform
            )
        else:
            return self._execute_single(
                context, llm, system_prompt, product_brief, repo,
                config_yaml, product_stub, contributing_stub
            )

    def _execute_single(
        self, context, llm, system_prompt, product_brief, repo,
        config_yaml, product_stub, contributing_stub
    ) -> dict:
        """Bootstrap a single-repo project."""
        system_prompt += (
            "\n\nYou are the Forge PM Agent running the project-bootstrap skill. "
            "Your job is to populate PRODUCT.md and CONTRIBUTING.md with substantive "
            "content derived from the product brief. Be concrete and specific. "
            "Replace all placeholder comments with real content."
        )

        messages = [
            {
                "role": "user",
                "content": f"""
Populate the plan documents for this new project.

## Product Brief
{product_brief}

## Project Config
{config_yaml[:2000] if config_yaml else "(not available)"}

## Current PRODUCT.md Stub
{product_stub}

## Current CONTRIBUTING.md Stub
{contributing_stub}

## Instructions

### PRODUCT.md
Populate every section with substantive content:
1. **Overview** — What the product is and why it exists
2. **Target Users** — Who uses it and their key characteristics
3. **Core Value Proposition** — The problem it solves and why users choose it
4. **Key Features** — Bulleted list of major capabilities
5. **Success Metrics** — Measurable KPIs

### CONTRIBUTING.md
Keep the existing testing requirements, PR template, branch naming, and commit message sections.
Populate the **Code Style** section with specific rules for the tech stack in config.yaml.

Replace ALL `<!-- ... -->` placeholder comments with real content.

Return strictly valid JSON:
{{
  "product_md": "Full markdown content for PRODUCT.md",
  "contributing_md": "Full markdown content for CONTRIBUTING.md",
  "architect_tasks": [
    {{
      "skill_name": "requirements-analysis",
      "input": {{
        "product_brief": "...",
        "repo": "..."
      }}
    }},
    {{
      "skill_name": "architecture-design",
      "input": {{
        "product_brief": "...",
        "repo": "..."
      }},
      "depends_on": "requirements-analysis"
    }},
    {{
      "skill_name": "api-design",
      "input": {{
        "product_brief": "...",
        "repo": "..."
      }},
      "depends_on": "requirements-analysis"
    }}
  ]
}}
""",
            }
        ]

        response = llm.complete(system_prompt, messages, max_tokens=8000, temperature=0.4)
        result = self._parse_json_response(response)

        result["repo"] = repo
        result["documents_populated"] = ["PRODUCT.md", "CONTRIBUTING.md"]
        result.setdefault("generated_at", datetime.now(timezone.utc).isoformat())
        return result

    def _execute_platform(
        self, context, llm, system_prompt, product_brief, repo,
        config_yaml, product_stub, contributing_stub, platform
    ) -> dict:
        """Bootstrap a multi-repo platform project."""
        platform_id = platform.get("id", "unknown")
        repos = platform.get("repos", [])

        # Build a summary of the platform for the LLM
        repo_summary = "\n".join(
            f"- **{r.get('local_path') or r.get('repo', 'unknown')}** (role: {r.get('role', 'unknown')})"
            for r in repos
        )
        repo_summary += "\n\nEach repo's tech stack is defined in its own .forge/config.yaml."

        system_prompt += (
            "\n\nYou are the Forge PM Agent running the project-bootstrap skill in PLATFORM MODE. "
            f"This is a multi-repo platform ({platform_id}) with {len(repos)} repositories. "
            "Your job is to: (1) generate a single shared PRODUCT.md for the entire platform, "
            "(2) generate a repo-specific CONTRIBUTING.md for each repo tailored to its tech stack, "
            "(3) prepare architect tasks that design the platform holistically across all repos. "
            "Be concrete and specific. Replace all placeholder comments with real content."
        )

        messages = [
            {
                "role": "user",
                "content": f"""
Bootstrap plan documents for a multi-repo platform.

## Product Brief
{product_brief}

## Platform Configuration
Platform ID: {platform_id}

### Repositories
{repo_summary}

## Project Config
{config_yaml[:2000] if config_yaml else "(not available)"}

## Current PRODUCT.md Stub
{product_stub}

## Current CONTRIBUTING.md Stub (from primary repo)
{contributing_stub}

## Instructions

### PRODUCT.md (shared across all repos)
Generate ONE PRODUCT.md that describes the entire platform. Populate every section:
1. **Overview** — What the platform is and why it exists (mention it spans multiple services)
2. **Target Users** — Who uses it and their key characteristics
3. **Core Value Proposition** — The problem it solves
4. **Key Features** — Bulleted list, noting which repo/service owns each feature
5. **Success Metrics** — Measurable KPIs
6. **Platform Architecture Summary** — Brief description of how the repos relate (API serves the UI, workers process async jobs, etc.)

### CONTRIBUTING.md (one per repo)
Generate a separate CONTRIBUTING.md for EACH repo with code style rules tailored to that repo's specific tech stack.
Keep the existing testing requirements, PR template, branch naming, and commit message sections.

### Architect Tasks
Prepare architect tasks that receive the FULL platform context so architecture is designed holistically:
- requirements-analysis: receives the full platform config + all repo roles
- architecture-design: designs per-repo ARCHITECTURE.md and DATA_MODEL.md with cross-references
- api-design: defines contracts that the UI consumes and events that workers process

Return strictly valid JSON:
{{
  "product_md": "Full shared PRODUCT.md content",
  "contributing_per_repo": {{
    "<repo>": "Full CONTRIBUTING.md content for that repo"
  }},
  "architect_tasks": [
    {{
      "skill_name": "requirements-analysis",
      "input": {{
        "product_brief": "...",
        "repo": "<primary repo>",
        "platform": {{
          "id": "{platform_id}",
          "repos": {json.dumps(repos)}
        }}
      }}
    }},
    {{
      "skill_name": "architecture-design",
      "input": {{
        "product_brief": "...",
        "repo": "<primary repo>",
        "platform": {{
          "id": "{platform_id}",
          "repos": {json.dumps(repos)}
        }}
      }},
      "depends_on": "requirements-analysis"
    }},
    {{
      "skill_name": "api-design",
      "input": {{
        "product_brief": "...",
        "repo": "<primary repo>",
        "platform": {{
          "id": "{platform_id}",
          "repos": {json.dumps(repos)}
        }}
      }},
      "depends_on": "requirements-analysis"
    }}
  ]
}}
""",
            }
        ]

        response = llm.complete(system_prompt, messages, max_tokens=8000, temperature=0.4)
        result = self._parse_json_response(response)

        result["repo"] = repo
        result["platform_id"] = platform_id
        result["platform_repos"] = [r.get("repo") or r.get("local_path", "unknown") for r in repos]
        result["documents_populated"] = {
            (r.get("repo") or r.get("local_path", "unknown")): ["PRODUCT.md", "CONTRIBUTING.md"] for r in repos
        }
        result.setdefault("generated_at", datetime.now(timezone.utc).isoformat())
        return result

    def _parse_json_response(self, text: str) -> dict:
        match = re.search(r"```(?:json)?\s*(\{.*?\})\s*```", text, re.DOTALL)
        if match:
            text = match.group(1)
        try:
            return json.loads(text)
        except json.JSONDecodeError:
            return {"raw_response": text, "parse_error": True}


# ---------------------------------------------------------------------------
# sprint-planning
# ---------------------------------------------------------------------------

class SprintPlanningSkill(Skill):
    """Transform product goals into sprint-ready Jira tickets."""

    @property
    def name(self) -> str:
        return "sprint-planning"

    @property
    def description(self) -> str:
        return "Decompose product goals into structured, sprint-ready Jira tickets"

    def execute(self, context: SkillContext, llm: LLMProvider) -> dict:
        goal = context.task.input_payload.get("goal_description", "")
        capacity = context.task.input_payload.get("sprint_capacity", None)
        arch_doc = context.plan_documents.get("ARCHITECTURE.md", "")
        contributing_doc = context.plan_documents.get("CONTRIBUTING.md", "")

        system_prompt = self.build_system_prompt(context)
        system_prompt += (
            "\n\nYou are the Forge PM Agent running the sprint-planning skill. "
            "Decompose the goal into specific, estimable Jira tickets. Every ticket "
            "must have acceptance criteria. Respect sprint capacity limits."
        )

        messages = [
            {
                "role": "user",
                "content": f"""
Decompose the following goal into sprint-ready tickets.

## Goal
{goal}

## Sprint Capacity
{f"{capacity} story points" if capacity else "(not specified)"}

## Architecture Context
{arch_doc[:2000] if arch_doc else "(not available)"}

## Contributing Guidelines
{contributing_doc[:1000] if contributing_doc else "(not available)"}

## Instructions
1. Identify Epics or Stories from the goal.
2. Break each Story into Tasks across agent roles (backend, frontend, QA, etc.).
3. Assign story point estimates and priorities.
4. Set dependency links between tickets.

Return strictly valid JSON:
{{
  "tickets": [
    {{
      "type": "Story|Task",
      "summary": "...",
      "description": "...",
      "acceptance_criteria": ["..."],
      "estimate_points": 0,
      "priority": "High|Medium|Low",
      "agent_role": "...",
      "depends_on": []
    }}
  ],
  "total_estimate_points": 0,
  "sprint_plan_summary": "..."
}}
""",
            }
        ]

        response = llm.complete(system_prompt, messages, max_tokens=8000, temperature=0.4)
        result = self._parse_json_response(response)
        result.setdefault("generated_at", datetime.now(timezone.utc).isoformat())
        return result

    def _parse_json_response(self, text: str) -> dict:
        match = re.search(r"```(?:json)?\s*(\{.*?\})\s*```", text, re.DOTALL)
        if match:
            text = match.group(1)
        try:
            return json.loads(text)
        except json.JSONDecodeError:
            return {"raw_response": text, "parse_error": True}


# ---------------------------------------------------------------------------
# status-reporting
# ---------------------------------------------------------------------------

class StatusReportingSkill(Skill):
    """Generate structured sprint status reports."""

    @property
    def name(self) -> str:
        return "status-reporting"

    @property
    def description(self) -> str:
        return "Generate sprint status reports from Jira data"

    def execute(self, context: SkillContext, llm: LLMProvider) -> dict:
        sprint_id = context.task.input_payload.get("sprint_id", "")
        audience = context.task.input_payload.get("report_audience", "team")

        system_prompt = self.build_system_prompt(context)
        system_prompt += (
            "\n\nYou are the Forge PM Agent running the status-reporting skill. "
            f"Generate a status report for audience: {audience}. "
            "Be data-driven. Highlight blockers and risks prominently."
        )

        messages = [
            {
                "role": "user",
                "content": f"""
Generate a sprint status report for sprint `{sprint_id}`.

## Audience
{audience} (adjust detail level accordingly)

## Instructions
1. Categorise tickets: To Do, In Progress, In Review, Done, Blocked.
2. Calculate completion percentage and velocity.
3. Identify risks and blockers.
4. Produce a narrative summary appropriate for the audience.

Return strictly valid JSON:
{{
  "sprint_id": "{sprint_id}",
  "completion_pct": 0.0,
  "velocity": 0,
  "categories": {{"todo": 0, "in_progress": 0, "in_review": 0, "done": 0, "blocked": 0}},
  "blockers": ["..."],
  "risks": ["..."],
  "narrative": "...",
  "report_markdown": "..."
}}
""",
            }
        ]

        response = llm.complete(system_prompt, messages, max_tokens=6000, temperature=0.3)
        result = self._parse_json_response(response)
        result.setdefault("generated_at", datetime.now(timezone.utc).isoformat())
        return result

    def _parse_json_response(self, text: str) -> dict:
        match = re.search(r"```(?:json)?\s*(\{.*?\})\s*```", text, re.DOTALL)
        if match:
            text = match.group(1)
        try:
            return json.loads(text)
        except json.JSONDecodeError:
            return {"raw_response": text, "parse_error": True}


# ---------------------------------------------------------------------------
# ticket-triage
# ---------------------------------------------------------------------------

class TicketTriageSkill(Skill):
    """Review incoming tickets for completeness and route to agents."""

    @property
    def name(self) -> str:
        return "ticket-triage"

    @property
    def description(self) -> str:
        return "Triage incoming Jira tickets — verify completeness, assign priority, route to agents"

    def execute(self, context: SkillContext, llm: LLMProvider) -> dict:
        ticket_ids = context.task.input_payload.get("ticket_ids", [])

        system_prompt = self.build_system_prompt(context)
        system_prompt += (
            "\n\nYou are the Forge PM Agent running the ticket-triage skill. "
            "Review each ticket for completeness (summary, description, acceptance criteria). "
            "Assign priority based on business impact. Route to the correct agent role."
        )

        messages = [
            {
                "role": "user",
                "content": f"""
Triage the following tickets: {json.dumps(ticket_ids)}

## Instructions
1. For each ticket, verify completeness (summary, description, acceptance criteria, type, priority).
2. Assign priority based on business impact and dependencies.
3. Route to the correct agent role based on ticket type and labels.
4. Flag incomplete tickets with specific clarification questions.

Return strictly valid JSON:
{{
  "triage_results": [
    {{
      "ticket_id": "...",
      "is_complete": true,
      "assigned_priority": "Critical|High|Medium|Low",
      "assigned_agent_role": "...",
      "missing_fields": [],
      "clarification_questions": []
    }}
  ],
  "incomplete_count": 0,
  "routed_count": 0
}}
""",
            }
        ]

        response = llm.complete(system_prompt, messages, max_tokens=6000, temperature=0.3)
        result = self._parse_json_response(response)
        result.setdefault("generated_at", datetime.now(timezone.utc).isoformat())
        return result

    def _parse_json_response(self, text: str) -> dict:
        match = re.search(r"```(?:json)?\s*(\{.*?\})\s*```", text, re.DOTALL)
        if match:
            text = match.group(1)
        try:
            return json.loads(text)
        except json.JSONDecodeError:
            return {"raw_response": text, "parse_error": True}


# ---------------------------------------------------------------------------
# Agent
# ---------------------------------------------------------------------------

class PMAgent(BaseAgent):
    """
    PM Agent — product management, sprint planning, and project bootstrap.

    Decomposes product goals into actionable tickets, triages incoming work,
    reports on sprint progress, and orchestrates initial project setup by
    collaborating with the Architect agent to populate plan documents.
    """

    @property
    def role(self) -> str:
        return "pm"

    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.register_skill(ProjectBootstrapSkill())
        self.register_skill(SprintPlanningSkill())
        self.register_skill(StatusReportingSkill())
        self.register_skill(TicketTriageSkill())
