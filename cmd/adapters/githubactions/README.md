# GitHub Actions / GitLab CI Adapter

Port: **:19114**

A unified adapter that receives workflow completion events from both GitHub Actions and GitLab CI pipelines, publishing outcomes to the Forge event bus. Shares webhook secrets with the GitHub and GitLab adapters respectively.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `GITHUB_ACTIONS_WEBHOOK_SECRET` | No | Secret for HMAC-SHA256 validation of GitHub Actions events (`X-Hub-Signature-256`) |
| `GITLAB_WEBHOOK_SECRET` | No | Static token for GitLab CI events (`X-Gitlab-Token`); can be shared with the GitLab adapter |

## Webhook

**Paths:**
- `/webhook/github` — GitHub Actions `workflow_run` events
- `/webhook/gitlab` — GitLab CI pipeline events

**Security:**
- GitHub: HMAC-SHA256 (`GITHUB_ACTIONS_WEBHOOK_SECRET` → `X-Hub-Signature-256`)
- GitLab: Static token (`GITLAB_WEBHOOK_SECRET` → `X-Gitlab-Token`)

Register at:
- **GitHub:** repo/org → Settings → Webhooks → subscribe to `workflow_run` events → URL `https://<host>:19114/webhook/github`
- **GitLab:** repo/group → Settings → Webhooks → subscribe to Pipeline events → URL `https://<host>:19114/webhook/gitlab`

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook/github` | Receive GitHub Actions workflow_run events |
| `POST` | `/webhook/gitlab` | Receive GitLab CI pipeline events |

## Events Published

| Event | Trigger |
|---|---|
| `task.completed` | GitHub workflow conclusion `success`; GitLab pipeline status `success` |
| `task.failed` | GitHub workflow conclusion `failure`; GitLab pipeline status `failed` or `canceled` |

## Events Subscribed

_None — this adapter is inbound only._
