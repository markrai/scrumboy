-- Supports chronological board and lane pagination.
-- todos.id is INTEGER PRIMARY KEY, so SQLite stores it as the implicit rowid
-- tiebreaker after created_at in this index.
CREATE INDEX IF NOT EXISTS idx_todos_project_column_key_created_at
  ON todos(project_id, column_key, created_at);
