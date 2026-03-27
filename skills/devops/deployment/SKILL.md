# Deployment Skill

## Purpose

Create and update deployment configuration for services:
- Kubernetes Deployment, Service, and Ingress manifests
- Helm chart values updates for environment promotion
- ArgoCD Application definitions
- ConfigMap and Secret reference updates
- HPA (Horizontal Pod Autoscaler) configuration

## Prerequisites

1. Jira ticket with deployment requirements
2. Container image built and pushed to registry
3. Kubernetes namespace and RBAC configured for the target environment
4. Helm chart exists or will be created as part of this skill

## Execution Steps

1. **Load Context**
   - Retrieve ticket and load ARCHITECTURE.md and deployment runbooks
   - Identify deployment toolchain (Helm, ArgoCD, raw manifests)
   - Check current deployment state in target environment

2. **Generate/Update Manifests**
   - Update Helm values file for the target environment
   - Set resource requests/limits based on workload profile
   - Configure liveness and readiness probes
   - Wire environment variables from ConfigMaps and Secrets

3. **Apply Deployment Strategy**
   - Rolling update: configure `maxUnavailable` and `maxSurge`
   - Blue-green: create new Deployment and switch Service selector
   - Canary: configure traffic split via Ingress annotations or service mesh

4. **Validate**
   - Run `helm template` dry-run to validate manifest syntax
   - Run `kubectl apply --dry-run=server` to check server-side validity
   - Confirm resource quotas are not exceeded

5. **Create PR**
   - Branch: `forge/devops/{ticket-id}-{service-name}-deployment`
   - Requires DevOps lead approval before merge

## Plan References

| Document | Sections to Consult |
|----------|---------------------|
| ARCHITECTURE.md | Service dependencies, port assignments, scaling behavior |
| CONTRIBUTING.md | Helm values structure, namespace conventions |

## Quality Gates

- [ ] `helm template` produces valid YAML
- [ ] `kubectl apply --dry-run=server` passes
- [ ] Resource requests and limits are set on all containers
- [ ] Liveness and readiness probes are configured
- [ ] No secrets are stored in plaintext in manifests

## Escalation Triggers

- Target namespace does not exist or lacks required RBAC
- Resource quota would be exceeded by new deployment
- Deployment requires a new Secret that hasn't been provisioned in Vault
- Blue-green or canary strategy requires service mesh not yet installed

## Anti-Patterns

- Do not hardcode image digests — use parameterized image tags
- Do not set CPU/memory limits to zero or unlimited
- Do not commit Kubernetes Secrets with base64 values — use External Secrets Operator
- Do not deploy to production without first deploying to staging

## Examples

### Input

```json
{
  "jira_ticket": "OPS-120",
  "deployment_spec": {
    "service": "payments-api",
    "image_tag": "v1.4.2",
    "replicas": 3,
    "resources": { "cpu_request": "100m", "cpu_limit": "500m", "memory_request": "128Mi", "memory_limit": "512Mi" }
  },
  "target_environment": "staging",
  "deployment_strategy": "rolling"
}
```

### Output

```json
{
  "branch": "forge/devops/OPS-120-payments-api-deployment",
  "pr_number": 601,
  "files_modified": [
    "deployments/helm/forge/values.staging.yaml"
  ],
  "dry_run_status": "passed"
}
```
