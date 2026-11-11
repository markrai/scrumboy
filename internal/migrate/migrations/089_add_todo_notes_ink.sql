CREATE TABLE IF NOT EXISTS todo_notes_ink (
  todo_id INTEGER PRIMARY KEY REFERENCES todos(id) ON DELETE CASCADE,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  data TEXT NOT NULL CHECK (length(data) <= 524288),
  version INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_todo_notes_ink_project_id ON todo_notes_ink(project_id);
