# Pre-Deployment Checklist

## Infrastructure
- [ ] EKS cluster provisioned and healthy
- [ ] RDS instance running with backups enabled
- [ ] Redis cluster deployed
- [ ] S3 bucket for skill packages created
- [ ] VPC, subnets, and security groups configured
- [ ] SSL certificates provisioned

## Security
- [ ] All secrets stored in AWS Secrets Manager / Vault
- [ ] Network policies applied to namespaces
- [ ] Pod security policies enabled
- [ ] RBAC configured for Kubernetes access
- [ ] Audit logging enabled

## Integrations
- [ ] Jira OAuth app created and configured
- [ ] GitHub App installed on organization
- [ ] Slack App deployed and invited to channels
- [ ] Webhook URLs configured in all integrations

## Monitoring
- [ ] Prometheus/Grafana stack deployed
- [ ] Forge dashboards imported
- [ ] Alerts configured for critical metrics
- [ ] Log aggregation (ELK/Loki) configured

## Validation
- [ ] Control Plane health checks passing
- [ ] Integration adapters connected
- [ ] Test ticket → task → PR flow working
- [ ] Approval workflow in Slack working
