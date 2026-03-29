-- Add scope to agent_memory so memories can be shared across agent roles.
-- 'role'    — visible only to the agent role that wrote it (existing behaviour)
-- 'project' — visible to all agent roles within the same project

ALTER TABLE agent_memory
    ADD COLUMN scope VARCHAR(20) NOT NULL DEFAULT 'role';

CREATE INDEX idx_memory_project_scope ON agent_memory(company_id, project_id, scope);
