# Notion Adapter

Port: **:19126**

Bridges Notion page and database events with the Forge event bus and exposes a REST bridge for the PM agent to create and update pages and query databases.

> **Note:** Notion does not support native outbound webhooks as of 2024. To receive page events, use a middleware service such as Zapier, Make (Integromat), or a scheduled poll via the adapter's `/api/v1/pages` endpoint. The adapter's inbound webhook endpoint is available for use with such intermediaries.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `NOTION_TOKEN` | Yes | Notion internal integration token (`secret_...`) |
| `NOTION_DATABASE_ID` | No | Default Notion database ID for task tracking |

## Webhook

**Path:** `/webhook`
**Security:** Bearer token (`Authorization: Bearer <NOTION_TOKEN>`).

No native Notion webhook — route events via Zapier/Make or a polling script that POSTs to `https://<host>:19126/webhook`.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive forwarded Notion page events |
| `GET` | `/api/v1/pages` | Fetch a Notion page by ID (`?id=`) |
| `POST` | `/api/v1/pages` | Create a new Notion page |
| `GET` | `/api/v1/databases` | Query a Notion database (`?id=`) |

## Events Published

| Event | Trigger |
|---|---|
| `task.created` | Page created event received via webhook |
| `task.completed` | Page updated with a `Done` status property |

## Events Subscribed

| Event | Action |
|---|---|
| `task.completed` | Updates the associated Notion page's status property to `Done` (requires `notion_page_id` in payload) |
| `task.failed` | Updates the associated Notion page's status property to `Failed` |
