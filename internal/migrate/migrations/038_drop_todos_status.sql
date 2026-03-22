CREATE TABLE todos_new (
  id INTEGER PRIMARY KEY,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  local_id INTEGER,
  title TEXT NOT NULL,
  body TEXT NOT NULL DEFAULT '',
  column_key TEXT NOT NULL,
  rank INTEGER NOT NULL,
  estimation_points INTEGER,
  assignee_user_id INTEGER,
  sprint_id INTEGER REFERENCES sprints(id) ON DELETE SET NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  done_at INTEGER
);

INSERT INTO todos_new (
  id, project_id, local_id, title, body, column_key, rank, estimation_points,
  assignee_user_id, sprint_id, created_at, updated_at, done_at
)
SELECT
  id, project_id, local_id, title, body, column_key, rank, estimation_points,
  assignee_user_id, sprint_id, created_at, updated_at, done_at
FROM todos;

DROP TABLE todos;
ALTER TABLE todos_new RENAME TO todos;

CREATE UNIQUE INDEX IF NOT EXISTS idx_todos_project_local_id
  ON todos(project_id, local_id);

CREATE INDEX IF NOT EXISTS idx_todos_project_updated
  ON todos(project_id, updated_at);

CREATE INDEX IF NOT EXISTS idx_todos_assignee_user_id
  ON todos(assignee_user_id);

CREATE INDEX IF NOT EXISTS idx_todos_sprint_id
  ON todos(sprint_id);

CREATE INDEX IF NOT EXISTS idx_todos_assignee_updated
  ON todos(assignee_user_id, updated_at, id);

CREATE INDEX IF NOT EXISTS idx_todos_project_column_key_rank_id
  ON todos(project_id, column_key, rank, id);

CREATE INDEX IF NOT EXISTS idx_todos_project_column_key_sprint_rank_id
  ON todos(project_id, column_key, sprint_id, rank, id);

CREATE INDEX IF NOT EXISTS idx_todos_assignee_column_key_done_at
  ON todos(assignee_user_id, column_key, done_at);
