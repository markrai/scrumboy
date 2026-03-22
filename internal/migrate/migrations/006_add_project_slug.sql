-- Add slug to projects for anonymous, shareable board URLs.
-- We keep the change minimal: add column, backfill existing rows, add unique index.

ALTER TABLE projects ADD COLUMN slug TEXT;

-- Backfill existing projects with a short lowercase hex slug (8 chars).
-- Hex is alphanumeric and avoids needing application code during migration.
UPDATE projects
SET slug = lower(hex(randomblob(4)))
WHERE slug IS NULL OR slug = '';

-- Enforce uniqueness.
CREATE UNIQUE INDEX IF NOT EXISTS idx_projects_slug ON projects(slug);

