ALTER TABLE todos ADD COLUMN archived_at INTEGER NULL;

-- Migration 060's full chronological index served board reads. Those reads
-- are active-only once archival exists, so replace it with the smaller partial
-- form below instead of retaining two indexes with the same leading columns.
DROP INDEX IF EXISTS idx_todos_project_column_key_created_at;

CREATE INDEX IF NOT EXISTS idx_todos_active_project_column_rank_id
  ON todos(project_id, column_key, rank, id)
  WHERE archived_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_todos_active_project_column_sprint_rank_id
  ON todos(project_id, column_key, sprint_id, rank, id)
  WHERE archived_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_todos_active_project_column_created_at
  ON todos(project_id, column_key, created_at, id)
  WHERE archived_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_todos_active_assignee_updated
  ON todos(assignee_user_id, updated_at, id)
  WHERE archived_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_todos_active_project_updated_local_id
  ON todos(project_id, updated_at DESC, local_id ASC)
  WHERE archived_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_todos_archived_project_archived_at_id
  ON todos(project_id, archived_at DESC, id DESC)
  WHERE archived_at IS NOT NULL;
