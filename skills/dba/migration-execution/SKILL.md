# Migration Execution Skill

## Purpose

Execute pending database migrations against a target environment and verify the outcome:
- Validate pending migrations before applying
- Apply migrations in the correct order using the project's migration tool
- Verify the target schema version matches expectations
- Run post-migration smoke queries to confirm data integrity
- Roll back automatically on failure when supported by the tool

## Prerequisites

1. Migration files have been generated and merged (see `dba/schema-migration`)
2. Database connection credentials for the target environment are available
3. Target environment (dev, staging, production) is specified
4. The migration tool is installed or available in the deployment environment

## Execution Steps

1. **Load Context**
   - Identify migration tool from `migration_tool` input or infer from project files
   - Resolve database connection string for the target environment from Vault or environment config
   - Fetch current migration version from the database

2. **Validate Pending Migrations**
   - List all pending migrations using the tool's status command
   - Confirm each pending migration file is present and has a valid checksum
   - If `dry_run` is true, report pending migrations and stop without applying

3. **Pre-flight Checks**
   - Confirm no migrations are in a failed/broken state before proceeding
   - For production: require a DBA lead acknowledgement on the associated PR
   - Estimate migration duration for large-table operations; escalate if > 5 minutes

4. **Execute Migrations**
   - Run migrations using the tool's migrate/update command
   - Capture per-migration execution time and SQL output
   - For `production`: run with reduced parallelism and confirm each step succeeds before continuing

5. **Verify Post-Conditions**
   - Query `information_schema` (or ORM layer) to confirm expected tables/columns exist
   - Run basic smoke queries defined in the ticket or inferred from the migration content
   - Record the new schema version

6. **Report Results**
   - Post a structured summary to the Jira ticket (if provided): migrations applied, duration, new version
   - If launched from a PR context, post a comment to the PR with the execution log

## Rollback Strategy

| Tool | Rollback command |
|------|-----------------|
| goose | `goose down` (runs down script) |
| flyway | `flyway undo` (requires Teams/Enterprise) |
| liquibase | `liquibase rollback --tag=<previous_tag>` |
| alembic | `alembic downgrade -1` |
| dbmate | `dbmate rollback` |
| migrate | `migrate down 1` |

If automatic rollback fails, escalate immediately with the error log and current migration state.

## Plan References

| Document | Sections to Consult |
|----------|---------------------|
| DATA_MODEL.md | Expected post-migration schema state |
| ARCHITECTURE.md | Migration tool, connection string conventions, environment config |
| CONTRIBUTING.md | Deployment runbook for database changes |

## Quality Gates

- [ ] Zero pending migrations remain after execution
- [ ] New schema version matches the highest migration number
- [ ] No ERROR entries in migration tool output
- [ ] Post-migration smoke queries pass
- [ ] Execution reported on Jira ticket or PR

## Escalation Triggers

- A migration has been in `failed` state before this run begins
- Any migration takes longer than the pre-flight estimate
- Post-migration smoke queries return unexpected results
- The database is unreachable or returns an authentication error
- Rollback fails or produces errors

## Anti-Patterns

- Do not execute production migrations without first running against staging
- Do not skip the dry-run check when running against production
- Do not hard-code connection strings — always resolve from Vault or environment secrets
- Do not run migrations in parallel across multiple replicas of this skill simultaneously
