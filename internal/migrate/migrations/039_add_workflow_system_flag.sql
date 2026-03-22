ALTER TABLE project_workflow_columns
  ADD COLUMN system INTEGER NOT NULL DEFAULT 0;

UPDATE project_workflow_columns
SET system = 1
WHERE key IN ('backlog', 'not_started', 'doing', 'testing', 'done');

CREATE INDEX IF NOT EXISTS idx_todos_project_column_key_rank
  ON todos(project_id, column_key, rank, id);
