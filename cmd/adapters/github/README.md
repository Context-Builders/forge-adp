# GitHub Adapter

Port: **:19091**

Bridges GitHub repository events (pull requests, check suites) with the Forge event bus and exposes a REST bridge for the Backend Developer agent to create branches, open pull requests, and query commits.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `GITHUB_TOKEN` | Yes | GitHub personal access token or GitHub App installation token with `repo` scope |
| `GITHUB_WEBHOOK_SECRET` | No | Secret for HMAC-SHA256 signature validation (`X-Hub-Signature-256`) |

## Webhook

**Path:** `/webhook`
**Security:** HMAC-SHA256 — set `GITHUB_WEBHOOK_SECRET`; adapter checks the `X-Hub-Signature-256` header.

Register at: **GitHub → repository or organisation → Settings → Webhooks → Add webhook** — subscribe to Pull Request and Check Suite events. Set URL to `https://<host>:19091/webhook`.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive GitHub events |
| `POST` | `/api/v1/branches` | Create a branch (`?repo=&branch=&sha=`) |
| `POST` | `/api/v1/pulls` | Open a pull request |
| `GET` | `/api/v1/commits` | List commits for a branch (`?repo=&branch=`) |

## Events Published

| Event | Trigger |
|---|---|
| `review.requested` | `PullRequestEvent` action `opened` |
| `task.completed` | `PullRequestEvent` action `closed` and merged |
| `review.rejected` | `PullRequestEvent` action `closed` and not merged |
| `task.completed` | `CheckSuiteEvent` conclusion `success` |
| `task.failed` | `CheckSuiteEvent` conclusion `failure` |

## Events Subscribed

_None — this adapter is inbound only._
