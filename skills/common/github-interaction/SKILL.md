# GitHub Interaction Skill

## Purpose

Provides all GitHub API operations required by Forge agents:
- Create and manage branches
- Commit files to a branch
- Open, update, and merge pull requests
- Request and manage code reviews
- Query repository state (files, branches, PR status)

## Prerequisites

1. `GITHUB_TOKEN` environment variable set with repo + PR scope
2. Repository exists and agent has write access
3. Base branch specified or defaults to `main`

## Execution Steps

1. **Validate Action**
   - Confirm `action` is a supported operation
   - Verify `repo` is accessible with the configured token

2. **create_branch**
   - Check base branch exists
   - Create branch from base with the provided `branch_name`
   - Return branch ref

3. **commit_files**
   - Verify branch exists; create it if missing
   - For each file in `files`, create or update the blob via API
   - Create a tree and commit pointing to it
   - Update branch ref to new commit SHA

4. **open_pr**
   - Verify head branch has commits ahead of base
   - Create PR with `pr_config.title`, `pr_config.body`, base
   - Assign reviewers from `pr_config.reviewers`
   - Return PR number and URL

5. **request_review**
   - Add reviewers or teams to an existing PR
   - Leave a comment if a message is provided

6. **merge_pr**
   - Confirm all required checks are green (Policy Engine gate)
   - Merge using squash strategy unless overridden
   - Delete head branch after merge

## Plan References

| Document | Sections to Consult |
|----------|---------------------|
| CONTRIBUTING.md | Branch naming conventions, PR template |
| ARCHITECTURE.md | Repository layout and module boundaries |

## Quality Gates

- [ ] Branch name follows `forge/{agent-role}/{ticket-id}-{slug}` convention
- [ ] PR includes link to Jira ticket in body
- [ ] PR has at least one human reviewer assigned
- [ ] All CI checks pass before merge

## Escalation Triggers

- GitHub API rate limit exceeded (429)
- Authentication failure (401/403)
- Merge conflict detected on PR
- Required status check failing and cannot be resolved automatically

## Anti-Patterns

- Do not force-push to shared branches
- Do not open PRs targeting a branch other than `main` or the configured base branch without explicit instruction
- Do not merge PRs that have unresolved review comments
- Do not store the GitHub token in committed files

## Examples

### Input

```json
{
  "action": "open_pr",
  "repo": "acme-corp/payments-service",
  "pr_config": {
    "title": "feat(PAY-1234): add GET /payments/:id endpoint",
    "body": "Closes PAY-1234\n\n## Changes\n- Added route handler\n- Added unit tests\n- Updated OpenAPI spec",
    "head": "forge/backend-developer/PAY-1234-get-payment",
    "base": "main",
    "reviewers": ["jane-smith", "bob-jones"]
  }
}
```

### Output

```json
{
  "pr_number": 347,
  "url": "https://github.com/acme-corp/payments-service/pull/347",
  "status": "open"
}
```
