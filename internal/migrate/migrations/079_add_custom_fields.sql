-- Typed custom fields: per-project definitions plus per-todo values.
-- A value row holds exactly one of value_text (text, url) or value_num (number).
CREATE TABLE IF NOT EXISTS project_custom_fields (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  key        TEXT NOT NULL,
  name       TEXT NOT NULL,
  type       TEXT NOT NULL CHECK(type IN ('text', 'url', 'number')),
  position   INTEGER NOT NULL DEFAULT 0,
  UNIQUE(project_id, key)
);

CREATE TABLE IF NOT EXISTS todo_custom_field_values (
  todo_id    INTEGER NOT NULL REFERENCES todos(id) ON DELETE CASCADE,
  field_id   INTEGER NOT NULL REFERENCES project_custom_fields(id) ON DELETE CASCADE,
  value_text TEXT,
  value_num  REAL,
  PRIMARY KEY (todo_id, field_id),
  CHECK((value_text IS NULL) <> (value_num IS NULL))
);

CREATE INDEX IF NOT EXISTS idx_todo_custom_field_values_field
  ON todo_custom_field_values(field_id);
