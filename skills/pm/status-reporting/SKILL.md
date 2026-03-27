# Status Reporting Skill

## Purpose

Generate structured sprint status reports from live Jira data:
- Sprint progress summary (completed / in-progress / not started)
- Velocity comparison to sprint goal
- Blockers and escalations
- Risk flags for tickets at-risk of missing sprint
- Stakeholder-appropriate narrative summary

Reports are posted to the configured Slack channel and optionally saved to Confluence.

## Prerequisites

1. Active sprint ID or name in the configured Jira project
2. Slack channel and Confluence space configured in `.forge/config.yaml`
3. Jira sprint data accessible with configured credentials

## Execution Steps

1. **Fetch Sprint Data**
   - Query Jira for all tickets in the sprint
   - Categorize by status: To Do, In Progress, In Review, Done, Blocked
   - Calculate completion percentage and velocity against sprint goal

2. **Identify Risks and Blockers**
   - Flag tickets with `Blocked` status or open escalation comments
   - Identify In Progress tickets with no recent activity (stale > 2 days)
   - Flag scope creep: tickets added after sprint start

3. **Generate Report**
   - Write report narrative tailored to `report_audience`
   - Include status table, blockers section, and risk flags
   - Summarize completed work with PR links where available

4. **Distribute Report**
   - Post to Slack channel via `slack-communication`
   - Create or update Confluence page if space is configured

## Plan References

| Document | Sections to Consult |
|----------|---------------------|
| `.forge/config.yaml` | Sprint reporting channel, Confluence space |

## Quality Gates

- [ ] All in-progress tickets are accounted for
- [ ] Completion percentage is accurate
- [ ] Blockers section is present if any tickets are blocked
- [ ] Report is posted to correct Slack channel

## Escalation Triggers

- Sprint completion is below 30% with fewer than 2 days remaining
- More than 3 tickets are blocked simultaneously
- Sprint goal has been formally changed mid-sprint

## Anti-Patterns

- Do not include sensitive ticket details in stakeholder or executive reports
- Do not fabricate metrics — all numbers must come from live Jira data
- Do not skip reporting even if the sprint has low activity

## Examples

### Input

```json
{
  "sprint_id": "PAY Sprint 12",
  "report_audience": "stakeholders",
  "include_blockers": true
}
```

### Output

```json
{
  "completion_percentage": 65,
  "tickets_done": 8,
  "tickets_in_progress": 4,
  "tickets_blocked": 1,
  "slack_message_ts": "1708531200.000200",
  "confluence_page_url": "https://acme.atlassian.net/wiki/spaces/ACME/pages/123456"
}
```
