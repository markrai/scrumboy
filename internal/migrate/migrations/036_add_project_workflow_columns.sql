CREATE TABLE IF NOT EXISTS project_workflow_columns (
  id INTEGER PRIMARY KEY,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  key TEXT NOT NULL,
  name TEXT NOT NULL,
  position INTEGER NOT NULL,
  is_done INTEGER NOT NULL DEFAULT 0,
  UNIQUE(project_id, key)
);

CREATE INDEX IF NOT EXISTS idx_workflow_columns_project_position
  ON project_workflow_columns(project_id, position);

CREATE INDEX IF NOT EXISTS idx_workflow_columns_project_key
  ON project_workflow_columns(project_id, key);

INSERT INTO project_workflow_columns(project_id, key, name, position, is_done)
SELECT p.id, 'backlog', 'Backlog', 0, 0 FROM projects p WHERE p.import_batch_id IS NULL
UNION ALL
SELECT p.id, 'not_started', 'Not Started', 1, 0 FROM projects p WHERE p.import_batch_id IS NULL
UNION ALL
SELECT p.id, 'doing', 'In Progress', 2, 0 FROM projects p WHERE p.import_batch_id IS NULL
UNION ALL
SELECT p.id, 'testing', 'Testing', 3, 0 FROM projects p WHERE p.import_batch_id IS NULL
UNION ALL
SELECT p.id, 'done', 'Done', 4, 1 FROM projects p WHERE p.import_batch_id IS NULL;
