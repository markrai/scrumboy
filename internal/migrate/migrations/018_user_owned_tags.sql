-- Step 1: Create temporary migration mapping table
CREATE TABLE IF NOT EXISTS tag_migration_map (
  old_tag_id INTEGER NOT NULL,
  new_user_id INTEGER NOT NULL,
  new_tag_id INTEGER NOT NULL,
  PRIMARY KEY (old_tag_id, new_user_id)
);

-- Step 2: Create new tags table structure (without scope, project_id, color)
CREATE TABLE IF NOT EXISTS tags_new (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  created_at INTEGER NOT NULL
);

-- Step 3: Create user_tag_colors table for per-viewer color preferences
CREATE TABLE IF NOT EXISTS user_tag_colors (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  tag_id INTEGER NOT NULL REFERENCES tags_new(id) ON DELETE CASCADE,
  color TEXT NOT NULL CHECK (color <> ''),
  PRIMARY KEY (user_id, tag_id)
);

-- Step 4: Create project_tags junction table (project-wide tag set)
CREATE TABLE IF NOT EXISTS project_tags (
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  tag_id INTEGER NOT NULL REFERENCES tags_new(id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now') * 1000),
  PRIMARY KEY (project_id, tag_id)
);

-- Step 5: Migration logic - assign ownership based on usage
-- For PROJECT-scoped tags: assign to project owner
INSERT INTO tag_migration_map (old_tag_id, new_user_id, new_tag_id)
SELECT 
  t.id AS old_tag_id,
  COALESCE(p.owner_user_id, (SELECT id FROM users WHERE is_bootstrap = 1 LIMIT 1)) AS new_user_id,
  0 AS new_tag_id -- Will be updated after tags_new is populated
FROM tags t
JOIN projects p ON t.project_id = p.id
WHERE t.scope = 'PROJECT' AND t.project_id IS NOT NULL;

-- For GLOBAL tags: assign to each project owner where tag is used
INSERT INTO tag_migration_map (old_tag_id, new_user_id, new_tag_id)
SELECT DISTINCT
  t.id AS old_tag_id,
  COALESCE(p.owner_user_id, (SELECT id FROM users WHERE is_bootstrap = 1 LIMIT 1)) AS new_user_id,
  0 AS new_tag_id -- Will be updated after tags_new is populated
FROM tags t
JOIN todo_tags tt ON t.id = tt.tag_id
JOIN todos todo ON tt.todo_id = todo.id
JOIN projects p ON todo.project_id = p.id
WHERE t.scope = 'GLOBAL' AND t.project_id IS NULL
  AND NOT EXISTS (
    SELECT 1 FROM tag_migration_map tmm
    WHERE tmm.old_tag_id = t.id AND tmm.new_user_id = COALESCE(p.owner_user_id, (SELECT id FROM users WHERE is_bootstrap = 1 LIMIT 1))
  );

-- Step 6: Populate tags_new with normalized names and deduplicate
-- Strategy: For each (user_id, normalized_name), keep the tag with lowest old_tag_id
-- Use a temporary table to identify canonical tags
CREATE TEMP TABLE IF NOT EXISTS canonical_tags AS
SELECT 
  tmm.new_user_id,
  LOWER(TRIM(t.name)) AS normalized_name,
  MIN(t.id) AS canonical_old_tag_id,
  MIN(t.created_at) AS created_at
FROM tag_migration_map tmm
JOIN tags t ON tmm.old_tag_id = t.id
GROUP BY tmm.new_user_id, LOWER(TRIM(t.name));

-- Insert canonical tags into tags_new (let SQLite auto-increment IDs)
-- We'll update the mapping after insertion
INSERT INTO tags_new (user_id, name, created_at)
SELECT 
  new_user_id,
  normalized_name,
  created_at
FROM canonical_tags
ORDER BY new_user_id, normalized_name;

-- Update tag_migration_map for canonical tags
UPDATE tag_migration_map
SET new_tag_id = (
  SELECT tn.id
  FROM tags_new tn
  JOIN canonical_tags ct ON tn.user_id = ct.new_user_id AND tn.name = ct.normalized_name
  WHERE ct.canonical_old_tag_id = tag_migration_map.old_tag_id
  LIMIT 1
)
WHERE EXISTS (
  SELECT 1
  FROM canonical_tags ct
  WHERE ct.canonical_old_tag_id = tag_migration_map.old_tag_id
);

-- For non-canonical tags, map them to the canonical tag for the same user/name
UPDATE tag_migration_map
SET new_tag_id = (
  SELECT tn.id
  FROM tags_new tn
  JOIN canonical_tags ct ON tn.user_id = ct.new_user_id AND tn.name = ct.normalized_name
  WHERE ct.new_user_id = tag_migration_map.new_user_id
    AND ct.normalized_name = (
      SELECT LOWER(TRIM(t.name))
      FROM tags t
      WHERE t.id = tag_migration_map.old_tag_id
    )
  LIMIT 1
)
WHERE new_tag_id = 0
  AND EXISTS (
    SELECT 1
    FROM tags t
    JOIN canonical_tags ct ON ct.new_user_id = tag_migration_map.new_user_id
      AND ct.normalized_name = LOWER(TRIM(t.name))
    WHERE t.id = tag_migration_map.old_tag_id
      AND ct.canonical_old_tag_id != t.id
  );

DROP TABLE IF EXISTS canonical_tags;

-- Step 7 is now handled in Step 6 above

-- Step 8: Repoint todo_tags using migration map
-- For each todo_tags row, find the project owner and map to new tag_id
-- IMPORTANT: use OR REPLACE so that if multiple old tag_ids collapse to the same (todo_id, new_tag_id),
-- the existing PRIMARY KEY(todo_id, tag_id) does not cause the migration to fail.
-- This deterministically keeps one row and drops the conflicting duplicate at the DB boundary.
UPDATE OR REPLACE todo_tags
SET tag_id = (
  SELECT tmm.new_tag_id
  FROM tag_migration_map tmm
  JOIN todos todo ON todo.id = todo_tags.todo_id
  JOIN projects p ON todo.project_id = p.id
  WHERE tmm.old_tag_id = todo_tags.tag_id
    AND tmm.new_user_id = COALESCE(p.owner_user_id, (SELECT id FROM users WHERE is_bootstrap = 1 LIMIT 1))
  LIMIT 1
)
WHERE EXISTS (
  SELECT 1
  FROM tag_migration_map tmm
  WHERE tmm.old_tag_id = todo_tags.tag_id
);

-- Step 9: Build project_tags from actual usage (all tags used in project's todos)
-- NOTE: This only READS from todo_tags, does not write to it
INSERT INTO project_tags (project_id, tag_id, created_at)
SELECT DISTINCT
  todo.project_id,
  tt.tag_id,
  MIN(COALESCE(todo.created_at, tags_new.created_at)) AS created_at
FROM todo_tags tt
JOIN todos todo ON tt.todo_id = todo.id
JOIN tags_new ON tt.tag_id = tags_new.id
GROUP BY todo.project_id, tt.tag_id
ON CONFLICT (project_id, tag_id) DO NOTHING;

-- Step 10: Migrate colors to user_tag_colors (only for tag owner)
INSERT INTO user_tag_colors (user_id, tag_id, color)
SELECT 
  tags_new.user_id,
  tags_new.id,
  t.color
FROM tags_new
JOIN tag_migration_map tmm ON tags_new.id = tmm.new_tag_id
JOIN tags t ON tmm.old_tag_id = t.id
WHERE t.color IS NOT NULL AND t.color <> ''
ON CONFLICT (user_id, tag_id) DO NOTHING;

-- Step 11: FINAL cleanup - remove duplicate todo-tag pairs
-- This MUST be the last mutation of todo_tags before the UNIQUE constraint
-- Nothing may touch todo_tags after this point
DELETE FROM todo_tags
WHERE rowid NOT IN (
  SELECT MIN(rowid)
  FROM todo_tags
  GROUP BY todo_id, tag_id
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_todo_tags_unique
ON todo_tags(todo_id, tag_id);

-- Step 13: Drop old tags table and rename tags_new to tags
-- NOTE: This does not touch todo_tags, so it's safe after deduplication
DROP TABLE IF EXISTS tags;
ALTER TABLE tags_new RENAME TO tags;

-- Step 14: Add constraints and indexes
CREATE UNIQUE INDEX IF NOT EXISTS idx_tags_user_name ON tags(user_id, name);
CREATE INDEX IF NOT EXISTS idx_tags_user_id ON tags(user_id);
CREATE INDEX IF NOT EXISTS idx_project_tags_project ON project_tags(project_id);
CREATE INDEX IF NOT EXISTS idx_project_tags_tag ON project_tags(tag_id);
CREATE INDEX IF NOT EXISTS idx_user_tag_colors_user_tag ON user_tag_colors(user_id, tag_id);

-- Step 15: Add CHECK constraint for lowercase names (enforced at application level, but add for safety)
-- Note: SQLite doesn't support CHECK constraints with functions well, so we rely on application-level enforcement

-- Step 16: Clean up temporary migration table
DROP TABLE IF EXISTS tag_migration_map;
