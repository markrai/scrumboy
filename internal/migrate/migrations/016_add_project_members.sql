PRAGMA foreign_keys = ON;

-- Project memberships table for role-based access control
CREATE TABLE IF NOT EXISTS project_members (
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role TEXT NOT NULL CHECK (role IN ('owner', 'editor', 'viewer')),
  created_at INTEGER NOT NULL,
  PRIMARY KEY (project_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_project_members_project_id
  ON project_members(project_id);

CREATE INDEX IF NOT EXISTS idx_project_members_user_id
  ON project_members(user_id);

-- Data migration: Create owner memberships from existing owner_user_id
-- This ensures all existing owners have explicit memberships
INSERT INTO project_members (project_id, user_id, role, created_at)
SELECT id, owner_user_id, 'owner', updated_at
FROM projects
WHERE owner_user_id IS NOT NULL
  AND NOT EXISTS (
    SELECT 1 FROM project_members pm
    WHERE pm.project_id = projects.id AND pm.user_id = projects.owner_user_id
  );
