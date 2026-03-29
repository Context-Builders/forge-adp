# Azure DevOps Boards Adapter

Port: **:19124**

Bridges Azure DevOps Work Items with the Forge event bus. Translates work item lifecycle events (created, updated, deleted) into Forge task events, and allows the PM and Architect agents to create and update work items via a REST bridge.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `AZURE_DEVOPS_ORG` | Yes | Azure DevOps organisation name |
| `AZURE_DEVOPS_PROJECT` | Yes | Azure DevOps project name |
| `AZURE_DEVOPS_PAT` | Yes | Personal Access Token with `Work Items (Read & Write)` scope |

## Webhook

**Path:** `/webhook`
**Security:** Basic auth using `AZURE_DEVOPS_PAT`.

Register at: **Azure DevOps → Project → Project Settings → Service hooks → Web hooks** — subscribe to Work Item Created, Updated, and Deleted events pointing to `https://<host>:19124/webhook`.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive Azure DevOps work item events |
| `GET` | `/api/v1/workitems` | Fetch a work item by ID (`?id=`) |
| `PATCH` | `/api/v1/workitems` | Update a work item's fields (`?id=`) |

## Events Published

| Event | Trigger |
|---|---|
| `task.created` | Work item created and tagged with `forge` |
| `task.completed` | Work item state transitions to `Done` |
| `task.failed` | Work item state transitions to `Removed` |

## Events Subscribed

_None — this adapter is inbound only._
