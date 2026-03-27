# Architecture Design Skill

## Purpose

Design the technical architecture for a project and populate ARCHITECTURE.md and DATA_MODEL.md with concrete, actionable content:
- Define system boundaries, service decomposition, and component responsibilities
- Design the data model with entities, relationships, and migration strategy
- Establish API design patterns, error handling, and security approach
- Ensure architecture aligns with the chosen tech stack and requirements

## Prerequisites

1. Requirements analysis completed (functional and non-functional requirements available)
2. `config.yaml` available with tech stack definition
3. PRODUCT.md populated with product vision
4. Triggered by PM agent's `project-bootstrap` skill after `requirements-analysis` completes

## Execution Steps

1. **Load Context**
   - Read config.yaml, PRODUCT.md, and seeded ARCHITECTURE.md / DATA_MODEL.md stubs via `plan-reader`
   - Receive requirements analysis output from the preceding task

2. **Design System Architecture**
   - Define the high-level system overview based on the tech stack and requirements
   - Identify service boundaries (monolith vs microservices based on project scale)
   - Map components to the tech stack (frontend, backend, database, infrastructure)
   - Define inter-service communication patterns
   - Design error handling strategy aligned with the API contracts pattern

3. **Design Security Architecture**
   - Define authentication approach (JWT, session-based, OAuth2, etc.)
   - Define authorization model (RBAC, ABAC, etc.)
   - Identify data classification and encryption requirements
   - Map security requirements from the requirements analysis

4. **Design Data Model**
   - Identify core entities from functional requirements
   - Define entity attributes and types
   - Map relationships (one-to-one, one-to-many, many-to-many)
   - Design migration strategy appropriate for the chosen database
   - Consider indexing strategy for key query patterns

5. **Design Data Flow**
   - Map how data moves through the system for core user workflows
   - Identify caching opportunities
   - Define event/message patterns if applicable

6. **Generate Documents**
   - Populate ARCHITECTURE.md with all sections: system overview, tech stack, service boundaries, API patterns, error handling, security, data flow
   - Populate DATA_MODEL.md with entities, relationships, and migration strategy
   - Ensure all placeholder comments are replaced with substantive content

## Dependencies

- `common/plan-reader` — read stubs and config
- `common/github-interaction` — commit populated documents

## Inputs

```json
{
  "requirements": "(required) Output from requirements-analysis skill",
  "product_brief": "(required) Original product description",
  "config": "(optional) Parsed config.yaml content",
  "repo": "(required) GitHub repository in org/repo format"
}
```

## Outputs

```json
{
  "architecture_md": "Full content for ARCHITECTURE.md",
  "data_model_md": "Full content for DATA_MODEL.md",
  "entities": [
    {
      "name": "...",
      "attributes": [{"name": "...", "type": "...", "constraints": "..."}],
      "relationships": [{"target": "...", "type": "one-to-many", "description": "..."}]
    }
  ],
  "architecture_decisions": [
    {
      "decision": "...",
      "rationale": "...",
      "alternatives_considered": ["..."]
    }
  ]
}
```

## Plan References

| Document | Sections to Consult |
|----------|---------------------|
| PRODUCT.md | Product vision and feature scope |
| config.yaml | Tech stack, infrastructure, CI/CD |
| ARCHITECTURE.md | Stub to populate |
| DATA_MODEL.md | Stub to populate |

## Quality Gates

- [ ] ARCHITECTURE.md has no remaining placeholder comments
- [ ] DATA_MODEL.md has no remaining placeholder comments
- [ ] System overview accurately reflects the tech stack from config.yaml
- [ ] Every core entity identified in requirements has a corresponding data model entry
- [ ] Security section addresses authentication and authorization
- [ ] Architecture decisions include rationale and alternatives considered
- [ ] Data model includes migration strategy appropriate for the chosen database

## Escalation Triggers

- Tech stack combination has known incompatibilities
- Requirements imply scale that conflicts with chosen infrastructure
- Security requirements demand capabilities not supported by the chosen stack
- Data model complexity suggests the need for multiple database technologies

## Anti-Patterns

- Do not design microservices for a project that would be better served by a monolith
- Do not over-architect — match complexity to the requirements, not theoretical best practices
- Do not specify vendor-specific features without confirming they're available in the chosen stack
- Do not skip the migration strategy — every data model needs a clear path for schema evolution
- Do not ignore non-functional requirements when making architecture decisions
