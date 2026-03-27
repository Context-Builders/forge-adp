# Query Optimization Skill

## Purpose

Analyze slow database queries and produce actionable optimizations:
- Explain plan analysis to identify sequential scans and join performance
- Index recommendations with partial and composite index strategies
- Query rewriting to use more efficient patterns
- Missing index migration file generation
- Runbook for applying changes safely in production

## Prerequisites

1. Jira ticket documenting the performance problem and impact
2. Slow query log entries or explicit slow queries to optimize
3. Database schema DDL for affected tables
4. EXPLAIN ANALYZE output preferred but not required

## Execution Steps

1. **Load Context**
   - Retrieve ticket and load DATA_MODEL.md via `plan-reader`
   - Fetch table DDL from database if not provided
   - Run EXPLAIN ANALYZE if query plans are missing

2. **Analyze Query Plans**
   - Identify sequential scans on large tables
   - Identify expensive sort and hash operations
   - Identify N+1 patterns in ORM-generated queries
   - Calculate estimated row counts and cost differentials

3. **Generate Optimizations**
   - Propose index additions (single-column, composite, partial, expression)
   - Rewrite queries to use index-friendly patterns
   - Suggest denormalization where join costs are prohibitive
   - Flag any queries requiring application-level changes

4. **Generate Migration**
   - Create `CONCURRENTLY` index creation statements to avoid table locks
   - Generate up/down migration file following project versioning convention
   - Include EXPLAIN ANALYZE targets in migration comments

5. **Create PR**
   - Branch: `forge/dba/{ticket-id}-query-optimization`
   - Requires DBA lead + backend lead sign-off before merge

## Plan References

| Document | Sections to Consult |
|----------|---------------------|
| DATA_MODEL.md | Existing indexes, cardinality notes, partition strategy |
| ARCHITECTURE.md | ORM patterns, query generation approach |

## Quality Gates

- [ ] EXPLAIN ANALYZE shows reduced cost for all optimized queries
- [ ] All new indexes use `CREATE INDEX CONCURRENTLY` (non-locking)
- [ ] Migration has a valid `DOWN` script to drop added indexes
- [ ] No query rewrites break existing application behavior
- [ ] DBA lead has approved index strategy

## Escalation Triggers

- Query cannot be optimized without schema changes requiring downtime
- Optimization requires disabling or restructuring business logic in application code
- Table size requires partitioning before indexing is effective
- Slow query is caused by lock contention, not missing indexes

## Anti-Patterns

- Do not drop existing indexes without confirming no other queries depend on them
- Do not create indexes on low-cardinality columns (e.g. boolean flags)
- Do not rewrite queries in a way that changes their semantic results
- Do not use `CREATE INDEX` without `CONCURRENTLY` on production tables

## Examples

### Input

```json
{
  "jira_ticket": "DB-055",
  "slow_queries": [
    "SELECT * FROM payments WHERE customer_id = $1 AND status = 'CAPTURED' ORDER BY created_at DESC LIMIT 20"
  ],
  "schema": "CREATE TABLE payments (id UUID, customer_id UUID, status VARCHAR(20), amount DECIMAL, created_at TIMESTAMPTZ);"
}
```

### Output

```json
{
  "optimized_queries": [
    "SELECT id, amount, status, created_at FROM payments WHERE customer_id = $1 AND status = 'CAPTURED' ORDER BY created_at DESC LIMIT 20"
  ],
  "index_recommendations": [
    "CREATE INDEX CONCURRENTLY idx_payments_customer_status_created ON payments(customer_id, status, created_at DESC);"
  ],
  "branch": "forge/dba/DB-055-query-optimization",
  "pr_number": 501,
  "estimated_speedup": "~40x on p99"
}
```
