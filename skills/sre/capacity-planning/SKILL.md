# Capacity Planning Skill

## Purpose

Analyze current resource utilization and forecast capacity needs:
- CPU, memory, and storage utilization trends
- Pod autoscaling headroom analysis
- Database connection pool and storage runway
- Traffic growth projections with configurable assumptions
- Scaling recommendations with cost delta estimates

## Prerequisites

1. Jira ticket authorizing the capacity review
2. Metrics access: Datadog or Grafana configured with service dashboards
3. Kubernetes cluster access for pod and node utilization data
4. Cloud cost data accessible (AWS Cost Explorer or equivalent)

## Execution Steps

1. **Load Context**
   - Retrieve ticket and identify services in scope
   - Load ARCHITECTURE.md for service topology

2. **Collect Metrics**
   - Pull 30-day CPU and memory utilization P50/P95/P99 per service
   - Collect HPA min/max/current replica counts
   - Pull database CPU, connections, storage used/available
   - Collect RPS and latency trends

3. **Project Growth**
   - Apply `growth_assumption_pct` to traffic metrics
   - Project resource utilization at target `planning_horizon`
   - Identify services projected to exceed 80% utilization within horizon

4. **Generate Recommendations**
   - For each at-risk service: recommend scaling action, estimated cost delta
   - For database issues: recommend instance resize, read replica, or connection pool change
   - Flag services with HPA already at max replicas — require node scaling or optimization

5. **Create Artifacts**
   - Write capacity report (Markdown or Confluence page)
   - Create Jira tickets for high-risk findings requiring action this quarter
   - Post summary to SRE Slack channel

## Plan References

| Document | Sections to Consult |
|----------|---------------------|
| ARCHITECTURE.md | Service dependencies, expected traffic patterns, scaling strategy |

## Quality Gates

- [ ] All services in scope have utilization data
- [ ] Projections cover at least the requested `planning_horizon`
- [ ] High-risk findings have Jira action items
- [ ] Cost estimates are included for all scaling recommendations

## Escalation Triggers

- A service is currently above 80% capacity with no HPA headroom
- Database storage runway is less than 30 days
- Cloud spend trending to exceed budget before end of quarter

## Anti-Patterns

- Do not make scaling recommendations without actual utilization data
- Do not assume linear growth for all services — check for seasonal patterns
- Do not recommend scaling without also considering optimization (right-sizing)

## Examples

### Input

```json
{
  "jira_ticket": "SRE-080",
  "services": ["payments-api", "payments-worker"],
  "planning_horizon": 90,
  "growth_assumption_pct": 25
}
```

### Output

```json
{
  "services_analyzed": 2,
  "at_risk_findings": [
    {
      "service": "payments-worker",
      "issue": "Projected to reach 85% CPU within 60 days at 25% growth",
      "recommendation": "Increase HPA max replicas from 10 to 16 (+$120/mo)",
      "ticket": "SRE-081"
    }
  ],
  "report_url": "https://acme.atlassian.net/wiki/spaces/SRE/pages/345678"
}
```
