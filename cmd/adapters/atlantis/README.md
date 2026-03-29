# Atlantis Adapter

Port: **:19105**

Receives Terraform plan and apply results from Atlantis via webhook and publishes outcomes to the Forge event bus. Also exposes a REST bridge for the DevOps agent to trigger plans and applies directly.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `ATLANTIS_URL` | Yes | Base URL of your Atlantis server (e.g. `https://atlantis.example.com`) |
| `ATLANTIS_TOKEN` | Yes | Atlantis API token |
| `ATLANTIS_WEBHOOK_SECRET` | No | Secret for validating inbound webhook HMAC-SHA256 signatures (`X-Atlantis-Signature`) |

## Webhook

**Path:** `/webhook`
**Security:** HMAC-SHA256 — set `ATLANTIS_WEBHOOK_SECRET`; adapter checks the `X-Atlantis-Signature` header.

Register at: **Atlantis `atlantis.yaml`** → `webhooks:` block, or your VCS provider's repo webhook settings pointing to `https://<host>:19105/webhook`.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive Atlantis plan/apply events |
| `POST` | `/api/v1/plan` | Trigger a Terraform plan |
| `POST` | `/api/v1/apply` | Trigger a Terraform apply |

## Events Published

| Event | Trigger |
|---|---|
| `deployment.requested` | Plan phase succeeded |
| `task.completed` | Apply phase succeeded |
| `task.failed` | Plan or apply failure/error |

## Events Subscribed

| Event | Action |
|---|---|
| `deployment.approved` | Triggers an Atlantis apply for the repo and workspace specified in `atlantis_repo` and `atlantis_workspace` payload fields |
