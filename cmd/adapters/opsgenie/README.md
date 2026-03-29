# Opsgenie Adapter

Port: **:19099**

Bridges Opsgenie alert events with the Forge event bus and exposes a REST bridge for the SRE agent to create, close, and acknowledge alerts.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `OPSGENIE_API_KEY` | Yes | Opsgenie API key |

## Webhook

**Path:** `/webhook`
**Security:** HMAC-SHA256 — Opsgenie signs requests using the integration's webhook secret.

Register at: **Opsgenie → Settings → Integrations → Add Integration → Webhook** — set the URL to `https://<host>:19099/webhook` and configure the HMAC secret.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive Opsgenie alert events |
| `POST` | `/api/v1/alerts` | Create a new Opsgenie alert |
| `DELETE` | `/api/v1/alerts` | Close an alert by ID (`?id=`) |
| `PATCH` | `/api/v1/alerts` | Acknowledge an alert (`?id=`) |

## Events Published

| Event | Trigger |
|---|---|
| `escalation.created` | Alert action `Create` |
| `task.completed` | Alert action `Close` |

## Events Subscribed

| Event | Action |
|---|---|
| `escalation.created` | Creates an Opsgenie alert for escalations originating from other sources |
