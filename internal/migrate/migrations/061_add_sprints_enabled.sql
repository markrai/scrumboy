-- Migration 061: Add the reversible per-project sprint capability.
-- Existing and newly created projects default to enabled (1) to preserve
-- current behavior.
ALTER TABLE projects
ADD COLUMN sprints_enabled INTEGER NOT NULL DEFAULT 1
CHECK(sprints_enabled IN (0, 1));
