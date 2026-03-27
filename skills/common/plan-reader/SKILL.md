# Plan Reader Skill

## Purpose

Retrieves structured plan documents that agents use as context before executing tasks:
- Architecture docs (`ARCHITECTURE.md`)
- API contracts (`API_CONTRACTS.md`)
- Data model definitions (`DATA_MODEL.md`)
- Contributing guidelines (`CONTRIBUTING.md`)
- Any custom plan documents in `.forge/plans/`

Documents are returned as a dictionary keyed by filename for direct consumption by other skills.

## Prerequisites

1. `GITHUB_TOKEN` with read access to the target repository
2. `CONFLUENCE_BASE_URL`, `CONFLUENCE_USERNAME`, `CONFLUENCE_API_TOKEN` if Confluence fallback is needed
3. Repository contains a `.forge/` directory or Confluence space key is configured

## Execution Steps

1. **Build Document List**
   - Accept `documents` list from calling agent
   - Add any documents referenced in `.forge/config.yaml` under `plan_documents`

2. **Attempt GitHub Read**
   - For each document, attempt to read from `.forge/plans/{document}` in the target repo
   - Fall back to reading from the repository root if not found in `.forge/plans/`
   - Cache results in memory for the duration of the task

3. **Attempt Confluence Fallback**
   - For any documents not found in GitHub, search the configured Confluence space by title
   - Convert Confluence storage format to plain text
   - Mark documents fetched from Confluence with `source: confluence`

4. **Return Results**
   - Return `{filename: content}` dict for all resolved documents
   - Populate `missing_documents` list for any that could not be found
   - Log warning if critical documents (ARCHITECTURE.md) are missing

## Plan References

| Document | Sections to Consult |
|----------|---------------------|
| `.forge/config.yaml` | `plan_documents` list and Confluence space key |

## Quality Gates

- [ ] At least one document successfully retrieved
- [ ] Document content is non-empty (not a 404 page or empty file)
- [ ] Missing critical documents are surfaced as warnings before task execution
- [ ] Documents over 100KB are truncated with a notice

## Escalation Triggers

- ARCHITECTURE.md missing from both GitHub and Confluence
- Both GitHub and Confluence authentication failing
- All requested documents are missing

## Anti-Patterns

- Do not cache documents across separate task executions
- Do not silently return empty string for missing documents — always populate `missing_documents`
- Do not attempt to parse or interpret document contents in this skill

## Examples

### Input

```json
{
  "documents": ["ARCHITECTURE.md", "API_CONTRACTS.md", "DATA_MODEL.md"],
  "repo": "acme-corp/payments-service",
  "branch": "main"
}
```

### Output

```json
{
  "documents": {
    "ARCHITECTURE.md": "# Payments Service Architecture\n\n...",
    "API_CONTRACTS.md": "# API Contracts\n\n..."
  },
  "missing_documents": ["DATA_MODEL.md"]
}
```
