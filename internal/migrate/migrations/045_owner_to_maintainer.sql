PRAGMA foreign_keys = ON;

-- Phase 2: Migrate owner → maintainer. Maintainer becomes highest project role.
-- No permission loss. Idempotent.
UPDATE project_members SET role = 'maintainer' WHERE role = 'owner';
