# Backstage Adapter

Port: **:19129**

Bridges Backstage's Software Catalog and Scaffolder with the Forge event bus. Receives scaffolder task completion events via webhook and exposes a REST bridge for the Architect agent to query catalog entities and components.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `BACKSTAGE_URL` | Yes | Base URL of your Backstage instance (e.g. `https://backstage.example.com`) |
| `BACKSTAGE_TOKEN` | No | Static bearer token configured in Backstage's `auth.keys` |

## Webhook

**Path:** `/webhook/scaffolder`
**Security:** Bearer token — `BACKSTAGE_TOKEN` sent as `Authorization: Bearer <token>`.

Register at: **Backstage `app-config.yaml`** → `scaffolder.webhooks` block, pointing to `https://<host>:19129/webhook/scaffolder`.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook/scaffolder` | Receive Backstage scaffolder task events |
| `GET` | `/api/v1/entities` | List catalog entities (optionally filtered by `?kind=`) |
| `GET` | `/api/v1/components` | Fetch a component by name (`?name=&namespace=`) or list all components |

## Events Published

| Event | Trigger |
|---|---|
| `task.completed` | Scaffolder task status `completed` |
| `task.failed` | Scaffolder task status `failed` |

## Events Subscribed

| Event | Action |
|---|---|
| `task.created` | Logs when a new task references a Backstage component via `backstage_component_ref` payload field |
