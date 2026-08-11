CREATE TABLE IF NOT EXISTS project_priorities (
  id INTEGER PRIMARY KEY,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  key TEXT NOT NULL,
  name TEXT NOT NULL,
  color TEXT NOT NULL DEFAULT '#64748b',
  position INTEGER NOT NULL,
  UNIQUE(project_id, key)
);

CREATE INDEX IF NOT EXISTS idx_project_priorities_project_position
  ON project_priorities(project_id, position);

CREATE INDEX IF NOT EXISTS idx_project_priorities_project_key
  ON project_priorities(project_id, key);

INSERT OR IGNORE INTO project_priorities(project_id, key, name, position, color)
SELECT p.id, 'low', 'Low', 0, '#9CA3AF' FROM projects p WHERE p.import_batch_id IS NULL
UNION ALL
SELECT p.id, 'medium', 'Medium', 1, '#F59E0B' FROM projects p WHERE p.import_batch_id IS NULL
UNION ALL
SELECT p.id, 'high', 'High', 2, '#F97316' FROM projects p WHERE p.import_batch_id IS NULL
UNION ALL
SELECT p.id, 'urgent', 'Urgent', 3, '#EF4444' FROM projects p WHERE p.import_batch_id IS NULL;
