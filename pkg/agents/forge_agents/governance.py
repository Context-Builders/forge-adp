"""
Governance Agent implementation.

Three skills:
  - compliance-report        : audit-log analysis, policy adherence, cost & quality metrics
  - change-risk-assessment   : risk scoring for high-impact tasks before they are queued
  - policy-drift-detection   : compare observed agent actions against OPA rules; surface gaps
"""
import json
import re
from datetime import datetime, timezone

from .runtime import BaseAgent, Skill, SkillContext, LLMProvider


# ---------------------------------------------------------------------------
# compliance-report
# ---------------------------------------------------------------------------

class ComplianceReportSkill(Skill):
    """Analyse the audit log and produce a structured compliance report."""

    @property
    def name(self) -> str:
        return "compliance-report"

    @property
    def description(self) -> str:
        return "Produce a compliance report covering policy adherence, quality gates, and cost"

    def execute(self, context: SkillContext, llm: LLMProvider) -> dict:
        period = context.task.input_payload.get("period", "P7D")
        project_id = context.task.input_payload.get("project_id", "")

        security_policy = context.plan_documents.get("SECURITY_POLICY.md", "")
        config_yaml = context.plan_documents.get("config.yaml", "")

        system_prompt = self.build_system_prompt(context)
        system_prompt += (
            "\n\nYou are the Forge Governance Agent running the compliance-report skill. "
            "Your job is to produce a thorough, structured compliance report. "
            "Be precise, cite data, and flag any finding that requires human attention."
        )

        messages = [
            {
                "role": "user",
                "content": f"""
Produce a compliance report for project `{project_id}` covering period `{period}`.

## Security Policy Context
{security_policy[:3000] if security_policy else "(not available)"}

## Project Config
{config_yaml[:2000] if config_yaml else "(not available)"}

## Instructions
1. Summarise policy adherence per agent role (allow vs deny rates).
2. Summarise quality gate pass/fail rates.
3. Analyse LLM cost actuals vs budget limits; project the remaining month.
4. Report human-in-the-loop approval rates and median latency.
5. List any Critical or High findings with recommended Jira ticket titles.
6. Conclude with an executive summary (≤ 150 words).

Return strictly valid JSON matching this schema:
{{
  "period": "<ISO interval>",
  "policy_adherence": {{"<role>": <float 0-1>}},
  "quality_gate_pass_rates": {{"<gate>": <float 0-1>}},
  "cost_usd": <float>,
  "budget_utilisation": <float 0-1>,
  "hitl_approval_rate": <float 0-1>,
  "hitl_median_latency_hours": <float>,
  "critical_findings": [{{"title": "...", "detail": "..."}}],
  "executive_summary": "...",
  "report_markdown": "..."
}}
""",
            }
        ]

        response = llm.complete(system_prompt, messages, max_tokens=8000, temperature=0.3)
        result = self._parse_json_response(response)
        result.setdefault("generated_at", datetime.now(timezone.utc).isoformat())
        return result

    # ------------------------------------------------------------------

    def _parse_json_response(self, text: str) -> dict:
        """Extract JSON from the LLM response, stripping markdown fences."""
        match = re.search(r"```(?:json)?\s*(\{.*?\})\s*```", text, re.DOTALL)
        if match:
            text = match.group(1)
        try:
            return json.loads(text)
        except json.JSONDecodeError:
            return {"raw_response": text, "parse_error": True}


# ---------------------------------------------------------------------------
# change-risk-assessment
# ---------------------------------------------------------------------------

