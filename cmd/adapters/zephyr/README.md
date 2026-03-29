# Zephyr Scale Adapter

Port: **:19132**

Bridges Zephyr Scale (TM4J) test cycle and execution events with the Forge event bus and exposes a REST bridge for the QA agent to create test cycles, log execution results, and list test cases.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `ZEPHYR_API_TOKEN` | Yes | Zephyr Scale API token |
| `ZEPHYR_PROJECT_KEY` | Yes | Jira/Zephyr project key (e.g. `PROJ`) |
| `ZEPHYR_WEBHOOK_SECRET` | No | Shared secret for validating inbound webhook events (`X-Zephyr-Secret`) |

## Webhook

**Path:** `/webhook`
**Security:** Shared secret — set `ZEPHYR_WEBHOOK_SECRET`; adapter checks the `X-Zephyr-Secret` header.

Register at: **Zephyr Scale (Jira app) → Project Settings → Webhooks → Add webhook** — subscribe to Test Cycle and Test Execution events. Set URL to `https://<host>:19132/webhook`.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive Zephyr Scale test cycle and execution events |
| `GET` | `/api/v1/cycles` | List test cycles (`?project_key=`) |
| `POST` | `/api/v1/cycles` | Create a new test cycle |
| `GET` | `/api/v1/executions` | List executions for a cycle (`?cycle_key=`) |
| `POST` | `/api/v1/executions` | Create a test execution result |
| `GET` | `/api/v1/cases` | List test cases for a project (`?project_key=`) |

## Events Published

| Event | Trigger |
|---|---|
| `task.completed` | Test cycle status `DONE` or `PASSED` |
| `task.blocked` | Test cycle status `FAILED` |
| `escalation.created` | Test execution status `FAIL` |

## Events Subscribed

| Event | Action |
|---|---|
| `review.approved` | Creates a new Zephyr test cycle (`POST /testcycles`) for the approved change (requires `zephyr_project_key` or `jira_key` in payload) |
