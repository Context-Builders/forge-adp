# Component Implementation Skill

## Purpose

Implement reusable, accessible UI components from design specifications:
- Presentational and container components
- Form elements with validation
- Data display components consuming API responses
- Shared design-system primitives
- Component story files and unit tests

## Prerequisites

1. Jira ticket with component requirements and acceptance criteria
2. Design spec (Figma or equivalent) or written style requirements
3. API contract if component fetches or displays API data
4. Frontend architecture document with component patterns

## Execution Steps

1. **Load Context**
   - Retrieve ticket and load plan documents via `plan-reader`
   - Identify framework (React, Vue, etc.) and CSS strategy from ARCHITECTURE.md
   - Check design system/component library in use

2. **Design Component Interface**
   - Define props interface with TypeScript types
   - Identify state requirements (local vs. lifted)
   - Plan accessibility attributes (ARIA roles, keyboard navigation)

3. **Generate Component**
   - Implement component following project file structure
   - Apply design tokens or CSS classes per CONTRIBUTING guidelines
   - Add loading, error, and empty states
   - Export from component barrel file

4. **Generate Tests**
   - Unit tests: render, prop variations, user interactions
   - Accessibility check using axe or jest-axe
   - Storybook story for each major state (optional per project config)

5. **Create PR**
   - Branch: `forge/frontend-developer/{ticket-id}-{ComponentName}`

## Plan References

| Document | Sections to Consult |
|----------|---------------------|
| ARCHITECTURE.md | Component structure, styling strategy, state management approach |
| CONTRIBUTING.md | File naming, export conventions, test requirements |
| API_CONTRACTS.md | Response shapes for data-driven components |

## Quality Gates

- [ ] Component renders without errors in all prop combinations
- [ ] TypeScript types are strict — no `any`
- [ ] Accessibility: no critical axe violations
- [ ] Loading and error states are handled
- [ ] All existing tests still pass

## Escalation Triggers

- Design spec is missing or contradicts API contract
- Component requires a new design system primitive not yet available
- Accessibility requirements are unclear or conflict with the design

## Anti-Patterns

- Do not fetch data inside presentational components
- Do not use inline styles — use design tokens or CSS modules
- Do not skip empty and loading state handling
- Do not use `any` for prop types

## Examples

### Input

```json
{
  "jira_ticket": "FE-210",
  "design_spec": "https://figma.com/file/abc123/PaymentCard",
  "api_contract": {
    "id": "string",
    "amount": "number",
    "currency": "string",
    "status": "PENDING | CAPTURED | REFUNDED"
  },
  "component_name": "PaymentCard"
}
```

### Output

```json
{
  "branch": "forge/frontend-developer/FE-210-PaymentCard",
  "pr_number": 401,
  "files_created": [
    "src/components/PaymentCard/PaymentCard.tsx",
    "src/components/PaymentCard/PaymentCard.test.tsx",
    "src/components/PaymentCard/index.ts"
  ]
}
```
