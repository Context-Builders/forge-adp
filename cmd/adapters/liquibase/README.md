# Liquibase Adapter

Port: **:19138**

Integrates with Liquibase Hub/Pro to run database migrations as part of the Forge deployment pipeline. Triggered by `deployment.requested` events from the bus — there is no inbound webhook from Liquibase.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `REDIS_ADDR` | Yes | Redis address for the event bus (e.g. `localhost:6379`) |
| `LIQUIBASE_URL` | Yes | Base URL of your Liquibase Hub API server |
| `LIQUIBASE_USERNAME` | Yes | Liquibase API username |
| `LIQUIBASE_PASSWORD` | Yes | Liquibase API password |

## Webhook

This adapter has no inbound webhook. It is entirely event-driven: it subscribes to `deployment.requested` on the Forge bus and calls the Liquibase API to run migrations.

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Health check |
| `POST` | `/api/v1/update` | Trigger a Liquibase update (apply pending changesets) |
| `GET` | `/api/v1/status` | Get pending changeset status |

## Events Published

_None — this adapter does not publish events directly._

## Events Subscribed

| Event | Action |
|---|---|
| `deployment.requested` | Triggers `POST /liquibase/update` when `liquibase_tag` or `liquibase_changelog_file` is present in the payload |
