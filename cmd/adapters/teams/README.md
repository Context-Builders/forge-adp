# Microsoft Teams Adapter

Port: **:19093**

Sends Forge event notifications to a Microsoft Teams channel via incoming webhook and receives Bot Framework activity events. The primary human-in-the-loop interface for Teams-based approval workflows.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `TEAMS_WEBHOOK_URL` | Yes | Microsoft Teams incoming webhook URL (`https://outlook.office.com/webhook/...`) |
| `TEAMS_HMAC_SECRET` | No | Secret for HMAC-SHA256 validation of inbound Bot Framework events |

## Webhook

**Path:** `/webhook`
**Security:** HMAC-SHA256 — set `TEAMS_HMAC_SECRET`; adapter checks the `Authorization` HMAC header on incoming Bot Framework activities.

Register at: **Azure Bot Service → Bot channels registration → Messaging endpoint** → set to `https://<host>:19093/webhook`. For simple notifications only (no bot interactions), the incoming webhook URL in `TEAMS_WEBHOOK_URL` is sufficient and no inbound endpoint is needed.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive Teams Bot Framework activity events |

## Events Published

_None — this adapter forwards events to Teams but does not publish to the bus from inbound events._

## Events Subscribed

| Event | Action |
|---|---|
| `task.completed` | Posts an Adaptive Card notification to the Teams channel |
| `review.requested` | Posts an approval request card to the Teams channel |
| `escalation.created` | Posts an escalation alert card to the Teams channel |
