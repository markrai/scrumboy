PRAGMA foreign_keys = ON;

-- Update CHECK constraint to include new roles
-- SQLite doesn't support ALTER TABLE ... DROP CONSTRAINT, so we need to:
-- 1. Create new table with updated constraint
-- 2. Copy data
-- 3. Drop old table
-- 4. Rename new table

CREATE TABLE project_members_new (
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role TEXT NOT NULL CHECK (role IN ('owner', 'editor', 'viewer', 'maintainer', 'contributor')),
  created_at INTEGER NOT NULL,
  PRIMARY KEY (project_id, user_id)
);

-- Copy existing data
INSERT INTO project_members_new SELECT * FROM project_members;

-- Drop old table
DROP TABLE project_members;

-- Rename new table
ALTER TABLE project_members_new RENAME TO project_members;

-- Recreate indexes
CREATE INDEX IF NOT EXISTS idx_project_members_project_id
  ON project_members(project_id);

CREATE INDEX IF NOT EXISTS idx_project_members_user_id
  ON project_members(user_id);
