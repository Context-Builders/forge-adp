# Azure Monitor Adapter

Port: **:19119**

Receives Azure Monitor alert notifications and Azure DevOps build completion events, publishing them to the Forge event bus. Exposes a REST bridge for the SRE agent to query active alerts and pipeline builds.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `AZURE_DEVOPS_ORG` | Yes | Azure DevOps organisation name (used for build queries) |
| `AZURE_DEVOPS_PAT` | Yes | Personal Access Token with `Build (Read)` scope |

## Webhook

**Path:** `/webhook`
**Security:** Basic auth using `AZURE_DEVOPS_PAT`.

Register at:
- **Azure Portal → Monitor → Alerts → Action groups** → add a Webhook action pointing to `https://<host>:19119/webhook`.
- **Azure DevOps → Project → Service hooks → Web hooks** → subscribe to Build completed events.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive Azure Monitor alert and ADO build events |
| `GET` | `/api/v1/alerts` | List active Azure Monitor alerts |
| `GET` | `/api/v1/builds` | List recent ADO pipeline builds (`?project=`) |

## Events Published

| Event | Trigger |
|---|---|
| `escalation.created` | Alert state `Fired` |
| `task.completed` | Alert state `Resolved` or ADO build result `succeeded` |
| `task.failed` | ADO build result `failed` or `canceled` |

## Events Subscribed

_None — this adapter is inbound only._
