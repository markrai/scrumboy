-- Migration 025: Add dashboard query index for assigned todos pagination

CREATE INDEX IF NOT EXISTS idx_todos_assignee_updated
ON todos(assignee_user_id, updated_at, id);
