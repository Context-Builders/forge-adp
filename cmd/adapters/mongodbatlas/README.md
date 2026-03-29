# MongoDB Atlas Adapter

Port: **:19106**

Receives MongoDB Atlas alert webhook notifications and publishes them to the Forge event bus. Also exposes a REST bridge for the DBA and SRE agents to query active alerts and cluster status.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `MONGODB_ATLAS_PUBLIC_KEY` | Yes | Atlas API public key |
| `MONGODB_ATLAS_PRIVATE_KEY` | Yes | Atlas API private key |
| `MONGODB_ATLAS_WEBHOOK_SECRET` | No | Secret for HMAC-SHA1 signature validation (`X-MMS-Signature`) |

## Webhook

**Path:** `/webhook`
**Security:** HMAC-SHA1 — set `MONGODB_ATLAS_WEBHOOK_SECRET`; adapter checks the `X-MMS-Signature` header.

Register at: **Atlas → Project Settings → Integrations → Webhook → Add** — set URL to `https://<host>:19106/webhook`.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive Atlas alert events |
| `GET` | `/api/v1/alerts` | List active alerts for a group (`?group_id=`) |
| `GET` | `/api/v1/clusters` | List clusters for a group (`?group_id=`) |

## Events Published

| Event | Trigger |
|---|---|
| `escalation.created` | Alert status `OPEN` |
| `task.completed` | Alert status `CLOSED` |

## Events Subscribed

| Event | Action |
|---|---|
| `escalation.created` | Logs the escalation (loop-safe: skips events where `source == "mongodbatlas"`) |
