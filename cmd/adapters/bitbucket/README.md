# Bitbucket Adapter

Port: **:19109**

Bridges Bitbucket Cloud pull request events with the Forge event bus and exposes a REST bridge for the Backend Developer agent to list pull requests and post review comments.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `BITBUCKET_USERNAME` | Yes | Bitbucket username |
| `BITBUCKET_APP_PASSWORD` | Yes | Bitbucket App Password with `Repositories: Read` and `Pull requests: Read & Write` scopes |
| `BITBUCKET_WEBHOOK_SECRET` | No | Secret for HMAC-SHA256 signature validation (`X-Hub-Signature`) |

## Webhook

**Path:** `/webhook`
**Security:** HMAC-SHA256 — set `BITBUCKET_WEBHOOK_SECRET`; adapter checks the `X-Hub-Signature` header.

Register at: **Bitbucket → repository → Repository settings → Webhooks → Add webhook** — subscribe to Pull Request Created, Fulfilled, and Rejected events. Set URL to `https://<host>:19109/webhook`.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive Bitbucket pull request events |
| `GET` | `/api/v1/pullrequests` | List open pull requests (`?repo=`) |
| `POST` | `/api/v1/comments` | Post a comment on a pull request (`?repo=&pr=`) |

## Events Published

| Event | Trigger |
|---|---|
| `review.requested` | `pullrequest:created` |
| `task.completed` | `pullrequest:fulfilled` (merged) |
| `review.rejected` | `pullrequest:rejected` (declined) |

## Events Subscribed

_None — this adapter is inbound only._
