# Slack Adapter

Port: **:19092**

Sends Forge event notifications to Slack channels and receives slash commands and interactive payload actions. The primary human-in-the-loop interface for approvals and escalation acknowledgement.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `SLACK_BOT_TOKEN` | Yes | Slack bot OAuth token (`xoxb-...`) |
| `SLACK_SIGNING_SECRET` | No | Slack signing secret for HMAC-SHA256 request verification |
| `SLACK_APP_TOKEN` | No | Slack app-level token (`xapp-...`) for Socket Mode |
| `FORGE_STATUS_CHANNEL` | No | Channel ID for task status notifications |
| `FORGE_APPROVALS_CHANNEL` | No | Channel ID for review/approval requests |
| `FORGE_ESCALATIONS_CHANNEL` | No | Channel ID for escalations and policy denials |

## Webhook

**Path:** `/webhook`
**Security:** HMAC-SHA256 — Slack signs requests using `SLACK_SIGNING_SECRET`; adapter verifies the `X-Slack-Signature` header.

Register at:
- **Slack App → Event Subscriptions** — set Request URL to `https://<host>:19092/webhook`, subscribe to `app_mention` and `message.channels` events.
- **Slack App → Interactivity & Shortcuts** — set Request URL to `https://<host>:19092/webhook` for interactive component payloads.
- **Slack App → Slash Commands** — point each `/forge-*` command to `https://<host>:19092/webhook`.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive Slack events, slash commands, and interactive payloads |

## Events Published

_None — this adapter forwards events to Slack but does not publish to the bus from inbound events._

## Events Subscribed

| Event | Action |
|---|---|
| `task.completed` | Posts a status message to `FORGE_STATUS_CHANNEL` |
| `review.requested` | Posts an approval request to `FORGE_APPROVALS_CHANNEL` |
| `escalation.created` | Posts an alert to `FORGE_ESCALATIONS_CHANNEL` |
| `policy.denied` | Posts a policy denial notification to `FORGE_ESCALATIONS_CHANNEL` |
