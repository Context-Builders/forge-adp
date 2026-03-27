# Ticket Triage Skill

## Purpose

Review newly created Jira tickets to ensure they are actionable before agents pick them up:
- Completeness check: acceptance criteria, type, priority, assignee
- Priority assignment based on business impact and dependencies
- Agent role routing based on ticket type and labels
- Clarification requests for incomplete tickets
- Duplicate detection against existing backlog

## Prerequisites

1. List of Jira ticket IDs to triage
2. Jira project accessible with configured credentials
3. Slack channel configured for clarification requests

## Execution Steps

1. **Fetch Tickets**
   - Retrieve full ticket details for each ID via `jira-interaction`
   - Load recent sprint backlog for duplicate detection

2. **Completeness Check**
   - Verify: summary, description, acceptance criteria, ticket type, priority
   - Flag missing fields per ticket
   - Check for duplicate tickets with similar summaries

3. **Priority Assignment**
   - Evaluate business impact signals: linked incidents, revenue impact, customer-reported
   - Apply priority matrix: Critical / High / Medium / Low
   - Update Jira priority field

4. **Agent Routing**
   - Determine correct agent role from ticket type, labels, and content
   - Add `forge` label and appropriate agent role label
   - Assign to agent queue if all criteria are met

5. **Handle Incomplete Tickets**
   - Post clarification requests to Slack for tickets missing key information
   - Add comment to ticket asking reporter to provide missing details
   - Transition ticket to `Needs Clarification` status

## Plan References

| Document | Sections to Consult |
|----------|---------------------|
| CONTRIBUTING.md | Ticket structure requirements, label taxonomy |

## Quality Gates

- [ ] All triaged tickets have a priority set
- [ ] All actionable tickets have an agent role label
- [ ] Incomplete tickets have clarification comments
- [ ] No tickets are silently dropped — every ticket has a triage outcome

## Escalation Triggers

- Ticket describes a production incident — escalate immediately to SRE
- Ticket requires urgent security review — escalate to SecOps
- Ticket cannot be routed because ownership is ambiguous

## Anti-Patterns

- Do not assign tickets to agent queues without acceptance criteria
- Do not close tickets as duplicates without linking to the original
- Do not change ticket priority without documenting the reason in a comment

## Examples

### Input

```json
{
  "ticket_ids": ["PAY-1270", "PAY-1271", "PAY-1272"],
  "triage_criteria": null
}
```

### Output

```json
{
  "triage_results": [
    { "ticket_id": "PAY-1270", "priority": "High", "assigned_agent": "backend-developer", "status": "ready" },
    { "ticket_id": "PAY-1271", "priority": "Medium", "assigned_agent": "frontend-developer", "status": "ready" },
    { "ticket_id": "PAY-1272", "priority": null, "assigned_agent": null, "status": "needs_clarification", "missing_fields": ["acceptance_criteria"] }
  ]
}
```
