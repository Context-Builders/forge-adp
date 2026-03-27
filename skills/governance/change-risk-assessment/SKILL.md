# Change Risk Assessment Skill

## Purpose

Perform a structured risk assessment for a proposed high-impact change before it is queued for execution:
- Classify the change type (deployment, schema migration, security config, infra, other)
- Score the blast radius (affected services, environments, user traffic)
- Check whether required prerequisites are met (feature flags off, maintenance window booked, rollback plan exists)
- Produce a numeric risk score (0 – 10) and a recommendation: `approve`, `conditional`, or `reject`

A `conditional` recommendation lists specific mitigations that must be verified before the change proceeds. A `reject` recommendation blocks the task from being queued until a human overrides.

## Prerequisites

1. The proposed task payload in JSON form
2. Read access to the target repository to inspect rollback scripts, migrations, and IaC
3. Jira token to attach the assessment as a comment (optional)

## Execution Steps

1. **Classify the Change**
   - Determine change type from `task_payload.skill_name` and `agent_role`
   - Identify target environment from task inputs

2. **Blast Radius Analysis**
   - If `devops`: enumerate services touched by the deployment manifest
   - If `dba`: identify tables/rows affected by the migration
   - If `secops` or `infra`: identify IAM boundaries and network surfaces

3. **Prerequisite Checks**
   - Rollback plan documented in the task or linked Jira ticket
   - Maintenance window scheduled (check `config.yaml` deployment constraints)
   - Feature flag guards in place for risky code paths
   - Previous similar changes in the audit log — pass/fail rate

4. **Risk Scoring**
   - Score each dimension (blast radius, reversibility, prerequisite coverage) on 0 – 10
   - Compute weighted average → overall risk score
   - Apply hard rules: score ≥ 8 always → `reject` unless senior override is configured

5. **Generate Report and Recommendation**
   - Produce a structured Markdown risk report
   - Emit `approval_recommendation`: `approve` (< 4), `conditional` (4 – 7), `reject` (≥ 8)
   - Attach to Jira ticket if provided

## Plan References

| Document | Sections to Consult |
|----------|---------------------|
| ARCHITECTURE.md | Service dependency graph, blast radius boundaries |
| INFRASTRUCTURE.md | Maintenance window schedule, deployment constraints |
| SECURITY_POLICY.md | Change control requirements |
| config.yaml | Quality gate overrides, approval workflows |

## Quality Gates

- [ ] Risk score is a number in [0, 10]
- [ ] All three prerequisite categories are checked
- [ ] Recommendation is one of: `approve`, `conditional`, `reject`
- [ ] Conditional recommendations include specific, actionable mitigations

## Escalation Triggers

- Risk score ≥ 8 with no senior override configured
- Rollback plan missing for a production-targeting change
- Previous identical change failed in the audit log

## Anti-Patterns

- Do not auto-approve changes with risk score ≥ 8 even if prerequisites pass
- Do not cache assessments — always re-evaluate against the current audit log
- Do not substitute subjective LLM opinion for the structured scoring rubric

## Examples

### Input

```json
{
  "task_payload": {
    "skill_name": "deployment",
    "input": {
      "environment": "production",
      "manifest": "k8s/payments-service.yaml"
    }
  },
  "agent_role": "devops",
  "jira_ticket": "REL-204"
}
```

### Output

```json
{
  "risk_score": 5.2,
  "change_type": "kubernetes-deployment",
  "blast_radius": {"services": ["payments", "notifications"], "environments": ["production"]},
  "prerequisite_checks": {
    "rollback_plan": true,
    "maintenance_window": false,
    "feature_flags": true
  },
  "approval_recommendation": "conditional",
  "mitigations": ["Schedule a maintenance window before proceeding"],
  "report_markdown": "..."
}
```
