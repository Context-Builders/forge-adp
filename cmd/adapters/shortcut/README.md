# Shortcut Adapter

Port: **:19125**

Bridges Shortcut (formerly Clubhouse) story lifecycle events with the Forge event bus and exposes a REST bridge for the PM agent to create stories and trigger workflow state transitions.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `SHORTCUT_API_TOKEN` | Yes | Shortcut API token |
| `SHORTCUT_WEBHOOK_SECRET` | No | Secret for HMAC-SHA256 signature validation (`Shortcut-Signature`) |

## Webhook

**Path:** `/webhook`
**Security:** HMAC-SHA256 — set `SHORTCUT_WEBHOOK_SECRET`; adapter checks the `Shortcut-Signature` header.

Register at: **Shortcut → Settings → Integrations → Webhooks → Create webhook** — set URL to `https://<host>:19125/webhook`.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive Shortcut story events |
| `GET` | `/api/v1/stories` | Fetch a story by ID (`?id=`) |
| `POST` | `/api/v1/stories` | Create a new story |
| `POST` | `/api/v1/transitions` | Update a story's workflow state (`?id=`) |

## Events Published

| Event | Trigger |
|---|---|
| `task.created` | Story `create` action |
| `task.completed` | Story `update` action with `completed_at` set |

## Events Subscribed

| Event | Action |
|---|---|
| `task.completed` | Updates the Shortcut story's workflow state via `PUT /stories/:id` (requires `shortcut_story_id` and `shortcut_workflow_state_id` in payload) |
| `task.failed` | Same as above |
| `task.blocked` | Same as above |
