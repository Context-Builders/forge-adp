"""
Architect Agent implementation.

Three skills:
  - requirements-analysis  : translate product brief into structured requirements
  - architecture-design    : design system architecture and data model
  - api-design             : design API surface and endpoint specifications
"""
import json
import re
from datetime import datetime, timezone

from .runtime import BaseAgent, Skill, SkillContext, LLMProvider


# ---------------------------------------------------------------------------
# requirements-analysis
# ---------------------------------------------------------------------------

class RequirementsAnalysisSkill(Skill):
    """Translate a product brief into structured functional and non-functional requirements."""

    @property
    def name(self) -> str:
        return "requirements-analysis"

    @property
    def description(self) -> str:
        return "Extract functional and non-functional requirements from a product brief"

    def _build_platform_context(self, platform: dict) -> str:
        """Build a platform context string for LLM prompts."""
        if not platform:
            return ""
        repos = platform.get("repos", [])
        lines = [
            f"\n## Platform Context",
            f"Platform ID: {platform.get('id', 'unknown')}",
            f"This is a multi-repo platform with {len(repos)} repositories:",
        ]
        for r in repos:
            source = r.get("local_path") or r.get("repo", "unknown")
            lines.append(f"- **{source}** (role: {r.get('role', 'unknown')})")
        lines.append("\nEach repo's tech stack is defined in its own .forge/config.yaml.")
        return "\n".join(lines)

    def execute(self, context: SkillContext, llm: LLMProvider) -> dict:
        product_brief = context.task.input_payload.get("product_brief", "")
        product_md = context.plan_documents.get("PRODUCT.md", "")
        config_yaml = context.plan_documents.get("config.yaml", "")
        platform = context.task.input_payload.get("platform")
        platform_context = self._build_platform_context(platform)

        system_prompt = self.build_system_prompt(context)
        system_prompt += (
            "\n\nYou are the Forge Architect Agent running the requirements-analysis skill. "
            "Your job is to extract structured, testable requirements from the product brief. "
            "Be thorough but do not invent features not present in the brief. "
            "Flag assumptions and open questions explicitly."
        )
        if platform:
            system_prompt += (
                " This is a MULTI-REPO PLATFORM. Requirements must be tagged with which "
                "repo role (api, workers, ui, etc.) they belong to. Cross-repo requirements "
                "(e.g. API contract between api and ui) must be identified explicitly."
            )

        platform_instructions = ""
        platform_json_fields = ""
        if platform:
            platform_instructions = """
6. Tag each functional requirement with the repo role(s) it applies to.
7. Identify cross-repo requirements (e.g. "UI must consume the payments API", "workers must process payment events from the API").
8. Define non-functional requirements per repo where they differ (e.g. UI latency vs worker throughput)."""
            platform_json_fields = """,
  "cross_repo_requirements": [
    {
      "id": "XR-001",
      "from_repo_role": "api",
      "to_repo_role": "ui",
      "description": "...",
      "contract_type": "rest_api|event|shared_type"
    }
  ]"""

        messages = [
            {
                "role": "user",
                "content": f"""
Analyse the following product brief and produce structured requirements.

## Product Brief
{product_brief}

## Product Vision (if available)
{product_md[:3000] if product_md else "(not yet populated)"}

## Project Config
{config_yaml[:2000] if config_yaml else "(not available)"}
{platform_context}

## Instructions
1. Extract functional requirements — each must be specific, testable, and prioritised (Must/Should/Could).
2. Define non-functional requirements covering at minimum: performance, security, availability, scalability.
3. List constraints imposed by the chosen tech stack.
4. List assumptions you are making.
5. Surface open questions that need product owner clarification.
{platform_instructions}

Return strictly valid JSON:
{{
  "functional_requirements": [
    {{
      "id": "REQ-001",
      "description": "...",
      "priority": "Must|Should|Could",
      "acceptance_criteria": ["..."],
      "source": "product_brief",
      "repo_roles": ["api"]
    }}
  ],
  "non_functional_requirements": [
    {{
      "id": "NFR-001",
      "category": "performance|security|scalability|availability|compliance",
      "description": "...",
      "target": "...",
      "rationale": "...",
      "repo_roles": ["api", "ui"]
    }}
  ],
  "constraints": ["..."],
  "assumptions": ["..."],
  "open_questions": ["..."]{platform_json_fields}
}}
""",
            }
        ]

        response = llm.complete(system_prompt, messages, max_tokens=8000, temperature=0.3)
        result = self._parse_json_response(response)
        result.setdefault("generated_at", datetime.now(timezone.utc).isoformat())
        if platform:
            result["platform_id"] = platform.get("id")
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
# architecture-design
# ---------------------------------------------------------------------------

