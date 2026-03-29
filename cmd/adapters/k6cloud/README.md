# k6 Cloud Adapter

Port: **:19135**

Receives load test run completion events from k6 Cloud via webhook and publishes pass/fail/threshold-breach outcomes to the Forge event bus. Also exposes a REST bridge for the SRE and QA agents to list runs and inspect threshold results.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `K6_CLOUD_API_TOKEN` | Yes | k6 Cloud API token |
| `K6_PROJECT_ID` | No | Default k6 Cloud project ID (used when no `project_id` query parameter is provided) |

## Webhook

**Path:** `/webhook`
**Security:** Bearer token (`Authorization: Bearer <K6_CLOUD_API_TOKEN>`).

Register at: **k6 Cloud → Project → Notifications → Webhooks → Add webhook** → set URL to `https://<host>:19135/webhook`.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive k6 Cloud test run events |
| `GET` | `/api/v1/runs` | List recent test runs (`?project_id=`) |
| `POST` | `/api/v1/runs` | Trigger a new test run |
| `GET` | `/api/v1/thresholds` | Get threshold results for a run (`?run_id=`) |

## Events Published

| Event | Trigger |
|---|---|
| `task.completed` | `TEST_FINISHED` with result `passed` and no breached thresholds |
| `escalation.created` | `TEST_FINISHED` with breached thresholds or a non-passing result |
| `task.failed` | `TEST_ABORTED` |

## Events Subscribed

| Event | Action |
|---|---|
| `deployment.approved` | Triggers a k6 Cloud test run (`POST /test-runs`) for the test ID specified in `k6_test_id` payload field |
