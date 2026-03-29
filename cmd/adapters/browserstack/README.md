# BrowserStack / Sauce Labs Adapter

Port: **:19131**

Receives cross-browser and mobile test build results from BrowserStack and Sauce Labs, publishing pass/fail outcomes to the Forge event bus. Also exposes a REST bridge for the QA agent to list builds and sessions.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `BROWSERSTACK_USER` | Yes | BrowserStack username |
| `BROWSERSTACK_ACCESS_KEY` | Yes | BrowserStack access key |
| `SAUCE_USERNAME` | No | Sauce Labs username (enables Sauce Labs REST bridge) |
| `SAUCE_ACCESS_KEY` | No | Sauce Labs access key |

## Webhook

**Path:** `/webhook`
**Security:** Basic auth using `BROWSERSTACK_USER` / `BROWSERSTACK_ACCESS_KEY`.

- **BrowserStack:** Register at **BrowserStack App Automate → Settings → Webhooks** → set URL to `https://<host>:19131/webhook`.
- **Sauce Labs:** Post build results from your CI pipeline to `https://<host>:19131/webhook` after test runs complete.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive BrowserStack or Sauce Labs build result events |
| `GET` | `/api/v1/builds` | List recent builds |
| `GET` | `/api/v1/sessions` | List sessions for a build (`?build_id=`) |

## Events Published

| Event | Trigger |
|---|---|
| `task.completed` | Build completed with all tests passing |
| `task.blocked` | Build completed with one or more test failures |

## Events Subscribed

_None — this adapter is inbound only._