class ArchitectureDesignSkill(Skill):
    """Design system architecture and data model, populating ARCHITECTURE.md and DATA_MODEL.md."""

    @property
    def name(self) -> str:
        return "architecture-design"

    @property
    def description(self) -> str:
        return "Design system architecture and data model from requirements"

    def _build_platform_context(self, platform: dict) -> str:
        if not platform:
            return ""
        repos = platform.get("repos", [])
        lines = [
            f"\n## Platform Context",
            f"Platform ID: {platform.get('id', 'unknown')}",
            f"This is a multi-repo platform with {len(repos)} repositories:",
        ]
        for r in repos:
            source = r.get("local_path") or r.get("repo", "unknown")
            lines.append(f"- **{source}** (role: {r.get('role', 'unknown')})")
        lines.append("\nEach repo's tech stack is defined in its own .forge/config.yaml.")
        return "\n".join(lines)

    def execute(self, context: SkillContext, llm: LLMProvider) -> dict:
        requirements = context.task.input_payload.get("requirements", {})
        product_brief = context.task.input_payload.get("product_brief", "")
        config_yaml = context.plan_documents.get("config.yaml", "")
        arch_stub = context.plan_documents.get("ARCHITECTURE.md", "")
        data_model_stub = context.plan_documents.get("DATA_MODEL.md", "")
        platform = context.task.input_payload.get("platform")
        platform_context = self._build_platform_context(platform)

        system_prompt = self.build_system_prompt(context)
        system_prompt += (
            "\n\nYou are the Forge Architect Agent running the architecture-design skill. "
            "Design a practical, right-sized architecture. Match complexity to requirements — "
            "do not over-architect. Every decision must include rationale."
        )
        if platform:
            system_prompt += (
                " This is a MULTI-REPO PLATFORM. You must design a holistic architecture "
                "spanning all repos, then produce per-repo ARCHITECTURE.md and DATA_MODEL.md "
                "documents. Each repo's docs must cross-reference its siblings. "
                "Only repos with a database need a DATA_MODEL.md."
            )

        if platform:
            repos = platform.get("repos", [])
            arch_output_spec = (
                '"architecture_per_repo": {\n'
                + "\n".join(f'    "{r.get("repo") or r.get("local_path", "unknown")}": "Full ARCHITECTURE.md content for {r["role"]} repo",' for r in repos)
                + '\n  },'
                + '\n  "data_model_per_repo": {\n'
                + '    "Include an entry for each repo that has a database (check each repo\'s .forge/config.yaml stack.database field)": "..."\n'
                + '  },'
            )
            platform_instructions = f"""
5. Design a holistic platform architecture showing how all {len(repos)} repos interact.
6. For EACH repo, produce a separate ARCHITECTURE.md that:
   - Describes that repo's specific responsibilities and boundaries
   - References sibling repos and how they communicate (REST APIs, events/messages, shared types)
   - Uses the tech stack from that repo's config
7. For repos with a database, produce a DATA_MODEL.md.
8. Define shared contracts: API schemas the UI consumes, event schemas the workers consume."""
        else:
            arch_output_spec = (
                '"architecture_md": "Full markdown content for ARCHITECTURE.md",\n'
                '  "data_model_md": "Full markdown content for DATA_MODEL.md",'
            )
            platform_instructions = ""

        messages = [
            {
                "role": "user",
                "content": f"""
Design the system architecture and data model for this project.

## Product Brief
{product_brief[:2000]}

## Requirements
{json.dumps(requirements, indent=2)[:4000]}

## Project Config
{config_yaml[:2000] if config_yaml else "(not available)"}
{platform_context}

## Current ARCHITECTURE.md Stub
{arch_stub[:1000]}

## Current DATA_MODEL.md Stub
{data_model_stub[:500]}

## Instructions
1. Write complete ARCHITECTURE.md content covering: system overview, tech stack, service boundaries, API design patterns, error handling, security (auth/authz), and data flow.
2. Write complete DATA_MODEL.md content covering: overview, entity definitions with attributes and types, entity relationships, and migration strategy.
3. List key architecture decisions with rationale and alternatives considered.
4. Replace ALL placeholder comments with substantive content.
{platform_instructions}

Return strictly valid JSON:
{{
  {arch_output_spec}
  "entities": [
    {{
      "name": "...",
      "attributes": [{{"name": "...", "type": "...", "constraints": "..."}}],
      "relationships": [{{"target": "...", "type": "one-to-many", "description": "..."}}],
      "repo_role": "api"
    }}
  ],
  "architecture_decisions": [
    {{
      "decision": "...",
      "rationale": "...",
      "alternatives_considered": ["..."]
    }}
  ]
}}
""",
            }
        ]

        response = llm.complete(system_prompt, messages, max_tokens=8000, temperature=0.4)
        result = self._parse_json_response(response)
        result.setdefault("generated_at", datetime.now(timezone.utc).isoformat())
        if platform:
            result["platform_id"] = platform.get("id")
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
# api-design
# ---------------------------------------------------------------------------

