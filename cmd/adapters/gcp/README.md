# GCP Adapter

Port: **:19120**

Receives Google Cloud Pub/Sub push messages for Cloud Build and Cloud Monitoring events, publishing build outcomes and alert notifications to the Forge event bus.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `GCP_PROJECT_ID` | Yes | Google Cloud project ID |
| `GCP_SERVICE_ACCOUNT_TOKEN` | Yes | Short-lived OAuth2 access token (run `gcloud auth print-access-token` to obtain) |
| `GCP_PUBSUB_AUDIENCE` | No | Expected audience for Pub/Sub push JWT verification |

## Webhook

**Path:** `/webhook/pubsub`
**Security:** Pub/Sub push JWT verification — GCP signs each push message with a service account JWT. Set `GCP_PUBSUB_AUDIENCE` to the expected audience claim.

Register at: **GCP → Pub/Sub → Subscriptions → Create subscription** → Delivery type: Push, Endpoint URL: `https://<host>:19120/webhook/pubsub`. Create subscriptions for your Cloud Build and Cloud Monitoring notification topics.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook/pubsub` | Receive Pub/Sub push notifications |

## Events Published

| Event | Trigger |
|---|---|
| `task.completed` | Cloud Build status `SUCCESS` |
| `task.failed` | Cloud Build status `FAILURE` or `TIMEOUT` |
| `escalation.created` | Cloud Monitoring alert policy opened (incident state `open`) |
| `task.completed` | Cloud Monitoring alert resolved (incident state `closed`) |

## Events Subscribed

_None — this adapter is inbound only._
