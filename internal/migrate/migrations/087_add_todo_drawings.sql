CREATE TABLE IF NOT EXISTS todo_drawings (
  id TEXT PRIMARY KEY,
  todo_id INTEGER NOT NULL REFERENCES todos(id) ON DELETE CASCADE,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  data TEXT NOT NULL CHECK (length(data) <= 262144),
  version INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_todo_drawings_todo_id ON todo_drawings(todo_id);
CREATE INDEX IF NOT EXISTS idx_todo_drawings_project_id ON todo_drawings(project_id);
