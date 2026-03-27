# Compliance Report Skill

## Purpose

Produce a structured compliance report over a configurable time window:
- Policy adherence rate per agent role (% of actions allowed vs denied)
- Quality gate pass/fail rates across all tasks
- LLM cost actuals vs budget limits and trend projection
- Human-in-the-loop approval rate and median resolution time
- Outstanding policy violations that were not remediated
- Comparison against the previous period (delta view)

## Prerequisites

1. Read access to the Forge PostgreSQL audit log
2. Slack token with `chat:write` permission
3. Jira token (optional — required only if a ticket number is supplied)

## Execution Steps

1. **Load Context**
   - Parse `period` input (default `P7D`)
   - Pull relevant records from `audit_log`, `tasks`, `agent_memory` tables

2. **Policy Adherence Analysis**
   - Count `ALLOW` vs `DENY` outcomes from the policy engine log for each role
   - Flag any deny patterns that repeat more than 3 times (systemic misconfiguration)

3. **Quality Gate Analysis**
   - Aggregate quality gate outcomes from the `tasks` table
   - Compute per-role and per-skill pass rates
   - Flag any gate that failed > 20 % of the time

4. **Budget & Cost Analysis**
   - Sum LLM token costs from `tasks.llm_cost_usd` for the period
   - Compare against `config.yaml` budget limits
   - Compute a linear projection for the remaining billing month

5. **Human-in-the-Loop Metrics**
   - Calculate approval rate and median latency from `tasks` where `status = 'awaiting_approval'`
   - Flag tasks blocked for > 48 h

6. **Synthesise Report and Publish**
   - Generate a Markdown report with executive summary and appendices
   - Post summary to the configured Slack channel
   - If `jira_ticket` is provided, attach full report as a Jira comment
   - Create Jira tickets for any Critical findings

## Plan References

| Document | Sections to Consult |
|----------|---------------------|
| SECURITY_POLICY.md | Quality gate thresholds, approval SLAs |
| config.yaml | Budget limits, Slack channel, quality gate definitions |

## Quality Gates

- [ ] Report covers the full requested period with no data gaps
- [ ] All agent roles are included in the analysis
- [ ] Critical findings have corresponding Jira tickets
- [ ] Report is posted to the configured Slack channel

## Escalation Triggers

- Budget consumption > 90 % of monthly limit
- Any agent role with policy adherence rate < 80 %
- Unresolved `BLOCKED` task older than 72 h

## Anti-Patterns

- Do not delete or alter audit log records
- Do not surface individual developer names — aggregate by role only
- Do not auto-close Jira tickets created by this skill

## Examples

### Input

```json
{
  "period": "P7D",
  "project_id": "proj-123",
  "jira_ticket": "GOV-010"
}
```

### Output

```json
{
  "period": "2026-02-15/2026-02-22",
  "policy_adherence": {"backend-developer": 0.97, "devops": 0.94},
  "quality_gate_pass_rates": {"unit_tests_pass": 0.91, "lint_pass": 0.99},
  "cost_usd": 312.50,
  "budget_utilisation": 0.63,
  "hitl_approval_rate": 1.0,
  "hitl_median_latency_hours": 3.2,
  "critical_findings": [],
  "report_url": "https://jira.example.com/browse/GOV-010"
}
```
