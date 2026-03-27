# Sprint Planning Skill

## Purpose

Transform product goals into a structured, sprint-ready set of Jira tickets:
- Decompose epics and goals into Stories, Tasks, and Sub-tasks
- Assign effort estimates based on complexity signals
- Identify dependencies between tickets
- Prioritize backlog items to fit within sprint capacity
- Generate a sprint plan summary for stakeholder review

## Prerequisites

1. Product goal or feature description with enough context to decompose
2. Jira project key and sprint configured
3. Team capacity in points or days (optional but recommended)
4. Existing backlog visible to avoid duplicating tickets

## Execution Steps

1. **Load Context**
   - Load product and architecture docs via `plan-reader`
   - Fetch existing backlog from Jira to identify overlaps

2. **Decompose Goal**
   - Identify top-level Epics or Stories from the goal
   - Break each Story into implementation Tasks across relevant agent roles
   - Identify acceptance criteria for each Story
   - Flag cross-team dependencies

3. **Estimate and Prioritize**
   - Assign story point estimates based on complexity and prior similar tickets
   - Order tickets by priority: dependencies first, then by business value
   - Check total estimate against `sprint_capacity` and trim/adjust as needed

4. **Create Jira Tickets**
   - Create all tickets via `jira-interaction` skill
   - Set parent Epic links and ticket dependencies
   - Assign agent labels (`forge`) for traceability

5. **Communicate Plan**
   - Post sprint plan summary to team Slack channel via `slack-communication`

## Plan References

| Document | Sections to Consult |
|----------|---------------------|
| ARCHITECTURE.md | Service boundaries to assign tickets to correct agent roles |
| CONTRIBUTING.md | Ticket template and labeling conventions |

## Quality Gates

- [ ] All tickets have clear acceptance criteria
- [ ] Ticket estimates are populated
- [ ] Dependency links set between tickets
- [ ] Total estimate is within sprint capacity
- [ ] PM lead or product owner has approved the plan

## Escalation Triggers

- Goal is too vague to decompose without further product owner input
- Sprint capacity is insufficient for the minimum viable scope
- Decomposition reveals an unresolved architectural decision

## Anti-Patterns

- Do not create tickets without acceptance criteria
- Do not estimate in hours — use story points
- Do not assign tickets across sprints without flagging it explicitly
- Do not skip creating dependency links

## Examples

### Input

```json
{
  "goal_description": "Enable customers to request refunds from the payment detail page",
  "sprint_capacity": 40,
  "existing_backlog": []
}
```

### Output

```json
{
  "tickets_created": ["PAY-1260", "PAY-1261", "PAY-1262", "PAY-1263"],
  "total_estimate_points": 34,
  "sprint_plan_summary": "4 tickets: refund API (BE 8pts), refund UI (FE 13pts), refund email (BE 5pts), E2E tests (QA 8pts)"
}
```
