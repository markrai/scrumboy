-- Migration 019: Add board-scoped tags for anonymous temporary boards
-- This allows tags on anonymous boards without requiring user ownership.
--
-- Changes:
-- 1. Add project_id column (nullable) for board-scoped tags
-- 2. Add color column (nullable) for board-scoped tag colors
-- 3. Make user_id nullable (requires table rebuild in SQLite)
-- 4. Update unique constraints (conditional indexes)
--
-- Tag types after migration:
-- - User-owned: user_id IS NOT NULL, project_id IS NULL, color IS NULL
-- - Board-scoped: user_id IS NULL, project_id IS NOT NULL, color (nullable)

-- Step 1: Create new tags table with updated schema
CREATE TABLE IF NOT EXISTS tags_new (
  id INTEGER PRIMARY KEY,
  user_id INTEGER REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL CHECK(name = LOWER(name)),
  created_at INTEGER NOT NULL,
  project_id INTEGER REFERENCES projects(id) ON DELETE CASCADE,
  color TEXT
);

-- CRITICAL: Column order must match SELECT order to avoid silent data misalignment
-- tags_new columns: (id, user_id, name, created_at, project_id, color)
-- Existing tags columns: (id, user_id, name, created_at)
-- We map: (id, user_id, name, created_at, NULL, NULL)

-- Step 2: Copy all existing tags as user-owned (user_id NOT NULL, project_id NULL, color NULL)
INSERT INTO tags_new (id, user_id, name, created_at, project_id, color)
SELECT id, user_id, name, created_at, NULL, NULL
FROM tags;

-- Step 3: Drop old tags table
DROP TABLE IF EXISTS tags;

-- Step 4: Rename tags_new to tags
ALTER TABLE tags_new RENAME TO tags;

-- Step 5: Create conditional unique indexes
-- User-owned tags: UNIQUE(user_id, name) WHERE user_id IS NOT NULL
CREATE UNIQUE INDEX IF NOT EXISTS idx_tags_user_name 
ON tags(user_id, name) 
WHERE user_id IS NOT NULL;

-- Board-scoped tags: UNIQUE(project_id, name) WHERE project_id IS NOT NULL AND user_id IS NULL
CREATE UNIQUE INDEX IF NOT EXISTS idx_tags_project_name 
ON tags(project_id, name) 
WHERE project_id IS NOT NULL AND user_id IS NULL;

-- Step 6: Create other indexes
CREATE INDEX IF NOT EXISTS idx_tags_user_id ON tags(user_id);
CREATE INDEX IF NOT EXISTS idx_tags_project_id ON tags(project_id);

-- Step 7: Verify invariants (all existing tags should be user-owned)
-- This is a sanity check - should return 0
-- SELECT COUNT(*) FROM tags WHERE user_id IS NULL; -- Expected: 0
-- SELECT COUNT(*) FROM tags WHERE project_id IS NOT NULL; -- Expected: 0
