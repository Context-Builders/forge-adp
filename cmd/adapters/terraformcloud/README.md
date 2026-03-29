# Terraform Cloud Adapter

Port: **:19104**

Bridges Terraform Cloud workspace run events with the Forge event bus and exposes a REST bridge for the DevOps agent to list workspaces, create runs, and query run status.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `TFC_TOKEN` | Yes | Terraform Cloud team or organisation token |
| `TFC_ORGANIZATION` | Yes | Terraform Cloud organisation name |
| `TFC_WEBHOOK_HMAC_KEY` | No | Secret for HMAC-SHA512 signature validation (`X-TFE-Notification-Signature`) |

## Webhook

**Path:** `/webhook`
**Security:** HMAC-SHA512 — set `TFC_WEBHOOK_HMAC_KEY`; adapter checks the `X-TFE-Notification-Signature` header.

Register at: **Terraform Cloud → Organisation or Workspace → Settings → Notifications → Create a notification** → type: Webhook, URL: `https://<host>:19104/webhook`. Subscribe to run state change events.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive Terraform Cloud run notification events |
| `GET` | `/api/v1/workspaces` | List workspaces in the organisation |
| `GET` | `/api/v1/runs` | List runs for a workspace (`?workspace=`) |
| `POST` | `/api/v1/runs` | Create a new run for a workspace |

## Events Published

| Event | Trigger |
|---|---|
| `task.completed` | Run status `applied` |
| `deployment.approved` | Run status `planned_and_finished` |
| `task.failed` | Run status `errored` or `canceled` |

## Events Subscribed

_None — this adapter is inbound only._
