# Schema Migration Skill

## Purpose

Design and generate production-safe database schema migrations:
- New table and column additions
- Column type changes and renames with zero-downtime strategies
- Index creation and removal
- Foreign key additions and removals
- Constraint additions (with backfill strategies)

## Prerequisites

1. Jira ticket with schema change requirements
2. Existing schema DDL for affected tables
3. Migration tool convention identified from project (`goose`, `flyway`, `alembic`, etc.)
4. DBA lead and backend lead available for sign-off

## Execution Steps

1. **Load Context**
   - Retrieve ticket and load DATA_MODEL.md
   - Fetch current table DDL from connected database
   - Identify migration tool and file naming convention

2. **Design Safe Migration**
   - Identify breaking vs. backward-compatible changes
   - For column renames/drops: plan multi-step expand/contract migration
   - For NOT NULL constraints: plan backfill migration before constraint addition
   - For large table changes: identify whether `LOCK` is required and estimate duration

3. **Generate Migration Files**
   - Create numbered up/down migration files per tool convention
   - Include safety checks (e.g. `IF NOT EXISTS`, `IF EXISTS`)
   - Use `CONCURRENTLY` for index operations
   - Add comments documenting the intent and rollback strategy

4. **Generate Model Updates**
   - Update ORM model definitions or struct tags to reflect schema changes
   - Flag any application-level changes required (column removals, renames)

5. **Create PR**
   - Branch: `forge/dba/{ticket-id}-schema-migration`
   - Requires DBA lead + backend lead sign-off

## Plan References

| Document | Sections to Consult |
|----------|---------------------|
| DATA_MODEL.md | Current schema, existing indexes, naming conventions |
| ARCHITECTURE.md | ORM being used, migration tool, deployment strategy |

## Quality Gates

- [ ] Both `up` and `down` scripts are present and valid SQL
- [ ] `down` script fully reverses the `up` script
- [ ] No breaking changes to columns still read by deployed application code
- [ ] Index operations use `CONCURRENTLY`
- [ ] DBA lead and backend lead have approved the PR

## Escalation Triggers

- Migration requires a table lock and table is too large for acceptable downtime
- Schema change is breaking and requires coordinated application deployment
- Data backfill is required and row count exceeds 10M rows
- Schema change conflicts with a migration currently in flight

## Anti-Patterns

- Do not drop columns or tables in the same migration that removes application references
- Do not add NOT NULL constraints without a default or prior backfill
- Do not use `ALTER TABLE ... RENAME COLUMN` on tables used by running application code
- Do not write irreversible `down` scripts that lose data

## Examples

### Input

```json
{
  "jira_ticket": "DB-060",
  "schema_change_spec": "Add 'currency' VARCHAR(3) NOT NULL DEFAULT 'USD' to payments table. Add index on (customer_id, currency).",
  "migration_tool": "goose"
}
```

### Output

```json
{
  "branch": "forge/dba/DB-060-schema-migration",
  "pr_number": 510,
  "files_created": [
    "internal/db/migrations/000002_add_currency_to_payments.up.sql",
    "internal/db/migrations/000002_add_currency_to_payments.down.sql"
  ]
}
```
