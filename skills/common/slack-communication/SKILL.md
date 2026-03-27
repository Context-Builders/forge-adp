# Slack Communication Skill

## Purpose

Provides all Slack messaging operations for Forge agents:
- Post task progress updates to engineering channels
- Escalate blockers to human reviewers with context
- Notify on task completion or failure with linked artifacts
- Reply within threads to maintain conversation context
- Mention specific users for time-sensitive approvals

## Prerequisites

1. `SLACK_BOT_TOKEN` with `chat:write`, `channels:read` scopes
2. Bot has been invited to the target channel
3. Channel ID or name configured in `.forge/config.yaml`

## Execution Steps

1. **Resolve Channel**
   - Look up channel by name if an ID is not provided
   - Confirm bot is a member; join if permitted and not already a member

2. **send_message**
   - Format message as plain text or Block Kit if structured content is provided
   - Apply urgency formatting: normal / yellow ⚠️ / red 🚨 header based on `urgency`
   - Post to channel and capture `message_ts`

3. **reply_thread**
   - Use `thread_ts` to post reply within existing thread
   - Avoids channel noise for ongoing task updates

4. **escalate**
   - Format escalation block with: reason, task ID, Jira link, attempted resolution
   - Mention `mention_users` or the default oncall handle
   - Set `urgency` to `high` or `critical` automatically

5. **notify_completion**
   - Post structured completion summary: task, duration, artifacts (PR URL, ticket link)
   - React to original task-start message with ✅ if `thread_ts` provided

6. **notify_failure**
   - Post failure summary: task, error message, next steps
   - React to original task-start message with ❌ if `thread_ts` provided
   - Trigger escalation if `urgency` is `critical`

## Plan References

| Document | Sections to Consult |
|----------|---------------------|
| `.forge/config.yaml` | Slack channel configuration, oncall handle |

## Quality Gates

- [ ] Message includes Forge task ID for traceability
- [ ] Escalation messages include a Jira ticket link
- [ ] No sensitive data (tokens, secrets) included in message body
- [ ] All mentions use verified Slack IDs, not raw email addresses

## Escalation Triggers

- `channel_not_found` (bot not invited)
- `rate_limited` (Slack API 429 — back off and retry)
- Cannot resolve mentioned user IDs

## Anti-Patterns

- Do not post more than one update per minute per thread (avoid spam)
- Do not use `escalate` for routine updates — reserve it for genuine blockers
- Do not include raw stack traces in Slack messages — link to logs instead
- Do not DM individual users without falling back to channel if DM fails

## Examples

### Input

```json
{
  "action": "escalate",
  "channel": "#payments-eng",
  "message": "Blocker on PAY-1234: API contract conflicts with existing /payments endpoint. Cannot proceed without PM or lead clarification.",
  "mention_users": ["U012AB3CD"],
  "urgency": "high"
}
```

### Output

```json
{
  "message_ts": "1708531200.000100",
  "channel_id": "C034XY5ZA",
  "permalink": "https://acme.slack.com/archives/C034XY5ZA/p1708531200000100"
}
```
