"""
SRE (Site Reliability Engineering) Agent implementation.
"""
from .runtime import BaseAgent, Skill, SkillContext, LLMProvider, AgentIdentity


class IncidentResponseSkill(Skill):
    """Coordinate incident detection, triage, mitigation, and post-mortem."""

    @property
    def name(self) -> str:
        return "incident-response"

    @property
    def description(self) -> str:
        return "Coordinate incident detection, triage, mitigation, and post-mortem"

    def execute(self, context: SkillContext, llm: LLMProvider) -> dict:
        system_prompt = self.build_system_prompt(context)

        payload = context.task.input_payload
        ticket_id = payload.get("ticket_id", context.task.jira_ticket_id)
        incident_title = payload.get("incident_title", "")
        severity = payload.get("severity", "p2")
        affected_services = payload.get("affected_services", [])
        symptoms = payload.get("symptoms", [])
        metrics_summary = payload.get("metrics_summary", "")
        timeline = payload.get("timeline", [])

        observability_doc = context.plan_documents.get("OBSERVABILITY.md", "")
        architecture_doc = context.plan_documents.get("ARCHITECTURE.md", "")

        affected_services_text = "\n".join(f"- {s}" for s in affected_services) if affected_services else "- (none specified)"
        symptoms_text = "\n".join(f"- {s}" for s in symptoms) if symptoms else "- (none specified)"

        timeline_text = ""
        if timeline:
            timeline_lines = [f"- [{entry.get('time', '?')}] {entry.get('event', '')}" for entry in timeline]
            timeline_text = f"\n## Incident Timeline\n" + "\n".join(timeline_lines)

        metrics_text = f"\n## Metrics Summary\n{metrics_summary}" if metrics_summary else ""

        observability_text = f"\n## Observability Guidelines\n{observability_doc[:3000]}" if observability_doc else ""
        architecture_text = f"\n## Architecture Context\n{architecture_doc[:2000]}" if architecture_doc else ""

        messages = [
            {
                "role": "user",
                "content": f"""You are responding to an active incident. Provide a structured incident response.

## Incident Details
- Ticket: {ticket_id}
- Title: {incident_title}
- Severity: {severity.upper()}

## Affected Services
{affected_services_text}

## Observed Symptoms
{symptoms_text}
{timeline_text}
{metrics_text}
{observability_text}
{architecture_text}

Respond with the following sections, clearly labeled:

1. **IMMEDIATE MITIGATION STEPS** — ordered list of actions to stop the bleeding right now (rollback, circuit breaker, traffic shift, etc.)

2. **ROOT CAUSE HYPOTHESES** — ranked list of likely root causes with supporting evidence from the symptoms and metrics

3. **INVESTIGATION RUNBOOK** — step-by-step diagnostic commands and checks to confirm or rule out each hypothesis

4. **STAKEHOLDER COMMUNICATION TEMPLATE** — a ready-to-send incident update message suitable for Slack/email (include placeholders for dynamic values)

5. **POST-MORTEM OUTLINE** — section headings and guiding questions for the post-mortem document once the incident is resolved

6. **OPEN QUESTIONS** — any clarifying information that would significantly change the response plan
"""
            }
        ]

        response = llm.complete(system_prompt, messages, max_tokens=6000)

        return {
            "severity": severity,
            "mitigation_steps": self._extract_section(response, "IMMEDIATE MITIGATION STEPS"),
            "root_cause_hypotheses": self._extract_section(response, "ROOT CAUSE HYPOTHESES"),
            "investigation_runbook": self._extract_section(response, "INVESTIGATION RUNBOOK"),
            "stakeholder_update": self._extract_section(response, "STAKEHOLDER COMMUNICATION TEMPLATE"),
            "postmortem_outline": self._extract_section(response, "POST-MORTEM OUTLINE"),
            "questions": self._extract_section(response, "OPEN QUESTIONS"),
            "raw_response": response,
        }

    def _extract_section(self, response: str, section_name: str) -> str:
        """Extract a named section from the LLM response."""
        lines = response.splitlines()
        capturing = False
        section_lines = []
        for line in lines:
            if section_name in line:
                capturing = True
                continue
            if capturing:
                # Stop at the next numbered bold section heading
                stripped = line.strip()
                if stripped and stripped[0].isdigit() and "**" in stripped and stripped[1:3] in (". ", ") "):
                    break
                section_lines.append(line)
        return "\n".join(section_lines).strip()


