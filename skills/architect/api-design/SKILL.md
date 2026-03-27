# API Design Skill

## Purpose

Design the API surface for a project and populate API_CONTRACTS.md with concrete endpoint specifications:
- Define RESTful endpoints derived from functional requirements
- Specify request/response schemas for each endpoint
- Establish consistent patterns for pagination, filtering, and error responses
- Ensure API design aligns with the architecture and data model

## Prerequisites

1. Requirements analysis completed (functional requirements available)
2. Architecture design completed or in progress (entities and service boundaries known)
3. `config.yaml` available with tech stack definition
4. Triggered by PM agent's `project-bootstrap` skill after `requirements-analysis` completes

## Execution Steps

1. **Load Context**
   - Read config.yaml, seeded API_CONTRACTS.md stub, and any available ARCHITECTURE.md via `plan-reader`
   - Receive requirements analysis output from the preceding task
   - Note the existing response envelope and error format from the API_CONTRACTS.md stub

2. **Identify API Resources**
   - Map functional requirements to REST resources
   - Group endpoints by domain/service boundary
   - Identify CRUD operations needed for each resource
   - Identify non-CRUD actions (workflows, batch operations, etc.)

3. **Design Endpoints**
   - Define route paths following RESTful conventions
   - Specify HTTP methods for each operation
   - Define request body schemas with field types and validation rules
   - Define response schemas using the existing response envelope
   - Specify path parameters, query parameters, and headers
   - Design pagination strategy for list endpoints
   - Design filtering and sorting patterns

4. **Define Authentication and Authorization**
   - Specify which endpoints require authentication
   - Define required roles or permissions per endpoint
   - Document any public (unauthenticated) endpoints

5. **Generate API_CONTRACTS.md**
   - Preserve the existing versioning strategy, response envelope, error format, and status codes
   - Append endpoint specifications grouped by resource
   - Include request/response examples for each endpoint
   - Replace all placeholder comments with substantive content

## Dependencies

- `common/plan-reader` — read stubs and config
- `common/github-interaction` — commit populated documents

## Inputs

```json
{
  "requirements": "(required) Output from requirements-analysis skill",
  "entities": "(optional) Entity list from architecture-design skill if available",
  "product_brief": "(required) Original product description",
  "config": "(optional) Parsed config.yaml content",
  "repo": "(required) GitHub repository in org/repo format"
}
```

## Outputs

```json
{
  "api_contracts_md": "Full content for API_CONTRACTS.md",
  "endpoints": [
    {
      "method": "GET|POST|PUT|PATCH|DELETE",
      "path": "/api/v1/...",
      "description": "...",
      "auth_required": true,
      "request_schema": {},
      "response_schema": {},
      "status_codes": [200, 400, 401, 404]
    }
  ],
  "resource_count": 5,
  "endpoint_count": 18
}
```

## Plan References

| Document | Sections to Consult |
|----------|---------------------|
| API_CONTRACTS.md | Existing response envelope, error format, status codes to preserve |
| ARCHITECTURE.md | Service boundaries and API design patterns |
| DATA_MODEL.md | Entity definitions for resource mapping |
| config.yaml | Tech stack (influences serialization, auth patterns) |

## Quality Gates

- [ ] API_CONTRACTS.md has no remaining placeholder comments
- [ ] Every functional requirement with a user-facing action maps to at least one endpoint
- [ ] All endpoints use the existing response envelope and error format
- [ ] Request schemas include field types and validation constraints
- [ ] List endpoints include pagination parameters
- [ ] Authentication requirements specified for every endpoint
- [ ] At least one request/response example per endpoint group

## Escalation Triggers

- Functional requirements imply real-time capabilities (WebSockets, SSE) not covered by REST patterns
- More than 50 endpoints identified — may indicate need for API gateway or service decomposition
- Requirements imply GraphQL or gRPC patterns that conflict with the REST-first stub

## Anti-Patterns

- Do not change the existing versioning strategy, response envelope, or error format from the stub
- Do not design endpoints for features not present in the requirements
- Do not use inconsistent naming conventions across endpoints
- Do not skip pagination for list endpoints
- Do not define endpoints without specifying authentication requirements
