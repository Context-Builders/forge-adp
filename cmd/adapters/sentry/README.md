# Sentry Adapter

Port: **:19115**

Bridges Sentry error and issue events with the Forge event bus and exposes a REST bridge for the SRE agent to query issues and post comments.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `SENTRY_AUTH_TOKEN` | Yes | Sentry internal integration auth token |
| `SENTRY_ORG_SLUG` | Yes | Sentry organisation slug |
| `SENTRY_WEBHOOK_SECRET` | No | Secret for HMAC-SHA256 signature validation (`Sentry-Hook-Signature`) |

## Webhook

**Path:** `/webhook`
**Security:** HMAC-SHA256 — set `SENTRY_WEBHOOK_SECRET`; adapter checks the `Sentry-Hook-Signature` header.

Register at: **Sentry → Settings → Developer Settings → Internal Integrations → New Internal Integration → Webhooks** — subscribe to Issue events. Set URL to `https://<host>:19115/webhook`.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive Sentry issue events |
| `GET` | `/api/v1/issues` | List unresolved issues for a project (`?project=`) |
| `POST` | `/api/v1/comments` | Post a comment on an issue (`?issue_id=`) |

## Events Published

| Event | Trigger |
|---|---|
| `escalation.created` | Issue event `created` with level `error` or `fatal` |
| `task.completed` | Issue event `resolved` |

## Events Subscribed

_None — this adapter is inbound only._
