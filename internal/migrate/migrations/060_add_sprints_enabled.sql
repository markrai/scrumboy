-- Migration 060: Add per-project toggle to disable sprints.
-- Boards that don't use sprints can turn this off to hide all sprint-related
-- UI (Settings > Sprints tab, sprint field on todos, bulk-edit, board chips).
-- Existing and newly created projects default to enabled (1) to preserve
-- current behavior.
ALTER TABLE projects
ADD COLUMN sprints_enabled INTEGER NOT NULL DEFAULT 1
CHECK(sprints_enabled IN (0, 1));
