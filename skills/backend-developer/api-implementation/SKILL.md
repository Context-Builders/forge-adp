# API Implementation Skill

## Purpose

Transform an OpenAPI specification into a working API endpoint implementation, including:
- Route handler
- Request validation
- Business logic integration
- Error handling
- Unit and integration tests

## Prerequisites

1. Jira ticket with clear acceptance criteria
2. API contract (OpenAPI spec or equivalent) available in plans or ticket
3. Data model defined (if persisting data)
4. Backend architecture document accessible

## Execution Steps

1. **Parse Inputs**
   - Load Jira ticket details and acceptance criteria
   - Retrieve API contract specification
   - Load relevant architecture documentation

2. **Analyze Requirements**
   - Identify HTTP method, path, parameters
   - Determine request/response schemas
   - Identify required integrations (database, external services)

3. **Generate Implementation**
   - Create route handler following project conventions
   - Implement input validation
   - Add business logic or delegate to service layer
   - Implement error handling per API_CONTRACTS.md patterns

4. **Generate Tests**
   - Unit tests for handler logic
   - Integration tests for full request cycle
   - Edge case and error scenario coverage

5. **Create PR**
   - Branch from main with naming: `forge/backend-developer/{ticket-id}-{endpoint-name}`
   - Commit with message referencing ticket
   - Open PR with description linking to ticket

## Plan References

| Document | Sections to Consult |
|----------|---------------------|
| ARCHITECTURE.md | API design patterns, service boundaries, error handling |
| API_CONTRACTS.md | Response schemas, error codes, versioning strategy |
| DATA_MODEL.md | Entity relationships relevant to this endpoint |
| CONTRIBUTING.md | Code style, testing requirements, PR template |

## Quality Gates

- [ ] All existing tests pass
- [ ] New tests cover happy path and error cases
- [ ] Code follows linting rules
- [ ] Type checks pass
- [ ] No security vulnerabilities in added dependencies
- [ ] API documentation updated

## Escalation Triggers

- Acceptance criteria are ambiguous or contradictory
- Required data model changes not yet implemented
- API contract conflicts with existing implementation
- Security concerns identified (authentication, authorization gaps)

## Anti-Patterns

- Do not hardcode configuration values
- Do not skip input validation even for "internal" endpoints
- Do not catch and swallow errors without logging
- Do not create database queries directly in handlers (use repository pattern)

## Examples

### Input

```json
{
  "jira_ticket_id": "PAY-1234",
  "api_contract": {
    "path": "/api/v1/payments/{id}",
    "method": "GET",
    "response": {
      "type": "object",
      "properties": {
        "id": {"type": "string"},
        "amount": {"type": "number"},
        "status": {"type": "string"}
      }
    }
  }
}
```

### Output

```
Created branch: forge/backend-developer/PAY-1234-get-payment
Created files:
  - handlers/payments.go
  - handlers/payments_test.go
Created PR: #347
```
