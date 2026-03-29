"""
Database Administrator (DBA) Agent implementation.
"""
from .runtime import BaseAgent, Skill, SkillContext, LLMProvider, AgentIdentity


class SchemaMigrationSkill(Skill):
    """Design and generate production-safe DB schema migrations."""

    @property
    def name(self) -> str:
        return "schema-migration"

    @property
    def description(self) -> str:
        return "Design and generate production-safe DB schema migrations"

    def execute(self, context: SkillContext, llm: LLMProvider) -> dict:
        system_prompt = self.build_system_prompt(context)

        ticket_id = context.task.input_payload.get("ticket_id", "")
        description = context.task.input_payload.get("description", "")
        changes_required = context.task.input_payload.get("changes_required", [])

        data_model = context.plan_documents.get("DATA_MODEL.md", "")
        db_conventions = context.plan_documents.get("DB_CONVENTIONS.md", "")

        changes_text = "\n".join(
            f"- {change}" for change in changes_required
        ) if changes_required else "No specific changes listed."

        messages = [
            {
                "role": "user",
                "content": f"""Generate production-safe database schema migrations for the following ticket.

## Ticket
ID: {ticket_id}
Description: {description}

## Schema Changes Required
{changes_text}

## Current Data Model
{data_model}

## DB Conventions
{db_conventions}

Produce both an up migration (applying the change) and a down migration (reverting it).
Follow the project's naming conventions and safety guidelines strictly.

Respond with:
1. migration_name: a descriptive, timestamped-style name (e.g. add_users_email_index)
2. up_sql: the full SQL for the up migration
3. down_sql: the full SQL for the down migration
4. risk_assessment: identify any locking risks, data loss potential, or performance impact
5. rollback_plan: step-by-step instructions to safely roll back if something goes wrong
6. questions: any clarifying questions before proceeding
"""
            }
        ]

        response = llm.complete(system_prompt, messages, max_tokens=6000)

        return {
            "up_sql": self._extract_section(response, "up_sql"),
            "down_sql": self._extract_section(response, "down_sql"),
            "migration_name": self._extract_section(response, "migration_name"),
            "risk_assessment": self._extract_section(response, "risk_assessment"),
            "rollback_plan": self._extract_section(response, "rollback_plan"),
            "questions": self._extract_questions(response),
            "raw_response": response,
        }

    def _extract_section(self, response: str, section: str) -> str:
        """Extract a named section from the LLM response (best-effort)."""
        # TODO: parse structured sections from response
        return ""

    def _extract_questions(self, response: str) -> list[str]:
        """Extract any clarifying questions from the LLM response."""
        # TODO: parse questions from response
        return []


class QueryOptimizationSkill(Skill):
    """Analyze slow queries and produce optimizations."""

    @property
    def name(self) -> str:
        return "query-optimization"

    @property
    def description(self) -> str:
        return "Analyze slow queries and suggest index changes and query rewrites"

    def execute(self, context: SkillContext, llm: LLMProvider) -> dict:
        system_prompt = self.build_system_prompt(context)

        ticket_id = context.task.input_payload.get("ticket_id", "")
        slow_queries = context.task.input_payload.get("slow_queries", [])
        explain_output = context.task.input_payload.get("explain_output", "")

        data_model = context.plan_documents.get("DATA_MODEL.md", "")

        queries_text = ""
        for i, q in enumerate(slow_queries, start=1):
            sql = q.get("sql", "")
            avg_ms = q.get("avg_ms", "unknown")
            table = q.get("table", "unknown")
            queries_text += f"\n### Query {i}\nTable: {table}\nAvg execution time: {avg_ms}ms\n```sql\n{sql}\n```\n"

        explain_section = ""
        if explain_output:
            explain_section = f"\n## EXPLAIN Output\n```\n{explain_output}\n```"

        messages = [
            {
                "role": "user",
                "content": f"""Analyze the following slow queries and produce concrete optimization recommendations.

## Ticket
ID: {ticket_id}

## Slow Queries
{queries_text}{explain_section}

## Data Model
{data_model}

For each query provide:
1. original_sql: the original query as provided
2. optimized_sql: a rewritten version (if applicable)
3. explanation: why this optimization helps
4. index_recommendations: specific CREATE INDEX statements to add

Also provide an overall summary of findings and priorities.
"""
            }
        ]

        response = llm.complete(system_prompt, messages, max_tokens=6000)

        return {
            "optimizations": self._extract_optimizations(response, slow_queries),
            "summary": self._extract_summary(response),
            "raw_response": response,
        }

    def _extract_optimizations(self, response: str, slow_queries: list) -> list[dict]:
        """Extract per-query optimization recommendations (best-effort)."""
        # TODO: parse structured optimizations from response
        return [
            {
                "original_sql": q.get("sql", ""),
                "optimized_sql": "",
                "explanation": "",
                "index_recommendations": [],
            }
            for q in slow_queries
        ]

    def _extract_summary(self, response: str) -> str:
        """Extract the overall summary from the LLM response."""
        # TODO: parse summary from response
        return ""


