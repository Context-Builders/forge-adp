# HashiCorp Vault Adapter

Port: **:19121**

Bridges HashiCorp Vault audit log events with the Forge event bus and exposes a REST bridge for the SecOps agent to read secrets and manage leases.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `VAULT_URL` | Yes | Vault server URL (e.g. `https://vault.example.com`) |
| `VAULT_TOKEN` | Yes | Vault token with appropriate policy (`X-Vault-Token` header) |
| `VAULT_WEBHOOK_SECRET` | No | Optional secret for additional inbound webhook validation |

## Webhook

**Path:** `/webhook/audit`
**Security:** Vault token (`X-Vault-Token: <VAULT_TOKEN>`).

Vault does not push webhooks natively. Options for receiving audit log events:
1. **File audit device + log forwarder:** Enable `vault audit enable file file_path=/var/log/vault/audit.log`, then use a log shipper (Fluentd, Vector) to POST entries to `https://<host>:19121/webhook/audit`.
2. **Syslog audit device:** Enable `vault audit enable syslog` and forward syslog to the adapter.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook/audit` | Receive Vault audit log entries |
| `GET` | `/api/v1/secrets` | List secrets at a path (`?path=`) |
| `POST` | `/api/v1/leases` | Renew a lease by lease ID |

## Events Published

| Event | Trigger |
|---|---|
| `escalation.created` | Audit log entry with error or permission-denied status |

## Events Subscribed

| Event | Action |
|---|---|
| `escalation.created` | Logs the escalation (loop-safe: skips events where `source == "vault"`) |
