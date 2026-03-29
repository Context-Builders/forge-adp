# Checkov / Trivy Adapter

Port: **:19123**

Receives IaC security scan results from Checkov (via Bridgecrew platform webhook) and container image scan results from Trivy (SARIF format), publishing security findings to the Forge event bus. Also exposes a REST bridge to query violations and suppressions from the Bridgecrew platform.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `BRIDGECREW_API_TOKEN` | No | Bridgecrew platform API token (enables `/api/v1/violations` and `/api/v1/suppressed` endpoints) |
| `CHECKOV_WEBHOOK_SECRET` | No | Secret for validating inbound Checkov webhook signatures |

## Webhook

**Paths:**
- `/webhook/checkov` — Checkov / Bridgecrew scan results (JSON)
- `/webhook/trivy` — Trivy scan results (SARIF format)

**Security:** Bridgecrew API token header (`Authorization: <BRIDGECREW_API_TOKEN>`).

Register at:
- **Checkov/Bridgecrew:** Bridgecrew Platform → Integrations → Webhooks → add `https://<host>:19123/webhook/checkov`.
- **Trivy:** Post SARIF output from CI to `https://<host>:19123/webhook/trivy` after each `trivy image` or `trivy fs` run.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook/checkov` | Receive Checkov scan results |
| `POST` | `/webhook/trivy` | Receive Trivy SARIF scan results |
| `GET` | `/api/v1/violations` | List resource violations from Bridgecrew |
| `GET` | `/api/v1/suppressed` | List suppressed violations from Bridgecrew |

## Events Published

| Event | Trigger |
|---|---|
| `escalation.created` | Checkov scan has CRITICAL or HIGH severity violations |
| `task.blocked` | Checkov scan has failures but none are CRITICAL/HIGH |
| `task.completed` | Checkov scan passes with zero failures |
| `escalation.created` | Trivy SARIF scan contains `error`-level findings |
| `task.completed` | Trivy scan is clean |

## Events Subscribed

| Event | Action |
|---|---|
| `deployment.requested` | Logs that a Checkov pre-deployment scan should be triggered for the specified repo/branch |
