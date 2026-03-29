# Linear Adapter

Port: **:19097**

Bridges Linear issue lifecycle events with the Forge event bus and exposes a REST bridge for the PM agent to create issues and trigger state transitions. Also updates Linear issue states and posts comments in response to Forge bus events.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `LINEAR_API_KEY` | Yes | Linear API key (`lin_api_...`) |
| `LINEAR_WEBHOOK_SECRET` | No | Secret for HMAC-SHA256 signature validation (`Linear-Signature`) |

## Webhook

**Path:** `/webhook`
**Security:** HMAC-SHA256 — set `LINEAR_WEBHOOK_SECRET`; adapter checks the `Linear-Signature` header.

Register at: **Linear → Settings → API → Webhooks → New webhook** — subscribe to Issue events. Set URL to `https://<host>:19097/webhook`.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive Linear issue events |
| `GET` | `/api/v1/issues` | Fetch an issue by ID (`?id=`) or list issues (`?team=`) |
| `POST` | `/api/v1/issues` | Create a new Linear issue |
| `POST` | `/api/v1/transitions` | Update an issue's workflow state (`?id=`) |

## Events Published

| Event | Trigger |
|---|---|
| `task.created` | Issue created |
| `task.completed` | Issue state updated to a completed state |
| `task.failed` | Issue removed/deleted |

## Events Subscribed

| Event | Action |
|---|---|
| `task.blocked` | Updates the Linear issue state via GraphQL mutation (issue ID from `linear_issue_id` payload field) |
| `escalation.created` | Posts a comment on the associated Linear issue with escalation details |
