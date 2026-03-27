# Incident Response Skill

## Purpose

Coordinate incident detection, triage, mitigation, and post-mortem:
- Acknowledge and triage PagerDuty/Opsgenie alerts
- Create incident Jira ticket with severity classification
- Coordinate communication in Slack incident war room
- Suggest runbook steps matching the alert type
- Record incident timeline
- Write structured post-mortem after resolution

## Prerequisites

1. PagerDuty or Opsgenie configured with on-call escalation policies
2. Incident Slack channel or war room configured
3. Runbooks accessible via plan documents or Confluence
4. SRE lead on-call and available for human decision-making

## Execution Steps

1. **Triage Alert**
   - Parse incoming alert payload for service, metric, threshold, and severity signals
   - Infer severity: Sev1 (production down / revenue impact), Sev2 (degraded), Sev3 (warning)
   - Acknowledge PagerDuty incident to stop escalation if auto-acknowledged is enabled

2. **Create Incident Ticket**
   - Create Jira incident ticket with severity, affected services, and initial description
   - Set status to `Investigating`

3. **Open War Room**
   - Create or join incident Slack channel `#incident-{ticket-id}`
   - Post initial incident summary: what is affected, severity, current status
   - Mention on-call SRE lead and relevant service owners

4. **Suggest Runbook Steps**
   - Match alert type to runbook in Confluence or `.forge/plans/`
   - Post relevant runbook section to Slack channel with next steps
   - Follow up with status updates as responders execute steps

5. **Record Timeline**
   - Log key events to Jira ticket: alert time, acknowledgement, mitigations attempted, resolution
   - Update Slack channel with regular status updates (every 15 min for Sev1)

6. **Write Post-Mortem** *(after resolution)*
   - Generate structured post-mortem in Confluence: timeline, root cause, contributing factors, action items
   - Create Jira tickets for each post-mortem action item

## Plan References

| Document | Sections to Consult |
|----------|---------------------|
| ARCHITECTURE.md | Service dependencies, data flow for blast radius assessment |
| Runbooks | Matching runbook for alert type (fetched from Confluence) |

## Quality Gates

- [ ] Incident ticket created within 5 minutes of alert acknowledgement
- [ ] Slack war room opened for Sev1 and Sev2 incidents
- [ ] Timeline events are timestamped and accurate
- [ ] Post-mortem completed within 48 hours of resolution (Sev1/Sev2)
- [ ] All post-mortem action items have Jira tickets

## Escalation Triggers

- Sev1 incident not mitigated within 30 minutes — escalate to engineering lead
- Root cause unknown and runbook is not helping — escalate to service owner
- Incident involves data loss or security breach — escalate to SecOps immediately

## Anti-Patterns

- Do not attempt to auto-remediate production issues without SRE lead approval (autonomy_level: 1)
- Do not close the incident ticket before confirming resolution
- Do not skip the post-mortem for Sev1 or Sev2 incidents
- Do not assign blame in post-mortem — focus on systemic causes

## Examples

### Input

```json
{
  "incident_trigger": "PD-12345: payments-api error rate >5% (threshold 1%) — P1 alert",
  "severity": "sev1",
  "affected_services": ["payments-api"]
}
```

### Output

```json
{
  "incident_ticket": "INC-021",
  "slack_channel": "#incident-INC-021",
  "severity_classified": "sev1",
  "runbook_suggested": "payments-api-high-error-rate-runbook",
  "timeline_events_recorded": 4,
  "post_mortem_url": null
}
```
