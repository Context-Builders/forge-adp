# Business Logic Skill

## Purpose

Implement service-layer business logic including:
- Domain validation rules and constraint enforcement
- Entity state machines and lifecycle transitions
- Cross-aggregate business calculations
- Service orchestration between repositories and external adapters
- Domain events and side-effect handling

## Prerequisites

1. Jira ticket with detailed acceptance criteria and domain rules
2. Data model defined (DBA agent output or existing schema)
3. Architecture document describing service boundaries and patterns
4. Existing domain models accessible for reference

## Execution Steps

1. **Load Context**
   - Retrieve ticket via `jira-interaction`
   - Load `ARCHITECTURE.md` and `DATA_MODEL.md` via `plan-reader`
   - Read existing service and domain model files referenced in ticket

2. **Analyze Domain Requirements**
   - Identify entities, aggregates, and value objects involved
   - Map acceptance criteria to specific validation rules
   - Identify state transitions and their guards
   - Determine side effects (events, notifications, external calls)

3. **Generate Service Layer**
   - Create or update service class following project patterns
   - Implement validation methods with clear error messages
   - Implement state machine transitions with guard conditions
   - Add domain event emissions where required

4. **Generate Unit Tests**
   - Test each validation rule with valid and invalid inputs
   - Test all state transitions: happy path and rejection cases
   - Mock external dependencies (repositories, adapters)
   - Aim for 90%+ line coverage on new code

5. **Create PR**
   - Branch: `forge/backend-developer/{ticket-id}-{domain-name}-logic`
   - PR description includes: domain summary, rules implemented, test coverage

## Plan References

| Document | Sections to Consult |
|----------|---------------------|
| ARCHITECTURE.md | Service layer patterns, domain boundaries, dependency injection |
| DATA_MODEL.md | Entity definitions, relationships, field constraints |
| CONTRIBUTING.md | Code style, naming conventions, testing requirements |

## Quality Gates

- [ ] All acceptance criteria from ticket are covered by implementation
- [ ] All existing tests still pass
- [ ] New unit tests cover happy path and all error conditions
- [ ] No business logic in handlers or repositories — service layer only
- [ ] Type checks and linting pass

## Escalation Triggers

- Acceptance criteria contain contradictions or undefined terms
- Required domain model changes are not yet implemented
- Business rules require cross-service transactions (two-phase commit needed)
- Ambiguity in state machine transitions cannot be resolved from documentation

## Anti-Patterns

- Do not embed SQL or repository queries inside service methods
- Do not swallow validation exceptions — surface them with actionable messages
- Do not use primitive types for domain concepts (use value objects)
- Do not implement business rules in API handlers

## Examples

### Input

```json
{
  "jira_ticket": "PAY-1235",
  "domain_spec": "A payment can only be REFUNDED if its status is CAPTURED and the refund amount does not exceed the original payment amount.",
  "existing_models": ["src/domain/payment.go", "src/services/payment_service.go"]
}
```

### Output

```json
{
  "branch": "forge/backend-developer/PAY-1235-payment-refund-logic",
  "pr_number": 348,
  "files_created": [
    "src/services/payment_service.go",
    "src/services/payment_service_test.go"
  ],
  "rules_implemented": ["refund_amount_validation", "status_guard_captured"]
}
```
