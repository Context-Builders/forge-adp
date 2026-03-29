# ArgoCD Adapter

Port: **:19113**

Bridges ArgoCD application sync events with the Forge event bus and exposes a REST bridge for the DevOps agent to list applications and trigger syncs.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `ARGOCD_URL` | Yes | Base URL of your ArgoCD instance (e.g. `https://argocd.example.com`) |
| `ARGOCD_TOKEN` | Yes | ArgoCD API bearer token |

## Webhook

**Path:** `/webhook`
**Security:** Bearer token — ArgoCD sends requests with an `Authorization: Bearer <ARGOCD_TOKEN>` header.

Register at: **ArgoCD → Settings → Webhooks**, or configure via `argocd-notifications` to POST to `https://<host>:19113/webhook` on sync events.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive ArgoCD sync status events |
| `GET` | `/api/v1/applications` | List ArgoCD applications |
| `POST` | `/api/v1/sync` | Trigger a sync for an application (`?app=`) |

## Events Published

| Event | Trigger |
|---|---|
| `deployment.approved` | Sync phase `Succeeded` |
| `task.started` | Sync phase `Running` |
| `task.failed` | Sync phase `Failed` or `Error` |

## Events Subscribed

| Event | Action |
|---|---|
| `deployment.requested` | Triggers an ArgoCD sync for the application named in `argocd_app` payload field |
