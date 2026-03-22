ALTER TABLE todos ADD COLUMN column_key TEXT;

UPDATE todos
SET column_key = CASE status
  WHEN 0 THEN 'backlog'
  WHEN 1 THEN 'not_started'
  WHEN 2 THEN 'doing'
  WHEN 3 THEN 'testing'
  WHEN 4 THEN 'done'
END;

CREATE INDEX IF NOT EXISTS idx_todos_project_column_key_rank_id
  ON todos(project_id, column_key, rank, id);

CREATE INDEX IF NOT EXISTS idx_todos_project_column_key_sprint_rank_id
  ON todos(project_id, column_key, sprint_id, rank, id);

CREATE INDEX IF NOT EXISTS idx_todos_assignee_column_key_done_at
  ON todos(assignee_user_id, column_key, done_at);
