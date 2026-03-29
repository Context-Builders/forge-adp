# New Relic Adapter

Port: **:19116**

Receives New Relic alert webhook notifications and publishes them to the Forge event bus. Also exposes a REST bridge for the SRE agent to query active alerts and post custom events.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `NEW_RELIC_API_KEY` | Yes | New Relic User API key |
| `NEW_RELIC_ACCOUNT_ID` | Yes | New Relic account ID |
| `NEW_RELIC_WEBHOOK_SECRET` | No | API key sent in the `X-Api-Key` header by New Relic for validation |

## Webhook

**Path:** `/webhook`
**Security:** API key header — New Relic sends the configured API key as `X-Api-Key`.

Register at: **New Relic → Alerts → Notification channels → New channel → Webhook** — set URL to `https://<host>:19116/webhook` and configure the channel as part of an alert policy.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive New Relic alert events |
| `GET` | `/api/v1/alerts` | List active alert violations |
| `POST` | `/api/v1/events` | Post a custom New Relic event |

## Events Published

| Event | Trigger |
|---|---|
| `escalation.created` | Alert violation opened with priority `CRITICAL` |
| `task.completed` | Alert violation closed/resolved |

## Events Subscribed

| Event | Action |
|---|---|
| `escalation.created` | Logs the escalation (loop-safe: skips events where `source == "newrelic"`) |
| `task.failed` | Logs the failure for observability |
