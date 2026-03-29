# Jenkins Adapter

Port: **:19111**

Bridges Jenkins build lifecycle events with the Forge event bus and exposes a REST bridge for the DevOps agent to list jobs and trigger builds.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `JENKINS_URL` | Yes | Jenkins base URL (e.g. `https://jenkins.example.com`) |
| `JENKINS_USER` | Yes | Jenkins username |
| `JENKINS_API_TOKEN` | Yes | Jenkins API token for the above user |
| `JENKINS_WEBHOOK_SECRET` | No | Secret for HMAC-SHA256 signature validation (`X-Jenkins-Signature`) |

## Webhook

**Path:** `/webhook`
**Security:** HMAC-SHA256 — set `JENKINS_WEBHOOK_SECRET`; adapter checks the `X-Jenkins-Signature` header.

Register at: **Jenkins → Manage Jenkins → Configure System → Notification** (requires the [Notification Plugin](https://plugins.jenkins.io/notification/)), or per-job under **Post-build Actions → Notification** → set the endpoint URL to `https://<host>:19111/webhook`.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive Jenkins build lifecycle events |
| `GET` | `/api/v1/builds` | List recent builds for a job (`?job=`) |
| `POST` | `/api/v1/builds` | Trigger a new build for a job (`?job=`) |
| `GET` | `/api/v1/jobs` | List all Jenkins jobs |

## Events Published

| Event | Trigger |
|---|---|
| `task.started` | Build phase `STARTED` |
| `task.completed` | Build phase `COMPLETED` with status `SUCCESS` |
| `task.failed` | Build phase `COMPLETED` with status `FAILURE`, `ABORTED`, or `UNSTABLE` |

## Events Subscribed

| Event | Action |
|---|---|
| `deployment.requested` | Triggers a Jenkins build (`POST /job/<job>/build`) for the job named in `jenkins_job` payload field |
