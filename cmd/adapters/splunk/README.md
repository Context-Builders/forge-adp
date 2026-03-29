# Splunk Adapter

Port: **:19117**

Receives Splunk alert webhook notifications and forwards Forge bus events to Splunk HEC (HTTP Event Collector) for centralised log aggregation and SIEM correlation. Also exposes a REST bridge for search queries.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `SPLUNK_HEC_URL` | Yes | Splunk HEC endpoint URL (e.g. `https://splunk.example.com:8088`) |
| `SPLUNK_HEC_TOKEN` | Yes | Splunk HEC token (`Authorization: Splunk <token>`) |
| `SPLUNK_URL` | No | Splunk management API URL (e.g. `https://splunk.example.com:8089`) |
| `SPLUNK_TOKEN` | No | Splunk management API session token (enables search endpoints) |

## Webhook

**Path:** `/webhook`
**Security:** HEC token header (`Authorization: Splunk <SPLUNK_HEC_TOKEN>`).

Register at: **Splunk → Settings → Alert actions → Webhook** — set the URL to `https://<host>:19117/webhook` in alert action configurations.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive Splunk alert webhook events |
| `POST` | `/api/v1/search` | Submit a Splunk search job |
| `GET` | `/api/v1/jobs` | List Splunk search jobs |

## Events Published

| Event | Trigger |
|---|---|
| `escalation.created` | Splunk alert webhook received |

## Events Subscribed

| Event | Action |
|---|---|
| `task.completed` | Forwards event to Splunk HEC |
| `task.failed` | Forwards event to Splunk HEC |
| `escalation.created` | Forwards event to Splunk HEC |
