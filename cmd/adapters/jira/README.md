# Jira Adapter

Port: **:19090**

The primary work item source for Forge. Bridges Jira issue lifecycle events with the Forge event bus and exposes a REST bridge for the PM agent to create and transition tickets.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `JIRA_BASE_URL` | Yes | Atlassian base URL (e.g. `https://myorg.atlassian.net`) |
| `JIRA_USER_EMAIL` | Yes | Atlassian account email for API auth |
| `JIRA_API_TOKEN` | Yes | Atlassian API token |

## Webhook

**Path:** `/webhook`
**Security:** No signature verification — restrict access by IP allowlist in Jira or use a VPN/tunnel.

Register at: **Jira → Project Settings → Webhooks → Create a webhook** — subscribe to Issue Created and Issue Updated events. Set URL to `https://<host>:19090/webhook`.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive Jira issue events |
| `GET` | `/api/v1/tickets` | Fetch a ticket by key (`?key=`) |
| `POST` | `/api/v1/tickets` | Create a new Jira issue |
| `POST` | `/api/v1/transitions` | Transition an issue to a new status (`?key=`) |

## Events Published

| Event | Trigger |
|---|---|
| `task.created` | `jira:issue_created` |
| `task.completed` | `jira:issue_updated` with status `Done` |
| `task.blocked` | `jira:issue_updated` with status `Blocked` |

## Events Subscribed

| Event | Action |
|---|---|
| `task.created` | Creates a Jira issue if `jira_project` is present in the payload |
