# PagerDuty Adapter

Port: **:19098**

Bridges PagerDuty incident events with the Forge event bus and exposes a REST bridge for the SRE agent to create and resolve incidents.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `PAGERDUTY_API_KEY` | Yes | PagerDuty v2 API key (full access or scoped with incidents read/write) |
| `PAGERDUTY_SERVICE_ID` | Yes | PagerDuty service ID to associate incidents with |
| `PAGERDUTY_FROM_EMAIL` | Yes | Email address of the PagerDuty user acting as requester (required by the PagerDuty v2 API) |

## Webhook

**Path:** `/webhook`
**Security:** PagerDuty signs requests with `X-PagerDuty-Signature`. Configure the signing secret in PagerDuty webhook settings.

Register at: **PagerDuty → Integrations → Generic Webhooks (V3) → New Webhook** — subscribe to `incident.triggered` and `incident.resolved` events. Set URL to `https://<host>:19098/webhook`.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive PagerDuty incident events |
| `POST` | `/api/v1/incidents` | Create a new PagerDuty incident |
| `PUT` | `/api/v1/incidents` | Resolve or acknowledge an incident (`?id=`) |

## Events Published

| Event | Trigger |
|---|---|
| `escalation.created` | Incident event `incident.triggered` |
| `task.completed` | Incident event `incident.resolved` |

## Events Subscribed

| Event | Action |
|---|---|
| `escalation.created` | Creates a PagerDuty incident for escalations originating from other sources |
