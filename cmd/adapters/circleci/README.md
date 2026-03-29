# CircleCI Adapter

Port: **:19112**

Bridges CircleCI workflow and pipeline events with the Forge event bus and exposes a REST bridge for the DevOps agent to list pipelines and workflows.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `CIRCLECI_TOKEN` | Yes | CircleCI personal or project API token |
| `CIRCLECI_WEBHOOK_SECRET` | No | Secret for HMAC-SHA256 signature validation (`Circleci-Signature` header, `v1=` prefix) |

## Webhook

**Path:** `/webhook`
**Security:** HMAC-SHA256 — set `CIRCLECI_WEBHOOK_SECRET`; adapter checks the `Circleci-Signature` header (value is prefixed with `v1=`).

Register at: **CircleCI → Project Settings → Webhooks → Add Webhook** — subscribe to workflow completion events. Set URL to `https://<host>:19112/webhook`.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive CircleCI workflow events |
| `GET` | `/api/v1/pipelines` | List recent pipelines (`?project_slug=`) |
| `GET` | `/api/v1/workflows` | List workflows for a pipeline (`?pipeline_id=`) |

## Events Published

| Event | Trigger |
|---|---|
| `task.completed` | Workflow completed with status `success` |
| `task.failed` | Workflow completed with status `failed` or `error` |

## Events Subscribed

_None — this adapter is inbound only._
