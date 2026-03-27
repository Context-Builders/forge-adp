# Requirements Analysis Skill

## Purpose

Translate a product brief into structured functional and non-functional requirements that guide architecture and implementation decisions:
- Extract functional requirements from product descriptions and user stories
- Define non-functional requirements (performance, scalability, security, availability)
- Identify constraints, assumptions, and open questions
- Produce a requirements document that the PM and development agents can reference

## Prerequisites

1. Product brief or PRODUCT.md content provided as input
2. `config.yaml` available with tech stack and integration settings
3. Triggered by PM agent's `project-bootstrap` skill during initial project setup

## Execution Steps

1. **Load Context**
   - Read PRODUCT.md and config.yaml via `plan-reader`
   - Parse tech stack, integrations, and agent list from config

2. **Extract Functional Requirements**
   - Identify core user workflows from the product brief
   - Decompose features into specific, testable requirements
   - Assign a priority (Must, Should, Could) to each requirement
   - Cross-reference with the tech stack to flag feasibility concerns

3. **Define Non-Functional Requirements**
   - Derive performance targets from the product's scale and user base
   - Define availability and reliability requirements
   - Specify security requirements based on data sensitivity
   - Define scalability expectations based on growth projections
   - Identify compliance or regulatory requirements if mentioned

4. **Identify Constraints and Assumptions**
   - List technology constraints from the chosen stack
   - Flag assumptions made during analysis
   - Surface open questions that need product owner clarification

5. **Produce Requirements Document**
   - Structure output as a JSON payload with categorized requirements
   - Include traceability IDs for each requirement (REQ-001, REQ-002, etc.)
   - Flag any requirements that conflict or need arbitration

## Dependencies

- `common/plan-reader` — read product context and config

## Inputs

```json
{
  "product_brief": "(required) Product description, goals, and constraints",
  "product_md": "(optional) Content of PRODUCT.md if already populated",
  "config": "(optional) Parsed config.yaml content",
  "repo": "(required) GitHub repository in org/repo format"
}
```

## Outputs

```json
{
  "functional_requirements": [
    {
      "id": "REQ-001",
      "description": "...",
      "priority": "Must|Should|Could",
      "acceptance_criteria": ["..."],
      "source": "product_brief"
    }
  ],
  "non_functional_requirements": [
    {
      "id": "NFR-001",
      "category": "performance|security|scalability|availability|compliance",
      "description": "...",
      "target": "...",
      "rationale": "..."
    }
  ],
  "constraints": ["..."],
  "assumptions": ["..."],
  "open_questions": ["..."]
}
```

## Plan References

| Document | Sections to Consult |
|----------|---------------------|
| PRODUCT.md | Product vision, target users, key features |
| config.yaml | Tech stack constraints, integration requirements |

## Quality Gates

- [ ] Every feature in the product brief has at least one functional requirement
- [ ] Non-functional requirements cover performance, security, and availability at minimum
- [ ] Each requirement has a unique traceability ID
- [ ] Open questions are surfaced rather than silently assumed
- [ ] Requirements are specific and testable, not vague aspirations

## Escalation Triggers

- Product brief lacks enough detail to derive meaningful requirements
- Conflicting requirements detected that need product owner arbitration
- Regulatory or compliance requirements mentioned but specifics are missing

## Anti-Patterns

- Do not invent features not present in the product brief
- Do not specify implementation details — requirements describe *what*, not *how*
- Do not skip non-functional requirements even if the brief doesn't mention them
- Do not assume scale or performance targets without flagging them as assumptions
