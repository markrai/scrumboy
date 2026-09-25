ALTER TABLE todos ADD COLUMN archived_at INTEGER NULL;

-- Migration 060's full chronological index had exactly one consumer: the
-- chronological (newest/oldest) board lane read. That read is active-only once
-- archival exists, so it is replaced by the partial form below. No surviving
-- query orders by (project_id, column_key, created_at) across archived rows.
DROP INDEX IF EXISTS idx_todos_project_column_key_created_at;

-- The partial indexes below COMPLEMENT the existing full-table todo indexes;
-- they do not supersede them, and the full-table forms are deliberately kept.
-- Archival is orthogonal to workflow state, so integrity and reporting reads
-- still have to see archived rows and still need unfiltered indexes:
--   * The full-table (project_id, column_key, ...) family -- currently
--     idx_todos_project_column_key_rank_id and
--     idx_todos_project_column_key_done_at -- backs DeleteWorkflowColumn's
--     "lane not empty" check and CountTodosByColumnKey. Both count archived
--     stories, so a lane still referenced by one cannot be deleted. SQLite
--     picks whichever of them covers the projection best.
--   * idx_todos_project_column_key_sprint_rank_id, idx_todos_assignee_updated
--     and idx_todos_project_updated keep serving their archived-inclusive
--     callers for the same reason.
-- Dropping any of those to "avoid duplication" would deoptimize the integrity
-- path, which is why only the single-consumer index above was removed.

CREATE INDEX IF NOT EXISTS idx_todos_active_project_column_rank_id
  ON todos(project_id, column_key, rank, id)
  WHERE archived_at IS NULL;

-- Serves the sprint-equality lane read. Today that is reached only by the MCP
-- board path (board mode "sprint"); the REST board emits "scheduled",
-- "unscheduled" or "sprint_number", none of which seek on sprint_id.
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
