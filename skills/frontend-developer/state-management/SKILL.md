# State Management Skill

## Purpose

Implement application state management following the project's established patterns:
- Global store slices/atoms for cross-feature state
- Feature-scoped stores for local feature state
- Selectors and derived state
- Async action creators with loading/error states
- Persistence configuration where required

## Prerequisites

1. Jira ticket with state requirements
2. Frontend architecture document identifying state management library and conventions
3. API contracts for any async state derived from server data

## Execution Steps

1. **Load Context**
   - Retrieve ticket and load ARCHITECTURE.md
   - Identify state management library (Redux Toolkit, Zustand, Jotai, React Context)
   - Review existing store structure to avoid naming conflicts

2. **Design State Shape**
   - Define TypeScript interfaces for state slice
   - Identify synchronous actions and async thunks/mutations
   - Design selectors for each consumer use case
   - Plan optimistic update strategies if applicable

3. **Generate Store Implementation**
   - Create slice/store/atom following project conventions
   - Implement actions, reducers, and selectors
   - Wire async operations with loading/error/success states
   - Register with root store if required

4. **Generate Tests**
   - Unit tests for reducers/actions: test each action and resulting state
   - Selector tests with various input states
   - Async action tests with mocked API calls

5. **Create PR**
   - Branch: `forge/frontend-developer/{ticket-id}-{feature}-state`

## Plan References

| Document | Sections to Consult |
|----------|---------------------|
| ARCHITECTURE.md | State management library, store structure, async patterns |
| CONTRIBUTING.md | Slice naming, selector conventions, test requirements |

## Quality Gates

- [ ] State shape has complete TypeScript types — no `any`
- [ ] All async operations have loading and error states
- [ ] Selectors are memoized where appropriate
- [ ] No direct state mutations (immutable update patterns)
- [ ] All new tests pass

## Escalation Triggers

- Required state change would necessitate restructuring the root store
- State needs to be persisted across sessions and persistence strategy isn't defined
- Multiple concurrent writers to the same state slice create race conditions

## Anti-Patterns

- Do not store derived data in state — use selectors
- Do not put UI state (modal open/closed) in global store — use local component state
- Do not dispatch actions from reducers
- Do not store non-serializable values in Redux stores

## Examples

### Input

```json
{
  "jira_ticket": "FE-220",
  "state_spec": "Payment list state: fetch list by date range, selected payment, loading/error states",
  "state_library": "redux",
  "affected_features": ["PaymentListPage", "PaymentSummaryWidget"]
}
```

### Output

```json
{
  "branch": "forge/frontend-developer/FE-220-payments-state",
  "pr_number": 415,
  "files_created": [
    "src/store/payments/paymentsSlice.ts",
    "src/store/payments/paymentsSelectors.ts",
    "src/store/payments/paymentsThunks.ts",
    "src/store/payments/paymentsSlice.test.ts"
  ]
}
```
