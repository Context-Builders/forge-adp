# Test Planning Skill

## Purpose

Generate a comprehensive test plan for a feature or release:
- Test scope and out-of-scope definition
- Test cases for each acceptance criterion
- Risk-based test prioritization
- Test environment and data requirements
- Test schedule aligned to sprint timeline

Plans are saved to Confluence and linked test case tickets created in Jira.

## Prerequisites

1. Jira Epic or Story with acceptance criteria
2. Confluence space configured for test plans
3. Architecture and API contract documents accessible

## Execution Steps

1. **Load Context**
   - Retrieve ticket and all linked child tickets
   - Load ARCHITECTURE.md and API_CONTRACTS.md via `plan-reader`
   - Identify existing test coverage and gaps

2. **Define Scope**
   - List features and components in scope
   - Explicitly document out-of-scope items
   - Map acceptance criteria to testable scenarios

3. **Design Test Cases**
   - For each acceptance criterion: write positive, negative, and boundary test cases
   - Assign test types: unit, integration, E2E, manual
   - Mark risk area test cases for priority execution
   - Estimate execution time per test case

4. **Define Environment Requirements**
   - List required test data
   - Document environment configuration needed
   - Identify external service dependencies and whether stubs/mocks are needed

5. **Create Artifacts**
   - Write test plan to Confluence page
   - Create Jira sub-tasks for manual test cases
   - Link test plan ticket back to feature Epic

## Plan References

| Document | Sections to Consult |
|----------|---------------------|
| ARCHITECTURE.md | Component boundaries for scope definition |
| API_CONTRACTS.md | API behavior for contract testing cases |
| CONTRIBUTING.md | Test environment setup, data seeding |

## Quality Gates

- [ ] Every acceptance criterion has at least one test case
- [ ] Risk areas have explicit test cases
- [ ] Environment requirements documented
- [ ] Confluence page created with test plan
- [ ] QA lead or PM has approved the test plan

## Escalation Triggers

- Acceptance criteria are ambiguous or absent — request clarification before planning
- Required test environment is not provisioned
- Feature requires external service testing that needs vendor coordination

## Anti-Patterns

- Do not write test cases before acceptance criteria are finalized
- Do not plan for 100% automated coverage on manual exploratory areas
- Do not skip documenting out-of-scope items

## Examples

### Input

```json
{
  "jira_ticket": "PAY-1260",
  "test_scope": ["refund-api", "refund-ui", "email-notifications"],
  "risk_areas": ["partial refund calculations", "concurrent refund requests"]
}
```

### Output

```json
{
  "test_cases_created": 18,
  "jira_tickets_created": ["PAY-1290", "PAY-1291", "PAY-1292"],
  "confluence_page_url": "https://acme.atlassian.net/wiki/spaces/ACME/pages/234567",
  "estimated_execution_hours": 6.5
}
```
