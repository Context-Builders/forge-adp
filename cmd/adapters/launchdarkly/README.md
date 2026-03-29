# LaunchDarkly Adapter

Port: **:19127**

Bridges LaunchDarkly feature flag change events with the Forge event bus and exposes a REST bridge for the DevOps and Backend Developer agents to list flags and environments. Automatically enables flags as part of the deployment approval workflow.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `LAUNCHDARKLY_API_KEY` | Yes | LaunchDarkly REST API key |
| `LAUNCHDARKLY_PROJECT_KEY` | No | Default project key (defaults to `default`) |
| `LAUNCHDARKLY_ENVIRONMENT` | No | Default environment key (defaults to `production`) |

## Webhook

**Path:** `/webhook`
**Security:** API key header (`Authorization: <LAUNCHDARKLY_API_KEY>`).

Register at: **LaunchDarkly → Account Settings → Integrations → Webhooks → Add webhook** → set URL to `https://<host>:19127/webhook` and select flag change events.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/webhook` | Receive LaunchDarkly flag change events |
| `GET` | `/api/v1/flags` | List feature flags (`?project_key=`) |
| `GET` | `/api/v1/environments` | List environments for a project (`?project_key=`) |

## Events Published

| Event | Trigger |
|---|---|
| `task.completed` | Flag targeting rule updated |

## Events Subscribed

| Event | Action |
|---|---|
| `deployment.approved` | Enables the feature flag specified in `ld_flag_key` for the environment in `ld_environment` (PATCH flag targeting `on: true`) |
