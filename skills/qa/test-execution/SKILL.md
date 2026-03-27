# Test Execution Skill

## Purpose

Execute test suites in a target environment and record results:
- Trigger CI test runs or execute tests directly
- Parse and aggregate pass/fail results
- Create bug tickets for each new failure via `bug-reporting` skill
- Post results summary to Jira ticket and Slack
- Gate release validation on pass rate threshold

## Prerequisites

1. Jira ticket documenting what is being tested
2. Target environment accessible and recently deployed
3. Test suite command or CI pipeline configured
4. QA Slack channel configured

## Execution Steps

1. **Load Context**
   - Retrieve ticket and load test plan from Jira or plan documents
   - Identify test framework and run command from CONTRIBUTING.md

2. **Execute Tests**
   - Trigger test run (CI pipeline dispatch or direct command)
   - Apply `test_filter` if provided
   - Wait for completion with timeout

3. **Parse Results**
   - Extract pass/fail/skip counts
   - Capture failure messages and stack traces for each failure
   - Compare to last known results to identify new regressions

4. **Report Failures**
   - For each new failure: invoke `bug-reporting` skill to create Jira ticket
   - Link bug tickets back to test execution ticket
   - Mark known/pre-existing failures as such to avoid noise

5. **Post Summary**
   - Add test results comment to Jira ticket
   - Post results summary to QA Slack channel: pass rate, failure count, links

## Plan References

| Document | Sections to Consult |
|----------|---------------------|
| CONTRIBUTING.md | Test run commands, CI pipeline names, test timeout config |

## Quality Gates

- [ ] All tests in scope are executed (no silent skips)
- [ ] Every failure has a linked bug ticket
- [ ] Pass rate is reported as a numeric percentage
- [ ] Results are posted to Jira ticket before marking execution complete

## Escalation Triggers

- Pass rate is below 80% for smoke or regression suite
- Test environment is unreachable or returning 5xx errors
- CI pipeline fails before tests begin (infra issue, not test issue)

## Anti-Patterns

- Do not mark test execution as passed if any tests are skipped without explanation
- Do not re-run tests to make failures disappear — investigate first
- Do not create bug tickets for known-flaky tests — mark them as flaky instead

## Examples

### Input

```json
{
  "jira_ticket": "PAY-1285",
  "test_suite": "integration",
  "target_environment": "staging",
  "test_filter": "payments"
}
```

### Output

```json
{
  "total_tests": 47,
  "passed": 44,
  "failed": 2,
  "skipped": 1,
  "pass_rate": 93.6,
  "new_bug_tickets": ["PAY-1286", "PAY-1287"],
  "jira_comment_added": true,
  "slack_notification_ts": "1708531400.000100"
}
```
