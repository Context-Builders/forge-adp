# Snyk Adapter

Port: **:19102**

Receives Snyk vulnerability webhook events and publishes critical/high severity findings to the Forge event bus as escalations. Also exposes a REST bridge for the SecOps agent to query projects and vulnerabilities.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `SNYK_API_TOKEN` | Yes | Snyk API token |
| `SNYK_ORG_ID` | Yes | Snyk organisation ID |
| `SNYK_WEBHOOK_SECRET` | No | Secret for HMAC-SHA256 signature validation (`X-Snyk-Signature`) |

## Webhook

**Path:** `/webhook`
**Security:** HMAC-SHA256 — set `SNYK_WEBHOOK_SECRET`; adapter checks the `X-Snyk-Signature` header.

Register at: **Snyk → Settings → Notifications → Webhook → Create webhook** — set URL to `https://<host>:19102/webhook`.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive Snyk project vulnerability events |
| `GET` | `/api/v1/vulnerabilities` | List aggregated issues for a project (`?project_id=`) |
| `GET` | `/api/v1/projects` | List Snyk projects for the configured organisation |

## Events Published

| Event | Trigger |
|---|---|
| `escalation.created` | New issues with severity `critical` or `high` detected in a project |

## Events Subscribed

| Event | Action |
|---|---|
| `task.created` | Logs when a new task references a Snyk project via `snyk_project_id` payload field |
