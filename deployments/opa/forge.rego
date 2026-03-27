package forge.authz

import future.keywords.in

default allow = false

# Protocol-level hard limits - never overridable
deny {
    input.action == "force_push"
    input.resource == "main"
}

deny {
    input.action == "delete"
    input.resource == "production_database"
}

deny {
    input.action == "disable"
    input.resource == "security_scan"
}

# Agent role permissions
allow {
    not deny
    agent_has_permission
}

agent_has_permission {
    input.action == "create_pr"
    role := get_agent_role(input.agent_id)
    role in ["backend-developer", "frontend-developer", "dba", "devops", "qa"]
}

agent_has_permission {
    input.action == "post_review"
    role := get_agent_role(input.agent_id)
    role in ["secops", "qa"]
}

agent_has_permission {
    input.action == "create_ticket"
    role := get_agent_role(input.agent_id)
    role == "pm"
}

agent_has_permission {
    input.action == "propose_migration"
    role := get_agent_role(input.agent_id)
    role == "dba"
}

# Governance agent permissions
# The governance agent may read audit data, open policy PRs, and post reports.
# It may NOT execute deployments, mutations, or any destructive action.
agent_has_permission {
    input.action in {
        "read_audit_log",
        "read_tasks",
        "read_policies",
        "create_ticket",
        "post_slack_message",
        "create_pr",
        "post_review",
    }
    role := get_agent_role(input.agent_id)
    role == "governance"
}

# Governance agent hard limits — never overridable
deny {
    input.action in {"deploy", "apply_migration", "force_push", "delete"}
    role := get_agent_role(input.agent_id)
    role == "governance"
}

# Helper to extract role from agent ID
get_agent_role(agent_id) = role {
    parts := split(agent_id, ":")
    role := parts[3]
}
