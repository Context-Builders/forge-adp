-- Forge Control Plane Schema

-- Agent Registry
CREATE TABLE agents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    role VARCHAR(50) NOT NULL,
    instance_id VARCHAR(100) NOT NULL UNIQUE,
    company_id VARCHAR(100) NOT NULL,
    project_id VARCHAR(100) NOT NULL,
    status VARCHAR(20) DEFAULT 'idle',
    capabilities JSONB,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_agents_company_project ON agents(company_id, project_id);
CREATE INDEX idx_agents_role ON agents(role);

-- Skills Registry
CREATE TABLE skills (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_role VARCHAR(50) NOT NULL,
    skill_name VARCHAR(100) NOT NULL,
    version VARCHAR(20) NOT NULL,
    manifest JSONB NOT NULL,
    s3_path VARCHAR(500),
    tier VARCHAR(20) DEFAULT 'project',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(agent_role, skill_name, version)
);

CREATE INDEX idx_skills_role ON skills(agent_role);

-- Tasks
CREATE TABLE tasks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    jira_ticket_id VARCHAR(50),
    agent_id UUID REFERENCES agents(id),
    skill_name VARCHAR(100),
    status VARCHAR(30) DEFAULT 'created',
    priority INTEGER DEFAULT 0,
    input_payload JSONB,
    output_payload JSONB,
    error_message TEXT,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_tasks_status ON tasks(status);
CREATE INDEX idx_tasks_agent ON tasks(agent_id);
CREATE INDEX idx_tasks_jira ON tasks(jira_ticket_id);

-- Task Dependencies
CREATE TABLE task_dependencies (
    task_id UUID REFERENCES tasks(id),
    depends_on_task_id UUID REFERENCES tasks(id),
    PRIMARY KEY (task_id, depends_on_task_id)
);

-- Audit Log (append-only)
CREATE TABLE audit_log (
    id BIGSERIAL PRIMARY KEY,
    timestamp TIMESTAMPTZ DEFAULT NOW(),
    agent_id VARCHAR(200) NOT NULL,
    action VARCHAR(100) NOT NULL,
    target VARCHAR(500),
    jira_ticket VARCHAR(50),
    skill_used VARCHAR(100),
    plan_references TEXT[],
    autonomy_level INTEGER,
    approval_required BOOLEAN,
    context_hash VARCHAR(100),
    metadata JSONB
);

CREATE INDEX idx_audit_timestamp ON audit_log(timestamp);
CREATE INDEX idx_audit_agent ON audit_log(agent_id);
CREATE INDEX idx_audit_jira ON audit_log(jira_ticket);

-- Agent Memory (Medium-Term)
CREATE TABLE agent_memory (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_role VARCHAR(50) NOT NULL,
    project_id VARCHAR(100) NOT NULL,
    company_id VARCHAR(100) NOT NULL,
    category VARCHAR(50) NOT NULL,
    content TEXT NOT NULL,
    source_tickets TEXT[],
    confidence DECIMAL(3,2) DEFAULT 0.5,
    quarter VARCHAR(10),
    created_at TIMESTAMPTZ DEFAULT NOW(),
    last_referenced TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_memory_project_role ON agent_memory(company_id, project_id, agent_role);
CREATE INDEX idx_memory_quarter ON agent_memory(quarter);

-- LLM Cost Tracking
CREATE TABLE llm_usage (
    id BIGSERIAL PRIMARY KEY,
    task_id UUID REFERENCES tasks(id),
    agent_id UUID REFERENCES agents(id),
    provider VARCHAR(50),
    model VARCHAR(100),
    input_tokens INTEGER,
    output_tokens INTEGER,
    cost_usd DECIMAL(10,6),
    timestamp TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_llm_usage_task ON llm_usage(task_id);
CREATE INDEX idx_llm_usage_timestamp ON llm_usage(timestamp);

-- Policy Definitions
CREATE TABLE policies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(100) NOT NULL UNIQUE,
    scope VARCHAR(50) NOT NULL, -- 'protocol', 'company', 'project'
    company_id VARCHAR(100),
    project_id VARCHAR(100),
    rules JSONB NOT NULL,
    enabled BOOLEAN DEFAULT true,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_policies_scope ON policies(scope, company_id, project_id);