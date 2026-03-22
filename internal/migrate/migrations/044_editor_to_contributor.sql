PRAGMA foreign_keys = ON;

-- Merge editor role into contributor (Phase 1 permission hardening).
-- Both have rank 2; behavior is identical. Editor is deprecated.
UPDATE project_members SET role = 'contributor' WHERE role = 'editor';
