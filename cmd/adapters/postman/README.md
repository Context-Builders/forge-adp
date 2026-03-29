# Postman / Newman Adapter

Port: **:19133**

Receives Postman monitor and Newman collection run results, publishing pass/fail outcomes to the Forge event bus. Also exposes a REST bridge for the QA agent to list collections, monitors, and trigger runs.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `POSTMAN_API_KEY` | Yes | Postman API key |

## Webhook

**Path:** `/webhook`
**Security:** API key header (`X-Api-Key: <POSTMAN_API_KEY>`).

Register at:
- **Postman Monitor:** Team Settings → Webhooks → configure a monitor webhook to POST to `https://<host>:19133/webhook`.
- **Newman (CI):** After `newman run`, POST the JSON report to `https://<host>:19133/webhook` from your pipeline.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive Postman monitor or Newman run results |
| `GET` | `/api/v1/collections` | List Postman collections |
| `GET` | `/api/v1/monitors` | List Postman monitors |
| `POST` | `/api/v1/runs` | Trigger a monitor run |

## Events Published

| Event | Trigger |
|---|---|
| `task.completed` | Monitor run `passed` with zero failures |
| `escalation.created` | Monitor run `failed` |
| `task.blocked` | Newman report contains assertion failures |

## Events Subscribed

_None — this adapter is inbound only._
