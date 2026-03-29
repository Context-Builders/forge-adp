-- Add retry_count to tasks for watchdog-driven requeue limiting.
-- started_at already exists in the schema but is never set; the watchdog
-- and AssignTask now populate it so stale-task detection can work.

ALTER TABLE tasks
    ADD COLUMN retry_count INTEGER NOT NULL DEFAULT 0;
