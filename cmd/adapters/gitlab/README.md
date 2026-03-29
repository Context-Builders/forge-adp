# GitLab Adapter

Port: **:19095**

Bridges GitLab merge request and pipeline events with the Forge event bus and exposes a REST bridge for the Backend Developer agent to create branches, open merge requests, and query commits.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `GITLAB_TOKEN` | Yes | GitLab personal access token or project access token with `api` scope |
| `GITLAB_BASE_URL` | No | GitLab instance base URL (defaults to `https://gitlab.com`) |
| `GITLAB_WEBHOOK_SECRET` | No | Static token for validating inbound webhook events (`X-Gitlab-Token`) |

## Webhook

**Path:** `/webhook`
**Security:** Static token — set `GITLAB_WEBHOOK_SECRET`; adapter checks the `X-Gitlab-Token` header.

Register at: **GitLab → repository or group → Settings → Webhooks** — subscribe to Merge Request and Pipeline events. Set URL to `https://<host>:19095/webhook` and enter the secret token.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive GitLab events |
| `POST` | `/api/v1/branches` | Create a branch |
| `POST` | `/api/v1/mergerequests` | Open a merge request |
| `GET` | `/api/v1/commits` | List commits for a branch (`?project=&branch=`) |

## Events Published

| Event | Trigger |
|---|---|
| `review.requested` | Merge request action `open` |
| `task.completed` | Merge request action `merge` |
| `review.rejected` | Merge request action `close` |
| `deployment.approved` | Pipeline status `success` |
| `task.failed` | Pipeline status `failed` |

## Events Subscribed

_None — this adapter is inbound only._
