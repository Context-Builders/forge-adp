# Vercel Adapter

Port: **:19108**

Bridges Vercel deployment lifecycle events with the Forge event bus and exposes a REST bridge for the DevOps agent to list deployments and projects.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `VERCEL_TOKEN` | Yes | Vercel personal access token or team token |
| `VERCEL_TEAM_ID` | No | Vercel team ID (`team_...`) — required for team-owned projects |
| `VERCEL_WEBHOOK_SECRET` | No | Secret for HMAC-SHA1 signature validation (`X-Vercel-Signature`) |

## Webhook

**Path:** `/webhook`
**Security:** HMAC-SHA1 — set `VERCEL_WEBHOOK_SECRET`; adapter checks the `X-Vercel-Signature` header.

Register at: **Vercel → Team Settings → Webhooks → Add Webhook** — subscribe to Deployment events. Set URL to `https://<host>:19108/webhook`.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive Vercel deployment events |
| `GET` | `/api/v1/deployments` | List recent deployments (`?project=`) |
| `GET` | `/api/v1/projects` | List Vercel projects |

## Events Published

| Event | Trigger |
|---|---|
| `task.completed` | Deployment state `READY` (succeeded) |
| `deployment.approved` | Deployment event `deployment.ready` |
| `task.failed` | Deployment state `ERROR` |

## Events Subscribed

_None — this adapter is inbound only._
