# Policy Drift Detection Skill

## Purpose

Identify gaps between declared OPA policy rules and real observed agent behaviour:
- Actions that are _technically allowed_ by OPA but are implicitly out of scope for a role
- Actions not covered by any explicit rule (policy gaps / silent permissions)
- Repeated deny patterns indicating agents are mis-using capabilities
- Roles whose observed action surface has grown beyond what the policy explicitly models
- Suggest new OPA rules or restrictions to close each gap

Where a gap is auto-remediable (e.g. a missing explicit deny for a clearly unsafe action), the skill opens a PR against the OPA bundle.

## Prerequisites

1. Read access to the Forge PostgreSQL audit log
2. Read access to the OPA bundle (`deployments/opa/forge.rego` by default)
3. GitHub token with write permission to open PRs (for auto-remediable gaps only)

## Execution Steps

1. **Load Policy and Audit Log**
   - Parse the OPA bundle to extract all explicit `allow` / `deny` rules and the roles/actions they cover
   - Query `audit_log` for every `(agent_role, action, resource)` triple in the analysis period

2. **Gap Analysis**
   - For each unique `(role, action)` pair in the audit log, check if OPA has an explicit rule
   - Classify as: `explicitly_allowed`, `explicitly_denied`, `implicitly_allowed` (no rule — defaulted to allow), `implicitly_denied`
   - `implicitly_allowed` triples are candidate policy gaps

3. **Drift Scoring**
   - Score each gap by frequency × severity (estimated from action + resource type)
   - Classify: `Critical` (risk ≥ 7), `High` (5–7), `Medium` (3–5), `Low` (< 3)

4. **Remediation Suggestions**
   - For auto-remediable Critical/High gaps, generate OPA rule additions
   - Open a GitHub PR against `deployments/opa/forge.rego` with a description of each change
   - For non-auto-remediable gaps, create Jira tickets

5. **Report and Publish**
   - Post drift summary to Slack
   - Return structured `policy_gaps` list with severity, current behaviour, and recommended fix

## Plan References

| Document | Sections to Consult |
|----------|---------------------|
| SECURITY_POLICY.md | Intended permission boundaries per role |
| ARCHITECTURE.md | Service and resource taxonomy for severity scoring |

## Quality Gates

- [ ] Every agent role present in the audit log is analysed
- [ ] All Critical gaps have either a remediation PR or a Jira ticket
- [ ] Report clearly distinguishes `implicitly_allowed` from `explicitly_allowed`
- [ ] Auto-remediation PRs do not add `allow` rules — only `deny` or explicit scoping

## Escalation Triggers

- Critical gap found where a non-privileged role can affect production infrastructure
- Drift score increases by > 30 % compared to the previous analysis
- OPA bundle cannot be parsed (misconfiguration)

## Anti-Patterns

- Do not open PRs that broaden permissions — this skill may only _restrict_ or _make explicit_
- Do not report noise: filter out `implicitly_allowed` triples where frequency < 3
- Do not run this skill concurrently on the same project — results may conflict

## Examples

### Input

```json
{
  "period": "P30D",
  "project_id": "proj-123"
}
```

### Output

```json
{
  "period": "2026-01-23/2026-02-22",
  "policy_gaps": [
    {
      "role": "backend-developer",
      "action": "delete_branch",
      "resource": "protected-branches",
      "frequency": 12,
      "severity": "High",
      "gap_type": "implicitly_allowed",
      "recommendation": "Add explicit deny rule for delete on protected-branches for non-devops roles",
      "jira_ticket": "GOV-015"
    }
  ],
  "remediation_prs": ["https://github.com/org/repo/pull/88"],
  "drift_score_delta": 0.04
}
```
