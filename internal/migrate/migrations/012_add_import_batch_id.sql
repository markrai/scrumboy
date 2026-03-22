PRAGMA foreign_keys = ON;

-- Add staging column for atomic Replace All imports
ALTER TABLE projects ADD COLUMN import_batch_id TEXT NULL;

-- Drop old slug index (known name only - variations handled by test assertion)
DROP INDEX IF EXISTS idx_projects_slug;

-- Create scoped unique index: only enforce uniqueness for production rows
-- This allows staging and production to have the same slug simultaneously
CREATE UNIQUE INDEX IF NOT EXISTS idx_projects_slug_production
  ON projects(slug)
  WHERE import_batch_id IS NULL;

-- Index for fast staging cleanup and swap operations
-- Use full index (not partial) for maximum compatibility
CREATE INDEX IF NOT EXISTS idx_projects_import_batch_id 
  ON projects(import_batch_id);
