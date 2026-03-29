# Azure DevOps Repos Adapter

Port: **:19110**

Bridges Azure DevOps pull request events with the Forge event bus and exposes a REST bridge for the Backend Developer and Architect agents to list repositories and pull requests.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `AZURE_DEVOPS_ORG` | Yes | Azure DevOps organisation name |
| `AZURE_DEVOPS_PROJECT` | Yes | Azure DevOps project name |
| `AZURE_DEVOPS_PAT` | Yes | Personal Access Token with `Code (Read)` scope |
| `AZURE_DEVOPS_REPOS_WEBHOOK_SECRET` | No | Secret for HMAC-SHA256 signature validation (`X-Hub-Signature-256`) |

## Webhook

**Path:** `/webhook`
**Security:** HMAC-SHA256 — set `AZURE_DEVOPS_REPOS_WEBHOOK_SECRET`; adapter checks the `X-Hub-Signature-256` header.

Register at: **Azure DevOps → Project → Project Settings → Service hooks → Web hooks** — subscribe to Pull Request Created, Updated, and Merged events pointing to `https://<host>:19110/webhook`.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive Azure DevOps pull request events |
| `GET` | `/api/v1/pullrequests` | List pull requests (`?repo=`) |
| `GET` | `/api/v1/repos` | List repositories in the project |

## Events Published

| Event | Trigger |
|---|---|
| `review.requested` | Pull request created (`git.pullrequest.created`) |
| `task.completed` | Pull request merged |
| `review.rejected` | Pull request abandoned/closed |

## Events Subscribed

_None — this adapter is inbound only._