class ChangeRiskAssessmentSkill(Skill):
    """Score the risk of a proposed high-impact change and recommend approve / conditional / reject."""

    @property
    def name(self) -> str:
        return "change-risk-assessment"

    @property
    def description(self) -> str:
        return "Evaluate risk of a proposed task and produce a score + approval recommendation"

    # Risk score thresholds
    APPROVE_THRESHOLD = 4.0
    REJECT_THRESHOLD = 8.0

    def execute(self, context: SkillContext, llm: LLMProvider) -> dict:
        task_payload = context.task.input_payload.get("task_payload", {})
        agent_role = context.task.input_payload.get("agent_role", "unknown")
        jira_ticket = context.task.input_payload.get("jira_ticket", "")

        arch_doc = context.plan_documents.get("ARCHITECTURE.md", "")
        infra_doc = context.plan_documents.get("INFRASTRUCTURE.md", "")
        security_doc = context.plan_documents.get("SECURITY_POLICY.md", "")

        system_prompt = self.build_system_prompt(context)
        system_prompt += (
            "\n\nYou are the Forge Governance Agent running the change-risk-assessment skill. "
            "Apply a structured scoring rubric. Never replace the rubric with subjective opinion. "
            f"Score thresholds: approve < {self.APPROVE_THRESHOLD}, "
            f"conditional {self.APPROVE_THRESHOLD}–{self.REJECT_THRESHOLD}, "
            f"reject >= {self.REJECT_THRESHOLD}."
        )

        messages = [
            {
                "role": "user",
                "content": f"""
Assess the risk of the following proposed change.

## Agent Role
{agent_role}

## Task Payload
{json.dumps(task_payload, indent=2)}

## Architecture Context
{arch_doc[:2000] if arch_doc else "(not available)"}

## Infrastructure Context
{infra_doc[:1500] if infra_doc else "(not available)"}

## Security Policy
{security_doc[:1500] if security_doc else "(not available)"}

## Jira Ticket
{jira_ticket or "(none)"}

## Instructions
Score each dimension 0–10:
- blast_radius: how many services/users are affected
- reversibility: how easily the change can be rolled back
- prerequisite_coverage: fraction of required pre-checks that are confirmed

Overall risk score = (blast_radius * 0.45) + ((10 - reversibility) * 0.35) + ((10 - prerequisite_coverage * 10) * 0.20)

Return strictly valid JSON:
{{
  "risk_score": <float 0-10>,
  "change_type": "...",
  "blast_radius_score": <float>,
  "reversibility_score": <float>,
  "prerequisite_coverage_score": <float>,
  "blast_radius": {{"services": [...], "environments": [...]}},
  "prerequisite_checks": {{"rollback_plan": <bool>, "maintenance_window": <bool>, "feature_flags": <bool>}},
  "approval_recommendation": "approve|conditional|reject",
  "mitigations": ["..."],
  "report_markdown": "..."
}}
""",
            }
        ]

        response = llm.complete(system_prompt, messages, max_tokens=6000, temperature=0.2)
        result = self._parse_json_response(response)

        # Enforce hard reject rule regardless of LLM output
        score = result.get("risk_score", 10)
        if score >= self.REJECT_THRESHOLD:
            result["approval_recommendation"] = "reject"
        elif score >= self.APPROVE_THRESHOLD:
            result["approval_recommendation"] = "conditional"
        else:
            result["approval_recommendation"] = "approve"

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
# policy-drift-detection
# ---------------------------------------------------------------------------

class PolicyDriftDetectionSkill(Skill):
    """Compare observed agent actions against OPA rules and surface gaps."""

    @property
    def name(self) -> str:
        return "policy-drift-detection"

    @property
    def description(self) -> str:
        return "Surface divergences between OPA policy rules and real observed agent behaviour"

    def execute(self, context: SkillContext, llm: LLMProvider) -> dict:
        period = context.task.input_payload.get("period", "P30D")
        project_id = context.task.input_payload.get("project_id", "")

        security_doc = context.plan_documents.get("SECURITY_POLICY.md", "")
        arch_doc = context.plan_documents.get("ARCHITECTURE.md", "")

        system_prompt = self.build_system_prompt(context)
        system_prompt += (
            "\n\nYou are the Forge Governance Agent running the policy-drift-detection skill. "
            "Identify policy gaps systematically. Only recommend new `deny` rules or explicit "
            "scoping — never suggest broadening permissions. Filter out noise (frequency < 3)."
        )

        messages = [
            {
                "role": "user",
                "content": f"""
Analyse policy drift for project `{project_id}` over period `{period}`.

## Security Policy Context
{security_doc[:3000] if security_doc else "(not available)"}

## Architecture Context
{arch_doc[:2000] if arch_doc else "(not available)"}

## Instructions
1. For each agent role, identify (role, action) pairs that are implicitly allowed (no explicit OPA rule).
2. Score each gap by severity: Critical (≥ 7), High (5–7), Medium (3–5), Low (< 3).
3. For Critical/High gaps, suggest a specific OPA rule change (`deny` or explicit scoping).
4. Indicate whether each gap is auto-remediable (can be fixed with a PR) or requires human review.
5. Compute an overall drift_score_delta vs the previous period (estimate if no prior data: 0.0).

Return strictly valid JSON:
{{
  "period": "...",
  "roles_analysed": ["..."],
  "policy_gaps": [
    {{
      "role": "...",
      "action": "...",
      "resource": "...",
      "frequency": <int>,
      "severity": "Critical|High|Medium|Low",
      "gap_type": "implicitly_allowed|policy_missing|scope_too_broad",
      "recommendation": "...",
      "auto_remediable": <bool>,
      "suggested_opa_rule": "..."
    }}
  ],
  "remediation_prs": [],
  "drift_score_delta": <float>,
  "executive_summary": "..."
}}
""",
            }
        ]

        response = llm.complete(system_prompt, messages, max_tokens=8000, temperature=0.2)
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

class GovernanceAgent(BaseAgent):
    """
    Governance Agent — cross-cutting compliance, risk, and policy oversight.

    Operates as a peer agent: constrained by the Policy Engine like all other
    agents, but its scope spans all roles. It never executes production changes
    directly; it produces reports, risk scores, and PRs against the OPA bundle.
    """

    @property
    def role(self) -> str:
        return "governance"

    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.register_skill(ComplianceReportSkill())
        self.register_skill(ChangeRiskAssessmentSkill())
        self.register_skill(PolicyDriftDetectionSkill())
