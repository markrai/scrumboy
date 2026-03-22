-- Migration 042: Add index for dashboard calendar-week completion query
-- Filters: project_id IN (...), column_key = ?, done_at >= ? AND done_at < ?

CREATE INDEX IF NOT EXISTS idx_todos_project_column_key_done_at
  ON todos(project_id, column_key, done_at);

-- Drop redundant index: idx_todos_project_column_key_rank is a subset of
-- idx_todos_project_column_key_rank_id (same leading columns; id is implicit in SQLite rowid).
DROP INDEX IF EXISTS idx_todos_project_column_key_rank;
