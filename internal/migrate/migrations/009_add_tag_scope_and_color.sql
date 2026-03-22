-- Add scope and color to tags table, make project_id nullable
-- Rebuild table to handle NOT NULL constraint change
-- Deduplicate tag names when promoting to GLOBAL scope

-- Step 1: Create new tags_new table with nullable project_id and new columns
CREATE TABLE tags_new (
  id         INTEGER PRIMARY KEY,
  scope      TEXT NOT NULL DEFAULT 'GLOBAL',
  project_id INTEGER REFERENCES projects(id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  color      TEXT,
  created_at INTEGER NOT NULL
);

-- Step 2: Deduplicate tags and migrate to tags_new
-- Strategy: For each unique tag name, choose canonical tag (lowest id)
-- Choose canonical color: prefer from tag_colors, else first non-null tags.color, else NULL
-- Repoint all todo_tags.tag_id references from duplicates → canonical tag

-- Step 2a: Create mapping table (canonical tag_id per name)
CREATE TEMP TABLE tag_dedup AS
SELECT 
  name,
  MIN(id) AS canonical_id,
  COALESCE(
    (SELECT color FROM tag_colors WHERE tag_colors.name = tags.name LIMIT 1),
    (SELECT color FROM tags t2 WHERE t2.name = tags.name AND t2.color IS NOT NULL LIMIT 1),
    NULL
  ) AS canonical_color,
  MIN(created_at) AS canonical_created_at
FROM tags
GROUP BY name;

-- Step 2b: Repoint todo_tags to canonical tags
UPDATE todo_tags
SET tag_id = (
  SELECT canonical_id 
  FROM tag_dedup 
  WHERE tag_dedup.name = (SELECT name FROM tags WHERE tags.id = todo_tags.tag_id)
)
WHERE tag_id NOT IN (SELECT canonical_id FROM tag_dedup);

-- Step 2c: Delete duplicate tags (keep only canonical)
DELETE FROM tags
WHERE id NOT IN (SELECT canonical_id FROM tag_dedup);

-- Step 2d: Insert into tags_new (all become GLOBAL, project_id=NULL)
INSERT INTO tags_new (id, scope, project_id, name, color, created_at)
SELECT 
  canonical_id,
  'GLOBAL',
  NULL,
  name,
  canonical_color,
  canonical_created_at
FROM tag_dedup;

-- Step 3: Drop old table and rename
DROP TABLE tags;
ALTER TABLE tags_new RENAME TO tags;

-- Step 4: Create partial unique indexes
CREATE UNIQUE INDEX idx_tags_global_name 
  ON tags(name) 
  WHERE scope='GLOBAL' AND project_id IS NULL;

CREATE UNIQUE INDEX idx_tags_project_name 
  ON tags(project_id, name) 
  WHERE scope='PROJECT' AND project_id IS NOT NULL;

-- Step 5: Drop old indexes (if they exist)
DROP INDEX IF EXISTS idx_tags_project_name;
