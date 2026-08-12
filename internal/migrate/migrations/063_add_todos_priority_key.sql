ALTER TABLE todos ADD COLUMN priority_key TEXT;

CREATE INDEX IF NOT EXISTS idx_todos_project_priority_key ON todos(project_id, priority_key);
