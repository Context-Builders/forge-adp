DROP INDEX IF EXISTS idx_memory_project_scope;

ALTER TABLE agent_memory DROP COLUMN IF EXISTS scope;
