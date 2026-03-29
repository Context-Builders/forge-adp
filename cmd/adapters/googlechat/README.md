# Google Chat Adapter

Port: **:19094**

Sends Forge event notifications to a Google Chat space and receives interactive card click events from Chat. Notifies on task completions, review requests, and escalations.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `GOOGLE_CHAT_WEBHOOK_URL` | Yes | Incoming webhook URL for the target Chat space |
| `GOOGLE_CHAT_VERIFICATION_TOKEN` | No | Verification token for validating inbound Chat events |

## Webhook

**Path:** `/webhook`
**Security:** Verification token — set `GOOGLE_CHAT_VERIFICATION_TOKEN`; adapter checks the token in the request body.

Register at: **Google Cloud Console → Chat API → Configuration** → set the App URL to `https://<host>:19094/webhook` for interactive card callbacks.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive Google Chat interactive events |

## Events Published

_None — this adapter forwards events to Chat but does not publish to the bus._

## Events Subscribed

| Event | Action |
|---|---|
| `task.completed` | Sends a Chat message to the configured space |
| `review.requested` | Sends a Chat message requesting human review |
| `escalation.created` | Sends a Chat alert message to the configured space |
