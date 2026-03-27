# Test Generation Skill

## Purpose

Generate comprehensive test suites for existing backend code:
- Unit tests for pure functions, services, and domain logic
- Integration tests for repository, adapter, and API layers
- Table-driven test patterns for validation rules
- Mock and fixture generation
- Coverage gap identification and targeted test addition

## Prerequisites

1. Jira ticket specifying test coverage requirements
2. Source files to test are accessible in the repository
3. Existing test framework and conventions identified from `CONTRIBUTING.md`
4. Any required test fixtures or database seeds available

## Execution Steps

1. **Load Context**
   - Retrieve ticket and load `CONTRIBUTING.md` for test conventions
   - Read all `source_files` to understand implementation details
   - Identify existing test files to avoid duplication

2. **Analyze Coverage Gaps**
   - Parse source files to identify untested exported functions/methods
   - Identify paths with no existing assertions
   - Prioritize: error paths, boundary conditions, state transitions

3. **Generate Unit Tests**
   - Create table-driven tests for each function signature
   - Generate mock implementations for all interface dependencies
   - Cover: happy path, input validation errors, all branches

4. **Generate Integration Tests** *(if requested)*
   - Create tests that exercise full request-to-response paths
   - Use test database or Docker-based fixtures
   - Verify external adapter calls with recorded HTTP fixtures

5. **Validate Coverage**
   - Run coverage reporting and confirm `coverage_target` is met
   - Report uncovered lines and add targeted tests if needed

6. **Create PR**
   - Branch: `forge/backend-developer/{ticket-id}-test-coverage`

## Plan References

| Document | Sections to Consult |
|----------|---------------------|
| CONTRIBUTING.md | Test file naming, mock patterns, coverage requirements |
| ARCHITECTURE.md | Module boundaries — what to mock vs. use real implementations |

## Quality Gates

- [ ] All generated tests pass on first run
- [ ] Coverage meets or exceeds `coverage_target`
- [ ] No tests make real external network calls (use mocks/fixtures)
- [ ] Test file names follow project conventions
- [ ] No flaky tests (all tests are deterministic)

## Escalation Triggers

- Source code has no clear contracts or interfaces — cannot generate meaningful mocks
- Coverage target is impossible without refactoring source code (hidden dependencies)
- Integration tests require infrastructure not available in CI

## Anti-Patterns

- Do not generate tests that only assert `not nil` — test meaningful behavior
- Do not share mutable state between test cases
- Do not use `time.Sleep` in tests — use deterministic clocks
- Do not generate tests for generated code (protobuf, ORM models)

## Examples

### Input

```json
{
  "jira_ticket": "PAY-1250",
  "source_files": [
    "src/services/payment_service.go",
    "src/domain/payment.go"
  ],
  "test_types": ["unit"],
  "coverage_target": 90
}
```

### Output

```json
{
  "branch": "forge/backend-developer/PAY-1250-test-coverage",
  "pr_number": 360,
  "files_created": [
    "src/services/payment_service_test.go",
    "src/domain/payment_test.go"
  ],
  "coverage_achieved": 93.4
}
```
