# Split.io Adapter

Port: **:19128**

Bridges Split.io feature flag events with the Forge event bus and exposes a REST bridge for the DevOps and Backend Developer agents to list splits and toggle treatments. Automatically updates split treatments as part of the deployment approval workflow.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `SPLITIO_ADMIN_TOKEN` | Yes | Split.io Admin API token |
| `SPLITIO_WORKSPACE_ID` | Yes | Split.io workspace ID |
| `SPLITIO_ENVIRONMENT` | No | Default environment name (defaults to `production`) |

## Webhook

**Path:** `/webhook`
**Security:** Bearer token (`Authorization: Bearer <SPLITIO_ADMIN_TOKEN>`).

Split.io does not have native outbound webhooks for flag changes. The adapter's webhook endpoint can receive forwarded events from a Split.io integration or custom pipeline step.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive Split.io flag change events |
| `GET` | `/api/v1/splits` | List splits in the workspace |
| `POST` | `/api/v1/toggles` | Update a split's treatment in an environment |

## Events Published

| Event | Trigger |
|---|---|
| `escalation.created` | `SPLIT_KILLED` event received |
| `task.completed` | `SPLIT_UPDATED` event received |

## Events Subscribed

| Event | Action |
|---|---|
| `deployment.approved` | Updates the split treatment to `on` for the environment via `PATCH /splits/ws/{workspace}/{split}/environments/{env}` (requires `splitio_split_name` in payload) |
