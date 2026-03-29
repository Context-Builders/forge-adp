# SonarQube Adapter

Port: **:19103**

Receives SonarQube analysis completion webhooks and publishes quality gate outcomes to the Forge event bus. Also exposes a REST bridge for the SecOps and QA agents to query issues and quality gate status.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `SONARQUBE_URL` | Yes | SonarQube base URL (e.g. `https://sonarqube.example.com`) |
| `SONARQUBE_TOKEN` | Yes | SonarQube user or project token |
| `SONARQUBE_WEBHOOK_SECRET` | No | Secret for HMAC-SHA256 signature validation (`X-Sonar-Webhook-HMAC-SHA256`) |

## Webhook

**Path:** `/webhook`
**Security:** HMAC-SHA256 — set `SONARQUBE_WEBHOOK_SECRET`; adapter checks the `X-Sonar-Webhook-HMAC-SHA256` header.

Register at: **SonarQube → Administration → Configuration → Webhooks → Create** — set URL to `https://<host>:19103/webhook` and enter the secret. Alternatively, configure it per-project under Project Settings → Webhooks.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive SonarQube analysis events |
| `GET` | `/api/v1/issues` | List issues for a project (`?project=`) |
| `GET` | `/api/v1/qualitygates` | Get quality gate status for a project (`?project=`) |

## Events Published

| Event | Trigger |
|---|---|
| `escalation.created` | Analysis completed with quality gate status `ERROR` |
| `task.completed` | Analysis completed with quality gate status `OK` |
| `task.failed` | Analysis status `FAILED` or `CANCELLED` |

## Events Subscribed

_None — this adapter is inbound only._
