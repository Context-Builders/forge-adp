# Flyway Adapter

Port: **:19137**

Integrates with Flyway Teams/Enterprise to run database migrations as part of the Forge deployment pipeline. Triggered by `deployment.requested` events from the bus — there is no inbound webhook from Flyway.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `FLYWAY_URL` | Yes | Base URL of your Flyway Teams API server (e.g. `https://flyway.example.com`) |
| `FLYWAY_USERNAME` | Yes | Flyway API username |
| `FLYWAY_PASSWORD` | Yes | Flyway API password |

## Webhook

This adapter has no inbound webhook. It is entirely event-driven: it subscribes to `deployment.requested` on the Forge bus and calls the Flyway API to run migrations.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/api/v1/migrate` | Trigger a Flyway migration directly |
| `GET` | `/api/v1/info` | Get migration status for a schema |

## Events Published

_None — this adapter does not publish events directly. Migration outcomes are reported via the Flyway API response._

## Events Subscribed

| Event | Action |
|---|---|
| `deployment.requested` | Triggers `POST /flyway/migrate` when `flyway_target` or `flyway_schema` is present in the payload |
