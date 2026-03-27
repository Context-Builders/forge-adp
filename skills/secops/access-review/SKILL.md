# Access Review Skill

## Purpose

Audit and report on access permissions across the environment:
- IAM role and policy analysis for over-privilege (AWS, GCP, Azure)
- Service account permission review
- GitHub repository and organization permission audit
- Identify stale access for offboarded users or decommissioned services
- Generate remediation PRs for IaC-managed permissions

## Prerequisites

1. Jira ticket authorizing the access review
2. Cloud provider credentials with read-only IAM/audit permissions
3. GitHub token with org read permissions
4. SecOps lead available for findings review

## Execution Steps

1. **Load Context**
   - Retrieve ticket scope and load security architecture docs
   - Determine which principals are in scope

2. **IAM Audit**
   - Enumerate all IAM roles, policies, and group memberships
   - Flag roles with wildcard `*` actions on sensitive resources
   - Identify roles not used in past 90 days (stale access)
   - Compare against approved role inventory if available

3. **Service Account Review**
   - Enumerate Kubernetes service accounts and bound roles
   - Flag service accounts with `cluster-admin` or over-broad permissions
   - Identify unused service accounts

4. **GitHub Permissions Audit**
   - List all outside collaborators and their access levels
   - Flag write/admin access for users not in the engineering team roster
   - Identify repos with no branch protection rules

5. **Generate Findings and Remediation**
   - Classify Findings as Critical / High / Medium / Low
   - Create Jira tickets for Critical and High findings
   - Generate IaC PRs to remove clearly over-privileged permissions (auto-remediable)
   - Post summary report to SecOps Slack channel

## Plan References

| Document | Sections to Consult |
|----------|---------------------|
| ARCHITECTURE.md | IAM boundary definitions, service account naming |

## Quality Gates

- [ ] All principals in scope are reviewed
- [ ] Critical/High findings have Jira tickets
- [ ] Report is submitted to SecOps lead for sign-off
- [ ] No automated remediation applied to production without human approval

## Escalation Triggers

- Unauthorized principal found with production admin access
- Evidence of dormant service account recently activated
- Critical finding cannot be auto-remediated within 24 hours

## Anti-Patterns

- Do not automatically remove permissions without human approval for production accounts
- Do not store audit credentials in code repositories
- Do not dismiss findings without documented justification

## Examples

### Input

```json
{
  "jira_ticket": "SEC-045",
  "scope": "iam",
  "environment": "production"
}
```

### Output

```json
{
  "principals_reviewed": 38,
  "findings": [
    { "severity": "high", "principal": "arn:aws:iam::123456789:role/payments-worker", "issue": "Has s3:* on all buckets", "ticket": "SEC-046" }
  ],
  "auto_remediation_prs": [],
  "report_posted": true
}
```
