# Wiz / Prisma Cloud Adapter

Port: **:19122**

Receives cloud security findings from Wiz and Prisma Cloud via webhook, publishing critical and high severity findings to the Forge event bus as escalations. Exposes separate webhook paths for each platform.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `WIZ_CLIENT_ID` | Yes | Wiz OAuth2 client ID |
| `WIZ_CLIENT_SECRET` | Yes | Wiz OAuth2 client secret |
| `PRISMA_ACCESS_KEY` | No | Prisma Cloud access key ID (enables Prisma Cloud webhook path) |
| `PRISMA_SECRET_KEY` | No | Prisma Cloud secret key |

## Webhook

**Paths:**
- `/webhook/wiz` — Wiz findings
- `/webhook/prisma` — Prisma Cloud alert payloads

**Security:** OAuth2 client credentials (`WIZ_CLIENT_ID` / `WIZ_CLIENT_SECRET` for Wiz; `PRISMA_ACCESS_KEY` / `PRISMA_SECRET_KEY` for Prisma Cloud).

Register at:
- **Wiz:** Settings → Automation → Actions → Add Action → Webhook → URL: `https://<host>:19122/webhook/wiz`
- **Prisma Cloud:** Settings → Integrations → Add Integration → Webhook → URL: `https://<host>:19122/webhook/prisma`

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook/wiz` | Receive Wiz finding events |
| `POST` | `/webhook/prisma` | Receive Prisma Cloud alert events |
| `GET` | `/api/v1/findings` | Returns links to the Wiz GraphQL and Prisma REST APIs for direct querying |

## Events Published

| Event | Trigger |
|---|---|
| `escalation.created` | Wiz finding `OPEN` with severity `CRITICAL` or `HIGH` |
| `task.completed` | Wiz finding `RESOLVED` |
| `escalation.created` | Prisma Cloud alert `open` with severity `critical` or `high` |

## Events Subscribed

| Event | Action |
|---|---|
| `escalation.created` | Logs the escalation (loop-safe: skips events where `source == "wiz"` or `source == "prismacloud"`) |
