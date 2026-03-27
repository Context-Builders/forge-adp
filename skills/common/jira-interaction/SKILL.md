# Jira Interaction Skill

## Purpose

Provides all Jira API operations required by Forge agents:
- Create tickets (Stories, Tasks, Bugs, Sub-tasks)
- Update ticket fields (summary, description, priority, labels, assignee)
- Transition tickets through workflow states
- Add comments with structured progress updates or blockers
- Retrieve ticket details and linked issues

## Prerequisites

1. `JIRA_API_TOKEN`, `JIRA_BASE_URL`, and `JIRA_USER_EMAIL` environment variables set
2. Agent's Jira user has appropriate project permissions
3. Project key confirmed in `.forge/config.yaml`

## Execution Steps

1. **Validate Action**
   - Check `action` is a supported operation
   - Verify `project_key` exists and is accessible

2. **create_ticket**
   - Map `ticket_data.type` to Jira issue type ID
   - Set required fields: summary, description, project
   - Apply labels including `forge` for agent-created tickets
   - Return new ticket ID and URL

3. **update_ticket**
   - Fetch current state of `ticket_id`
   - Apply only the changed fields via PATCH
   - Return updated ticket state

4. **transition_ticket**
   - Fetch available transitions for `ticket_id`
   - Find transition matching the requested state name
   - Execute transition; add comment if `comment` is provided
   - Return new ticket status

5. **add_comment**
   - Post `comment` body to `ticket_id`
   - Tag relevant team members if mentioned
   - Return comment ID and permalink

6. **get_ticket**
   - Fetch full ticket including description, acceptance criteria, attachments, linked issues
   - Return structured ticket object for downstream skill consumption

## Plan References

| Document | Sections to Consult |
|----------|---------------------|
| CONTRIBUTING.md | Jira ticket structure, label conventions |

## Quality Gates

- [ ] Created tickets include the `forge` label
- [ ] Ticket summaries are under 255 characters
- [ ] Transitions only move tickets forward in the workflow
- [ ] Comments cite the Forge task ID for traceability

## Escalation Triggers

- Jira project key not found (404)
- Insufficient permissions to create or transition tickets
- Required custom field missing from project configuration
- Ticket referenced by `ticket_id` does not exist

## Anti-Patterns

- Do not create duplicate tickets; check for existing tickets with the same acceptance criteria first
- Do not transition a ticket to Done without attaching evidence (PR link, test results)
- Do not store Jira credentials in committed files
- Do not bypass workflow transitions by directly patching `status`

## Examples

### Input

```json
{
  "action": "transition_ticket",
  "project_key": "PAY",
  "ticket_id": "PAY-1234",
  "transition": "In Review",
  "comment": "Implementation complete. PR #347 opened for review: https://github.com/acme-corp/payments-service/pull/347"
}
```

### Output

```json
{
  "ticket_id": "PAY-1234",
  "new_status": "In Review",
  "comment_id": "10045",
  "url": "https://acme.atlassian.net/browse/PAY-1234"
}
```
