# Bug Reporting Skill

## Purpose

Transform test failures, error logs, and observed defects into well-structured Jira bug reports:
- Structured bug tickets with clear repro steps
- Severity and priority classification
- Environment and version information
- Linked test run or CI job
- Slack notification to QA and engineering channels

## Prerequisites

1. Failure evidence: test output, stack trace, screenshot, or error log
2. Jira project key configured
3. QA/engineering Slack channel configured in `.forge/config.yaml`

## Execution Steps

1. **Analyze Evidence**
   - Parse failure output to extract error type, message, and location
   - Identify affected component from stack trace or error context
   - Infer severity if not provided: `critical` if production/data loss, `high` if blocker, otherwise `medium`

2. **Draft Bug Report**
   - Write summary: `[Component] Description of failure` (< 100 chars)
   - Write structured description with sections:
     - **Environment**: where observed
     - **Steps to Reproduce**: ordered list
     - **Expected Result**: what should happen
     - **Actual Result**: what happened
     - **Evidence**: log excerpt, stack trace, or screenshot link
   - Set type: Bug, priority, severity label

3. **Create Jira Ticket**
   - Create ticket via `jira-interaction`
   - Link to CI run URL if available
   - Apply `forge` and `qa` labels

4. **Notify**
   - Post to QA/engineering Slack channel with ticket link and one-line summary

## Plan References

| Document | Sections to Consult |
|----------|---------------------|
| CONTRIBUTING.md | Bug ticket template requirements, severity definitions |

## Quality Gates

- [ ] Bug summary is under 100 characters and clearly identifies the component
- [ ] Reproduction steps are present (or explicitly marked as `not reproducible`)
- [ ] Expected vs actual result is documented
- [ ] Severity and priority are set
- [ ] Slack notification is sent

## Escalation Triggers

- Bug is observed in production — escalate to SRE immediately
- Bug involves data loss or corruption — escalate to SecOps and DBA
- Bug cannot be reproduced — flag for manual investigation

## Anti-Patterns

- Do not create duplicate bug tickets — check for existing similar bugs first
- Do not omit evidence — always attach or link the failure output
- Do not mark all bugs as `critical` — apply accurate severity

## Examples

### Input

```json
{
  "failure_evidence": "FAIL: TestRefundPayment\npanic: runtime error: index out of range [0] with length 0\ngoroutine 1 [running]:\npayments/service.(*PaymentService).Refund(...)\n  payments/service/payment_service.go:142",
  "reproduction_steps": ["Create a payment", "Attempt to refund with amount=0"],
  "environment": "staging",
  "severity": "high"
}
```

### Output

```json
{
  "ticket_id": "PAY-1280",
  "url": "https://acme.atlassian.net/browse/PAY-1280",
  "severity": "high",
  "slack_notification_ts": "1708531300.000100"
}
```
