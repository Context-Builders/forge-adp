# Cypress Cloud Adapter

Port: **:19134**

Receives test run completion events from Cypress Cloud via webhook and publishes pass/fail outcomes to the Forge event bus. Also exposes a REST bridge for the QA agent to list runs and inspect individual spec instances.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `CYPRESS_RECORD_KEY` | Yes | Cypress Cloud record key |
| `CYPRESS_PROJECT_ID` | Yes | Cypress Cloud project ID |
| `CYPRESS_WEBHOOK_SECRET` | No | Shared secret for validating inbound webhook signatures (`X-Cypress-Secret`) |

## Webhook

**Path:** `/webhook`
**Security:** Shared secret — set `CYPRESS_WEBHOOK_SECRET`; adapter checks the `X-Cypress-Secret` header.

Register at: **Cypress Cloud → Project Settings → Notifications → Custom webhooks** → set URL to `https://<host>:19134/webhook`.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive Cypress Cloud run completion events |
| `GET` | `/api/v1/runs` | List recent test runs |
| `GET` | `/api/v1/instances` | List spec instances for a run (`?run_id=`) |

## Events Published

| Event | Trigger |
|---|---|
| `task.completed` | Run status `RUN_COMPLETED` with all specs passing |
| `task.blocked` | Run completed with spec failures or errors |

## Events Subscribed

| Event | Action |
|---|---|
| `review.approved` | Logs that a Cypress regression run can be triggered for the approved change |
