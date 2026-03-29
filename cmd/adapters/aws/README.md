# AWS Adapter

Port: **:19118**

Receives Amazon SNS notifications (CloudWatch alarms, deployment events) and publishes them to the Forge event bus. Automatically confirms SNS subscription requests on startup. SNS messages are verified using Amazon's X.509 certificate signing — no shared secret is required.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `AWS_REGION` | Yes | AWS region (e.g. `us-east-1`) |
| `AWS_ACCESS_KEY_ID` | Yes | AWS access key ID |
| `AWS_SECRET_ACCESS_KEY` | Yes | AWS secret access key |
| `AWS_SNS_WEBHOOK_SECRET` | No | Optional additional HMAC secret for custom webhook validation |
| `AWS_DEPLOY_TOPIC_ARN` | No | SNS topic ARN for deployment events |

## Webhook

**Path:** `/webhook`
**Security:** Amazon SNS X.509 message signing — no shared secret needed. SNS subscription confirmation requests are auto-confirmed by the adapter.

Register at: **AWS → SNS → Subscriptions → Create subscription** → Protocol: HTTPS, Endpoint: `https://<host>:19118/webhook`. Also configure **CloudWatch → Alarms → Actions → Send notification** to the same SNS topic.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive SNS notifications and subscription confirmations |

## Events Published

| Event | Trigger |
|---|---|
| `escalation.created` | CloudWatch alarm state `ALARM` |
| `task.completed` | CloudWatch alarm state `OK` |

## Events Subscribed

_None — this adapter is inbound only._
