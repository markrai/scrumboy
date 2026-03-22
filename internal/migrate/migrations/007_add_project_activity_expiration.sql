-- Add activity tracking and expiration for anonymous board mode.
-- last_activity_at: tracks last read/write activity (throttled updates)
-- expires_at: NULL for full mode projects, set to now+14days for anonymous mode projects
--
-- Note: SQLite doesn't support non-constant defaults (like unixepoch('now')) when adding
-- columns to existing tables. We add last_activity_at as nullable, backfill it, and rely
-- on application logic to always set it (which it does in CreateProject and CreateAnonymousBoard).

ALTER TABLE projects ADD COLUMN last_activity_at INTEGER;
ALTER TABLE projects ADD COLUMN expires_at INTEGER NULL;

-- Backfill existing projects: set last_activity_at = updated_at, expires_at = NULL (full mode)
UPDATE projects
SET last_activity_at = updated_at,
    expires_at = NULL
WHERE last_activity_at IS NULL;

-- Index for cleanup queries (expires_at IS NOT NULL AND expires_at < now())
CREATE INDEX IF NOT EXISTS idx_projects_expires_at ON projects(expires_at) WHERE expires_at IS NOT NULL;