class APIDesignSkill(Skill):
    """Design the API surface and populate API_CONTRACTS.md."""

    @property
    def name(self) -> str:
        return "api-design"

    @property
    def description(self) -> str:
        return "Design RESTful API endpoints and populate API contracts"

    def _build_platform_context(self, platform: dict) -> str:
        if not platform:
            return ""
        repos = platform.get("repos", [])
        lines = [
            f"\n## Platform Context",
            f"Platform ID: {platform.get('id', 'unknown')}",
            f"This is a multi-repo platform with {len(repos)} repositories:",
        ]
        for r in repos:
            source = r.get("local_path") or r.get("repo", "unknown")
            lines.append(f"- **{source}** (role: {r.get('role', 'unknown')})")
        lines.append("\nEach repo's tech stack is defined in its own .forge/config.yaml.")
        return "\n".join(lines)

    def execute(self, context: SkillContext, llm: LLMProvider) -> dict:
        requirements = context.task.input_payload.get("requirements", {})
        entities = context.task.input_payload.get("entities", [])
        product_brief = context.task.input_payload.get("product_brief", "")
        config_yaml = context.plan_documents.get("config.yaml", "")
        api_stub = context.plan_documents.get("API_CONTRACTS.md", "")
        platform = context.task.input_payload.get("platform")
        platform_context = self._build_platform_context(platform)

        system_prompt = self.build_system_prompt(context)
        system_prompt += (
            "\n\nYou are the Forge Architect Agent running the api-design skill. "
            "Design a consistent, RESTful API surface. Preserve the existing response envelope "
            "and error format from the stub. Every endpoint must specify auth requirements."
        )
        if platform:
            system_prompt += (
                " This is a MULTI-REPO PLATFORM. The API repo's contracts are the source of truth. "
                "Also define event schemas that workers consume and client SDK types the UI uses. "
                "Produce per-repo API_CONTRACTS.md where applicable."
            )

        if platform:
            repos = platform.get("repos", [])
            api_repos = [r for r in repos if r.get("role") in ("api", "workers")]
            api_output_spec = (
                '"api_contracts_per_repo": {\n'
                + "\n".join(f'    "{r.get("repo") or r.get("local_path", "unknown")}": "Full API_CONTRACTS.md content for {r["role"]} repo",' for r in api_repos)
                + '\n  },'
                + '\n  "event_schemas": [\n'
                + '    {"name": "...", "producer_repo_role": "api", "consumer_repo_roles": ["workers"], "schema": {}}\n'
                + '  ],'
            )
            platform_instructions = f"""
7. Design the API contracts for the API repo as the canonical source of truth.
8. Define event/message schemas that the workers repo consumes (e.g. payment.completed, order.created).
9. For the workers repo, document the events it subscribes to and the processing contracts.
10. The UI repo does not need its own API_CONTRACTS.md — it consumes the API repo's contracts."""
        else:
            api_output_spec = '"api_contracts_md": "Full markdown content for API_CONTRACTS.md",'
            platform_instructions = ""

        messages = [
            {
                "role": "user",
                "content": f"""
Design the API surface for this project.

## Product Brief
{product_brief[:2000]}

## Requirements
{json.dumps(requirements, indent=2)[:4000]}

## Known Entities
{json.dumps(entities, indent=2)[:2000] if entities else "(pending from architecture-design)"}

## Project Config
{config_yaml[:2000] if config_yaml else "(not available)"}
{platform_context}

## Current API_CONTRACTS.md Stub
{api_stub}

## Instructions
1. Preserve the existing versioning strategy, response envelope, error format, and status codes table from the stub.
2. Design RESTful endpoints for each core resource derived from the requirements.
3. For each endpoint specify: method, path, description, auth requirement, request schema, response schema, and applicable status codes.
4. Include pagination parameters for list endpoints.
5. Include at least one request/response example per resource group.
6. Replace the placeholder comment in the Endpoints section with full specifications.
{platform_instructions}

Return strictly valid JSON:
{{
  {api_output_spec}
  "endpoints": [
    {{
      "method": "GET|POST|PUT|PATCH|DELETE",
      "path": "/api/v1/...",
      "description": "...",
      "auth_required": true,
      "request_schema": {{}},
      "response_schema": {{}},
      "status_codes": [200, 400, 401, 404],
      "repo_role": "api"
    }}
  ],
  "resource_count": 0,
  "endpoint_count": 0
}}
""",
            }
        ]

        response = llm.complete(system_prompt, messages, max_tokens=8000, temperature=0.3)
        result = self._parse_json_response(response)
        result.setdefault("generated_at", datetime.now(timezone.utc).isoformat())
        if platform:
            result["platform_id"] = platform.get("id")
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

class ArchitectAgent(BaseAgent):
    """
    Architect Agent — technical architecture, requirements analysis, and API design.

    Collaborates with the PM agent during project bootstrap to translate product
    vision into technical plans. Produces ARCHITECTURE.md, DATA_MODEL.md, and
    API_CONTRACTS.md content. Does not implement code directly; provides the
    technical foundation that development agents build upon.
    """

    @property
    def role(self) -> str:
        return "architect"

    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.register_skill(RequirementsAnalysisSkill())
        self.register_skill(ArchitectureDesignSkill())
        self.register_skill(APIDesignSkill())
