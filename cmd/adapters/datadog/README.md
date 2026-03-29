# Datadog Adapter

Port: **:19100**

Receives Datadog monitor alert webhooks and publishes escalation and resolution events to the Forge event bus. Also forwards Forge escalation events back to Datadog as events, and exposes a REST bridge for the SRE agent to post custom events.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `DATADOG_API_KEY` | Yes | Datadog API key |
| `DATADOG_APP_KEY` | No | Datadog application key (required for some query endpoints) |
| `DATADOG_WEBHOOK_SECRET` | No | Shared secret for validating inbound webhook signatures (`X-Datadog-Signature`) |

## Webhook

**Path:** `/webhook`
**Security:** Shared secret — set `DATADOG_WEBHOOK_SECRET`; adapter checks the `X-Datadog-Signature` header.

Register at: **Datadog → Integrations → Webhooks → New** → set URL to `https://<host>:19100/webhook`. Then reference the webhook in monitor notification messages using `@webhook-forge`.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive Datadog monitor alert events |
| `POST` | `/api/v1/events` | Post a custom event to Datadog |

## Events Published

| Event | Trigger |
|---|---|
| `escalation.created` | Monitor alert state `Triggered` or `No Data` |
| `task.completed` | Monitor alert state `Recovered` |

## Events Subscribed

| Event | Action |
|---|---|
| `escalation.created` | Posts a Datadog event for escalations originating from other sources (loop-safe: skips events where `source == "datadog"`) |
| `task.failed` | Posts a Datadog event for task failures |
