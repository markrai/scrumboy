-- Migration 033: Index for board/lane queries with sprint filter
-- Covers WHERE project_id = ? AND status = ? AND sprint_id ... ORDER BY rank, id
-- so sprint filtering (assigned / unscheduled / sprint_id = ?) stays fast on large boards.

CREATE INDEX IF NOT EXISTS idx_todos_project_status_sprint_rank_id
  ON todos(project_id, status, sprint_id, rank, id);
