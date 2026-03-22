-- Burndown snapshots table to track incomplete todos count over time
CREATE TABLE IF NOT EXISTS burndown_snapshots (
  id         INTEGER PRIMARY KEY,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  date       INTEGER NOT NULL, -- Unix timestamp in milliseconds, truncated to start of day
  incomplete_count INTEGER NOT NULL, -- Count of todos not in DONE status
  created_at INTEGER NOT NULL,
  UNIQUE(project_id, date)
);

CREATE INDEX IF NOT EXISTS idx_burndown_project_date
  ON burndown_snapshots(project_id, date DESC);
