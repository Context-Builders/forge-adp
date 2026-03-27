# Service Integration Skill

## Purpose

Implement adapter code that connects the application to external or internal services:
- REST and gRPC client adapters
- SDK wrappers with retry and circuit-breaker logic
- Message producer and consumer implementations
- Authentication and credential management for external calls
- Response mapping between external schemas and internal domain models

## Prerequisites

1. Jira ticket with integration requirements and acceptance criteria
2. External service API documentation or SDK reference accessible
3. Architecture document describing adapter patterns
4. Secrets/credentials provisioned in the secrets manager

## Execution Steps

1. **Load Context**
   - Retrieve ticket and service spec via `plan-reader` / `jira-interaction`
   - Identify integration type from ticket or infer from spec

2. **Design Adapter Interface**
   - Define internal interface the adapter will implement
   - Map external API methods to interface operations
   - Plan request/response transformation shapes

3. **Generate Adapter Implementation**
   - Implement HTTP/gRPC/SDK client following project adapter pattern
   - Add authentication header injection or SDK credential setup
   - Implement retry logic with exponential backoff
   - Add circuit breaker using project-standard library
   - Map external error codes to internal domain errors

4. **Generate Tests**
   - Unit tests with mocked HTTP responses using recorded fixtures
   - Integration tests against a sandbox/staging endpoint (if available)
   - Test authentication failure, timeout, and circuit-open scenarios

5. **Create PR**
   - Branch: `forge/backend-developer/{ticket-id}-{service-name}-adapter`

## Plan References

| Document | Sections to Consult |
|----------|---------------------|
| ARCHITECTURE.md | Adapter patterns, retry strategy, circuit breaker config |
| CONTRIBUTING.md | Interface naming conventions, error mapping patterns |

## Quality Gates

- [ ] Adapter implements the defined internal interface — no direct calls from service layer
- [ ] Credentials read from environment or secrets manager — never hardcoded
- [ ] Retry and circuit breaker parameters are configurable, not hardcoded
- [ ] All external error types are mapped to internal errors
- [ ] Tests cover auth failure, network timeout, and 5xx scenarios

## Escalation Triggers

- External service has no sandbox or test environment
- Authentication scheme is non-standard and not documented
- External API changes are breaking and require service-level contract changes
- Rate limits are unknown — cannot set safe retry parameters

## Anti-Patterns

- Do not call external services directly from handlers or domain objects
- Do not catch and swallow connection errors — surface via internal error types
- Do not hardcode base URLs — use configuration injection
- Do not make synchronous calls with no timeout

## Examples

### Input

```json
{
  "jira_ticket": "PAY-1240",
  "service_spec": "Stripe API v3 — create PaymentIntent, capture, refund",
  "integration_type": "rest_client"
}
```

### Output

```json
{
  "branch": "forge/backend-developer/PAY-1240-stripe-adapter",
  "pr_number": 352,
  "files_created": [
    "adapters/stripe/client.go",
    "adapters/stripe/client_test.go",
    "adapters/stripe/fixtures/create_payment_intent_200.json"
  ]
}
```
