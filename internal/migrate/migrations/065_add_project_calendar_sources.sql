ALTER TABLE projects ADD COLUMN agenda_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE projects ADD COLUMN agenda_timezone TEXT NOT NULL DEFAULT 'UTC';

CREATE TABLE IF NOT EXISTS calendar_sources (
  id INTEGER PRIMARY KEY,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  type TEXT NOT NULL CHECK(type = 'ics_feed'),
  name TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  secret_enc TEXT NOT NULL,
  url_hash TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  UNIQUE(project_id, url_hash)
);

CREATE INDEX IF NOT EXISTS idx_calendar_sources_project
  ON calendar_sources(project_id);
