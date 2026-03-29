# Confluence Adapter

Port: **:19096**

Bridges Confluence page lifecycle events with the Forge event bus and exposes a REST bridge for the PM and Architect agents to create and update pages and query spaces.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `CONFLUENCE_BASE_URL` | Yes | Confluence base URL (e.g. `https://myorg.atlassian.net`) |
| `CONFLUENCE_USERNAME` | Yes | Atlassian account email address |
| `CONFLUENCE_API_TOKEN` | Yes | Atlassian API token |

## Webhook

**Path:** `/webhook`
**Security:** Bearer token (Atlassian API token).

Register at: **Confluence → Space Tools → Integrations → Webhooks** (Confluence Data Center/Server), or use Atlassian's Automation rules to POST to `https://<host>:19096/webhook` on page-created/updated events for Cloud.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive Confluence page events |
| `GET` | `/api/v1/pages` | Fetch a page by ID (`?id=`) |
| `POST` | `/api/v1/pages` | Create a new page |
| `PUT` | `/api/v1/pages` | Update a page (`?id=`) |
| `GET` | `/api/v1/spaces` | List Confluence spaces |

## Events Published

| Event | Trigger |
|---|---|
| `task.created` | Page created in a space that carries the `forge` label |

## Events Subscribed

_None — this adapter is inbound only._
