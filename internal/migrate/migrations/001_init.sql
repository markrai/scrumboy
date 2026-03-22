PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS projects (
  id         INTEGER PRIMARY KEY,
  name       TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

-- status: 0=BACKLOG, 1=NOT_STARTED, 2=IN_PROGRESS, 3=DONE
CREATE TABLE IF NOT EXISTS todos (
  id         INTEGER PRIMARY KEY,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  title      TEXT NOT NULL,
  body       TEXT NOT NULL DEFAULT '',
  status     INTEGER NOT NULL CHECK (status IN (0,1,2,3)),
  rank       INTEGER NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_todos_project_status_rank
  ON todos(project_id, status, rank, id);

CREATE INDEX IF NOT EXISTS idx_todos_project_updated
  ON todos(project_id, updated_at);

CREATE TABLE IF NOT EXISTS tags (
  id         INTEGER PRIMARY KEY,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  UNIQUE(project_id, name)
);

CREATE INDEX IF NOT EXISTS idx_tags_project_name
  ON tags(project_id, name);

CREATE TABLE IF NOT EXISTS todo_tags (
  todo_id INTEGER NOT NULL REFERENCES todos(id) ON DELETE CASCADE,
  tag_id  INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
  PRIMARY KEY (todo_id, tag_id)
);

CREATE INDEX IF NOT EXISTS idx_todo_tags_tag
  ON todo_tags(tag_id, todo_id);

