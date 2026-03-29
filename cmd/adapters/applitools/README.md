# Applitools Adapter

Port: **:19136**

Receives visual test results from Applitools Eyes via webhook and publishes pass/fail/unresolved outcomes to the Forge event bus. Also exposes a REST bridge for the QA agent to query batches, results, and baselines directly.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `APPLITOOLS_API_KEY` | Yes | Applitools account API key |
| `APPLITOOLS_TEAM_ID` | No | Team ID scoping (used in API calls) |
| `APPLITOOLS_WEBHOOK_SECRET` | No | Shared secret for validating inbound webhook signatures (`X-Applitools-Signature`) |

## Webhook

**Path:** `/webhook`
**Security:** Shared secret — set `APPLITOOLS_WEBHOOK_SECRET`; adapter checks the `X-Applitools-Signature` header.

Register at: **Applitools → Admin → Hooks → Add webhook** → set URL to `https://<host>:19136/webhook`.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive Applitools batch completion events |
| `GET` | `/api/v1/batches` | List recent test batches |
| `GET` | `/api/v1/results` | List test results for a batch (`?batch_id=`) |
| `GET` | `/api/v1/baselines` | List visual baselines |
| `DELETE` | `/api/v1/baselines` | Delete a baseline (`?baseline_id=`) |

## Events Published

| Event | Trigger |
|---|---|
| `task.completed` | Batch status `Passed` |
| `task.blocked` | Batch status `Failed` |
| `review.requested` | Batch status `Unresolved` |

## Events Subscribed

| Event | Action |
|---|---|
| `review.approved` | Logs that Applitools baselines can be accepted for the associated batch |
