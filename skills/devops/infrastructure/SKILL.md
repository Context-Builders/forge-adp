# Infrastructure Skill

## Purpose

Create and manage cloud infrastructure using Infrastructure-as-Code:
- Compute resources (EC2, EKS node groups, GKE clusters)
- Managed databases (RDS, Cloud SQL, Cosmos DB)
- Networking (VPCs, subnets, security groups, load balancers)
- Storage (S3 buckets, GCS, Azure Blob)
- IAM roles and policies

## Prerequisites

1. Jira ticket with infrastructure requirements and capacity estimates
2. Existing IaC codebase accessible in repository
3. Cloud provider credentials configured in CI/CD pipeline
4. SecOps has reviewed security requirements

## Execution Steps

1. **Load Context**
   - Retrieve ticket and load existing Terraform/Pulumi modules
   - Identify cloud provider and IaC tool in use
   - Review existing naming and tagging conventions

2. **Design Resources**
   - Define resource specifications: type, size, region, redundancy
   - Identify dependencies between resources
   - Plan IAM permissions following least-privilege principle
   - Assess security group/firewall rule requirements

3. **Generate IaC**
   - Create or update Terraform `.tf` files following project module structure
   - Use existing project modules where available; create new modules for reusable patterns
   - Add required tags (environment, team, cost center, managed-by)
   - Parameterize values that differ between environments

4. **Run Plan**
   - Execute `terraform init` and `terraform plan` against the target workspace
   - Review plan output for unexpected resource replacements or deletions
   - Flag any destructive changes for explicit human approval

5. **Create PR**
   - Branch: `forge/devops/{ticket-id}-{resource-name}-infra`
   - Attach `terraform plan` output as PR comment
   - Requires DevOps lead + SecOps review

## Plan References

| Document | Sections to Consult |
|----------|---------------------|
| ARCHITECTURE.md | Cloud architecture, region strategy, network topology |
| CONTRIBUTING.md | IaC module structure, tagging standards, naming conventions |

## Quality Gates

- [ ] `terraform plan` exits with code 0 and no errors
- [ ] No resource deletions without explicit approval
- [ ] All resources are tagged with required tags
- [ ] IAM policies follow least-privilege (no `*` actions without justification)
- [ ] SecOps has reviewed any security group or IAM changes

## Escalation Triggers

- Terraform plan contains unexpected resource replacement (destroy + create)
- New IAM role grants permissions that violate security policy
- Resource cost estimate exceeds budget threshold
- Required cloud account/project permissions are not provisioned for the Terraform service account

## Anti-Patterns

- Do not hardcode account IDs, region names, or credentials in `.tf` files
- Do not use `terraform apply` directly — all changes go through PRs and CI
- Do not create resources outside of IaC (manual console changes) without immediate import
- Do not use `count = 0` to "soft delete" resources — remove the resource block

## Examples

### Input

```json
{
  "jira_ticket": "OPS-130",
  "infra_spec": "Create an RDS PostgreSQL 16 instance: db.t3.medium, Multi-AZ, encrypted at rest, in the payments VPC private subnet.",
  "cloud_provider": "aws",
  "iac_tool": "terraform"
}
```

### Output

```json
{
  "branch": "forge/devops/OPS-130-payments-rds-infra",
  "pr_number": 610,
  "files_created": ["deployments/terraform/rds_payments.tf"],
  "plan_summary": "+4 resources, ~0 changes, ~0 destructions",
  "estimated_monthly_cost": "$185"
}
```
