# Grafana Adapter

Port: **:19101**

Receives Grafana alert webhook notifications and publishes firing/resolved events to the Forge event bus. Also exposes a REST bridge for the SRE agent to post annotations and manage silences.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `GRAFANA_URL` | Yes | Grafana base URL (e.g. `https://grafana.example.com`) |
| `GRAFANA_API_KEY` | Yes | Grafana service account API key with `Editor` role |

## Webhook

**Path:** `/webhook`
**Security:** Grafana signs requests with `X-Grafana-Signature` using the webhook URL as the key. Configure a shared secret in the Grafana contact point settings.

Register at: **Grafana → Alerting → Contact points → New contact point** → Type: Webhook → URL: `https://<host>:19101/webhook`. Add the contact point to an alert rule's notification policy.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive Grafana alert events |
| `POST` | `/api/v1/annotations` | Create a Grafana annotation |
| `POST` | `/api/v1/silences` | Create an alert silence |

## Events Published

| Event | Trigger |
|---|---|
| `escalation.created` | Alert state `firing` |
| `task.completed` | Alert state `resolved` |

## Events Subscribed

| Event | Action |
|---|---|
| `escalation.created` | Posts a Grafana annotation to mark the escalation on dashboards |