class MigrationExecutionSkill(Skill):
    """Validate and execute pending migrations safely."""

    @property
    def name(self) -> str:
        return "migration-execution"

    @property
    def description(self) -> str:
        return "Review a migration for safety and produce an execution checklist"

    def execute(self, context: SkillContext, llm: LLMProvider) -> dict:
        system_prompt = self.build_system_prompt(context)

        ticket_id = context.task.input_payload.get("ticket_id", "")
        migration_file = context.task.input_payload.get("migration_file", "")
        target_environment = context.task.input_payload.get("target_environment", "")
        pre_migration_checks = context.task.input_payload.get("pre_migration_checks", [])

        db_conventions = context.plan_documents.get("DB_CONVENTIONS.md", "")

        checks_text = "\n".join(
            f"- {check}" for check in pre_migration_checks
        ) if pre_migration_checks else "No pre-migration checks specified."

        messages = [
            {
                "role": "user",
                "content": f"""Review the following database migration for production safety and produce an execution checklist.

## Ticket
ID: {ticket_id}

## Migration File
{migration_file}

## Target Environment
{target_environment}

## Pre-Migration Checks Requested
{checks_text}

## DB Conventions
{db_conventions}

Evaluate the migration for:
- Irreversible operations (DROP TABLE, DROP COLUMN, TRUNCATE, etc.)
- Lock risks that could cause downtime (ALTER TABLE on large tables, etc.)
- Potential data loss
- Missing rollback path

Respond with:
1. safety_assessment: a narrative assessment of risks found
2. execution_checklist: an ordered list of steps to execute the migration safely
3. estimated_downtime: best estimate of downtime impact (e.g. "< 1s", "5-10 minutes", "unknown")
4. approved: true if safe to proceed, false if concerns must be resolved first
5. concerns: a list of specific issues that must be addressed before running
"""
            }
        ]

        response = llm.complete(system_prompt, messages, max_tokens=4000)

        return {
            "safety_assessment": self._extract_safety_assessment(response),
            "execution_checklist": self._extract_checklist(response),
            "estimated_downtime": self._extract_downtime(response),
            "approved": self._extract_approved(response),
            "concerns": self._extract_concerns(response),
            "raw_response": response,
        }

    def _extract_safety_assessment(self, response: str) -> str:
        """Extract the safety assessment narrative."""
        # TODO: parse from response
        return ""

    def _extract_checklist(self, response: str) -> list[str]:
        """Extract the ordered execution checklist."""
        # TODO: parse from response
        return []

    def _extract_downtime(self, response: str) -> str:
        """Extract the estimated downtime."""
        # TODO: parse from response
        return "unknown"

    def _extract_approved(self, response: str) -> bool:
        """Extract the approval decision."""
        # TODO: parse from response
        return False

    def _extract_concerns(self, response: str) -> list[str]:
        """Extract the list of concerns."""
        # TODO: parse from response
        return []


class DBAAgent(BaseAgent):
    """Database Administrator Agent - manages schema migrations, query optimization, and migration execution."""

    @property
    def role(self) -> str:
        return "dba"

    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)

        # Register skills
        self.register_skill(SchemaMigrationSkill())
        self.register_skill(QueryOptimizationSkill())
        self.register_skill(MigrationExecutionSkill())