class CapacityPlanningSkill(Skill):
    """Analyze resource utilization and forecast capacity needs."""

    @property
    def name(self) -> str:
        return "capacity-planning"

    @property
    def description(self) -> str:
        return "Analyze resource utilization and forecast capacity needs"

    def execute(self, context: SkillContext, llm: LLMProvider) -> dict:
        system_prompt = self.build_system_prompt(context)

        payload = context.task.input_payload
        ticket_id = payload.get("ticket_id", context.task.jira_ticket_id)
        service_name = payload.get("service_name", "")
        current_metrics = payload.get("current_metrics", {})
        growth_rate_percent = payload.get("growth_rate_percent", None)
        planning_horizon_days = payload.get("planning_horizon_days", 90)

        observability_doc = context.plan_documents.get("OBSERVABILITY.md", "")
        architecture_doc = context.plan_documents.get("ARCHITECTURE.md", "")

        metrics_rows = []
        for metric_name, values in current_metrics.items():
            current = values.get("current", "N/A")
            avg_30d = values.get("avg_30d", "N/A")
            peak_30d = values.get("peak_30d", "N/A")
            metrics_rows.append(
                f"| {metric_name} | {current} | {avg_30d} | {peak_30d} |"
            )
        metrics_table = (
            "| Metric | Current | 30d Avg | 30d Peak |\n"
            "|--------|---------|---------|----------|\n"
            + "\n".join(metrics_rows)
        ) if metrics_rows else "(no metrics provided)"

        growth_text = f"\nObserved or assumed growth rate: **{growth_rate_percent}% per period**" if growth_rate_percent is not None else ""

        observability_text = f"\n## Observability Guidelines\n{observability_doc[:3000]}" if observability_doc else ""
        architecture_text = f"\n## Architecture Context\n{architecture_doc[:2000]}" if architecture_doc else ""

        messages = [
            {
                "role": "user",
                "content": f"""Perform a capacity planning analysis for the following service.

## Request Details
- Ticket: {ticket_id}
- Service: {service_name}
- Planning horizon: {planning_horizon_days} days
{growth_text}

## Current Resource Metrics
{metrics_table}
{observability_text}
{architecture_text}

Respond with the following sections, clearly labeled:

1. **UTILIZATION SUMMARY** — a concise assessment of the current resource health for each metric (healthy / warning / critical)

2. **FORECASTS** — for each resource, provide:
   - Projected usage at the end of the {planning_horizon_days}-day horizon
   - Estimated exhaustion date (when the resource hits capacity limits)
   - Confidence level (low / medium / high) based on data quality and trend stability

3. **SCALING RECOMMENDATIONS** — specific actions ranked by priority:
   - Horizontal scaling (add replicas/nodes)
   - Vertical scaling (increase instance size)
   - Architectural changes (caching, sharding, archival, etc.)
   Include the recommended timeline for each action.

4. **ESTIMATED COST IMPACT** — rough cost delta for the top recommended actions

5. **OPEN QUESTIONS** — data gaps or assumptions that would change the forecast significantly
"""
            }
        ]

        response = llm.complete(system_prompt, messages, max_tokens=5000)

        return {
            "service_name": service_name,
            "utilization_summary": self._extract_section(response, "UTILIZATION SUMMARY"),
            "forecasts": self._extract_section(response, "FORECASTS"),
            "recommendations": self._extract_section(response, "SCALING RECOMMENDATIONS"),
            "estimated_cost_impact": self._extract_section(response, "ESTIMATED COST IMPACT"),
            "questions": self._extract_section(response, "OPEN QUESTIONS"),
            "raw_response": response,
        }

    def _extract_section(self, response: str, section_name: str) -> str:
        """Extract a named section from the LLM response."""
        lines = response.splitlines()
        capturing = False
        section_lines = []
        for line in lines:
            if section_name in line:
                capturing = True
                continue
            if capturing:
                stripped = line.strip()
                if stripped and stripped[0].isdigit() and "**" in stripped and stripped[1:3] in (". ", ") "):
                    break
                section_lines.append(line)
        return "\n".join(section_lines).strip()


class SREAgent(BaseAgent):
    """SRE Agent - handles incident response and capacity planning."""

    @property
    def role(self) -> str:
        return "sre"

    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)

        # Register skills
        self.register_skill(IncidentResponseSkill())
        self.register_skill(CapacityPlanningSkill())
