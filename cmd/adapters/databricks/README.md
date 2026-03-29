# Databricks Adapter

Port: **:19107**

Bridges Databricks job run lifecycle events with the Forge event bus and exposes a REST bridge for the Data Science agent to list jobs and runs.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `DATABRICKS_HOST` | Yes | Databricks workspace URL (e.g. `https://your-workspace.azuredatabricks.net`) |
| `DATABRICKS_TOKEN` | Yes | Databricks personal access token |

## Webhook

**Path:** `/webhook`
**Security:** Bearer token (`Authorization: Bearer <DATABRICKS_TOKEN>`).

Register at: **Databricks → Jobs → Edit job → Notifications** — configure a webhook notification to POST to `https://<host>:19107/webhook` on run success/failure, or use the [Databricks Webhooks API](https://docs.databricks.com/en/administration-guide/workspace/webhooks.html) to register a job notification endpoint.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive Databricks job run events |
| `GET` | `/api/v1/jobs` | List Databricks jobs |
| `GET` | `/api/v1/runs` | List runs for a job (`?job_id=`) |

## Events Published

| Event | Trigger |
|---|---|
| `task.completed` | Run lifecycle state `TERMINATED` with result `SUCCESS` |
| `task.failed` | Run lifecycle state `TERMINATED` with result `FAILURE` |

## Events Subscribed

| Event | Action |
|---|---|
| `deployment.requested` | Triggers a Databricks job run (`POST /jobs/run-now`) for the job ID specified in `databricks_job_id` payload field |
