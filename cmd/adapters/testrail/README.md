# TestRail / Xray Adapter

Port: **:19130**

Receives test run completion results from TestRail and Xray (Jira), publishing pass/fail outcomes to the Forge event bus. Also exposes a REST bridge for the QA agent to list runs and fetch test results.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `TESTRAIL_URL` | Yes | TestRail base URL (e.g. `https://myorg.testrail.io`) |
| `TESTRAIL_USER` | Yes | TestRail user email address |
| `TESTRAIL_API_KEY` | Yes | TestRail API key |
| `XRAY_CLIENT_ID` | No | Xray app client ID (enables Xray OAuth2 and REST bridge) |
| `XRAY_CLIENT_SECRET` | No | Xray app client secret |

## Webhook

**Path:** `/webhook`
**Security:** Basic auth using `TESTRAIL_USER` / `TESTRAIL_API_KEY`.

Register at:
- **TestRail:** Administration → Site Settings → Webhooks → Add webhook → URL: `https://<host>:19130/webhook`.
- **Xray:** Project Settings → Webhooks → Add webhook → URL: `https://<host>:19130/webhook`.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive TestRail or Xray test run events |
| `GET` | `/api/v1/runs` | List test runs (`?project_id=`) |
| `GET` | `/api/v1/results` | List results for a test run (`?run_id=`) |

## Events Published

| Event | Trigger |
|---|---|
| `task.completed` | Test run completed with all tests passing |
| `task.blocked` | Test run completed with one or more test failures |

## Events Subscribed

| Event | Action |
|---|---|
| `review.approved` | Creates a new TestRail test run for the approved change |
