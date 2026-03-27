# Page Implementation Skill

## Purpose

Implement complete application pages by composing reusable components, wiring data fetching, and registering routes:
- Page-level container components
- Route registration and navigation integration
- Data fetching hooks or server-side data loading
- Page-level error boundaries and loading states
- SEO metadata and page titles where applicable

## Prerequisites

1. Jira ticket with page requirements
2. UX spec (Figma or equivalent) describing layout and interactions
3. Required components exist or will be created alongside this page
4. API endpoints the page consumes are documented

## Execution Steps

1. **Load Context**
   - Retrieve ticket and load ARCHITECTURE.md and API_CONTRACTS.md
   - Identify routing library and data fetching patterns from architecture doc
   - List required components; flag any that don't exist yet

2. **Design Page Structure**
   - Map UX spec sections to components
   - Plan data fetching: which endpoints, when to call, caching strategy
   - Identify protected route requirements (auth guards)

3. **Generate Page Component**
   - Create page container with route registration
   - Implement data fetching using project-standard pattern (SWR, React Query, server actions, etc.)
   - Compose existing or newly created components
   - Add error boundary, loading spinner, and empty state handling

4. **Generate Tests**
   - Integration test rendering the full page with mocked API responses
   - Navigation tests: correct route renders correct page
   - Auth guard tests if page is protected

5. **Create PR**
   - Branch: `forge/frontend-developer/{ticket-id}-{page-name}-page`

## Plan References

| Document | Sections to Consult |
|----------|---------------------|
| ARCHITECTURE.md | Routing library, data fetching strategy, auth patterns |
| API_CONTRACTS.md | Request/response shapes for data the page consumes |
| CONTRIBUTING.md | Page file naming, route registration location |

## Quality Gates

- [ ] Page renders without errors in all data states
- [ ] Route is registered and navigable from existing navigation
- [ ] Auth guard applied to protected pages
- [ ] TypeScript strict — no `any`
- [ ] All new tests pass

## Escalation Triggers

- Required components don't exist and are complex enough to require a separate ticket
- UX spec is ambiguous about responsive behavior or edge states
- Page requires new global state that impacts other pages

## Anti-Patterns

- Do not put business logic in page components — delegate to hooks or services
- Do not directly call `fetch` in components — use the project data fetching layer
- Do not duplicate data fetching logic across pages — create a shared hook
- Do not hardcode route paths — use route constants

## Examples

### Input

```json
{
  "jira_ticket": "FE-215",
  "ux_spec": "https://figma.com/file/def456/PaymentDetailPage",
  "route_path": "/payments/:id",
  "data_requirements": ["GET /api/v1/payments/:id", "GET /api/v1/payments/:id/events"]
}
```

### Output

```json
{
  "branch": "forge/frontend-developer/FE-215-payment-detail-page",
  "pr_number": 408,
  "files_created": [
    "src/pages/PaymentDetailPage/PaymentDetailPage.tsx",
    "src/pages/PaymentDetailPage/PaymentDetailPage.test.tsx",
    "src/pages/PaymentDetailPage/usePaymentDetail.ts",
    "src/pages/PaymentDetailPage/index.ts"
  ]
}
```
