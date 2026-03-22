-- Migration 041: Deduplicate tags where different stored names canonicalize to the same value
-- e.g. "make space" and "make-space" both canonicalize to "make-space"
-- Merges duplicate rows into one keeper per (user_id|project_id, canonical_name)

-- Canonical name approximation (matches Go CanonicalizeTag for common cases)
-- TRIM, LOWER, collapse spaces to hyphen, trim hyphens

-- Step 1: All tags with canonical name
CREATE TEMP TABLE tag_canonical AS
SELECT id, user_id, project_id, name,
  TRIM(REPLACE(REPLACE(REPLACE(REPLACE(LOWER(TRIM(name)), '  ', ' '), '  ', ' '), '  ', ' '), ' ', '-'), '-') AS canonical_name
FROM tags;

-- Step 2: User-owned duplicate groups - pick keeper (prefer exact match, else MIN(id))
CREATE TEMP TABLE user_dup_keepers AS
SELECT user_id, canonical_name,
  COALESCE(
    (SELECT id FROM tag_canonical t2
     WHERE t2.user_id = tc.user_id AND t2.canonical_name = tc.canonical_name
       AND t2.name = tc.canonical_name
     ORDER BY id LIMIT 1),
    (SELECT MIN(id) FROM tag_canonical t2
     WHERE t2.user_id = tc.user_id AND t2.canonical_name = tc.canonical_name)
  ) AS keeper_id
FROM (
  SELECT user_id, canonical_name
  FROM tag_canonical
  WHERE user_id IS NOT NULL
  GROUP BY user_id, canonical_name
  HAVING COUNT(*) > 1
) tc;

-- Step 3: Board-scoped duplicate groups - pick keeper
CREATE TEMP TABLE board_dup_keepers AS
SELECT project_id, canonical_name,
  COALESCE(
    (SELECT id FROM tag_canonical t2
     WHERE t2.project_id = tc.project_id AND t2.canonical_name = tc.canonical_name
       AND t2.name = tc.canonical_name
     ORDER BY id LIMIT 1),
    (SELECT MIN(id) FROM tag_canonical t2
     WHERE t2.project_id = tc.project_id AND t2.canonical_name = tc.canonical_name)
  ) AS keeper_id
FROM (
  SELECT project_id, canonical_name
  FROM tag_canonical
  WHERE user_id IS NULL AND project_id IS NOT NULL
  GROUP BY project_id, canonical_name
  HAVING COUNT(*) > 1
) tc;

-- Step 4: Map redundant tag_id -> keeper_id (all tags that will be merged away)
CREATE TEMP TABLE tag_merge_map (
  tag_id INTEGER PRIMARY KEY,
  keeper_id INTEGER NOT NULL
);

INSERT INTO tag_merge_map (tag_id, keeper_id)
SELECT t.id, k.keeper_id
FROM tag_canonical t
JOIN user_dup_keepers k ON t.user_id = k.user_id AND t.canonical_name = k.canonical_name AND t.id != k.keeper_id
WHERE t.user_id IS NOT NULL;

INSERT INTO tag_merge_map (tag_id, keeper_id)
SELECT t.id, k.keeper_id
FROM tag_canonical t
JOIN board_dup_keepers k ON t.project_id = k.project_id AND t.canonical_name = k.canonical_name AND t.id != k.keeper_id
WHERE t.user_id IS NULL AND t.project_id IS NOT NULL;

-- Step 5: Update keeper names to canonical form (idempotent)
UPDATE tags SET name = (SELECT k.canonical_name FROM user_dup_keepers k WHERE k.keeper_id = tags.id)
WHERE id IN (SELECT keeper_id FROM user_dup_keepers);

UPDATE tags SET name = (SELECT k.canonical_name FROM board_dup_keepers k WHERE k.keeper_id = tags.id)
WHERE id IN (SELECT keeper_id FROM board_dup_keepers);

-- Step 6: Repoint todo_tags from redundant to keeper
UPDATE todo_tags
SET tag_id = (SELECT keeper_id FROM tag_merge_map WHERE tag_id = todo_tags.tag_id)
WHERE tag_id IN (SELECT tag_id FROM tag_merge_map);

-- Step 7: Deduplicate todo_tags (keep one per todo_id, tag_id)
DELETE FROM todo_tags
WHERE rowid NOT IN (
  SELECT MIN(rowid) FROM todo_tags GROUP BY todo_id, tag_id
);

-- Step 8: Ensure project_tags has keeper linked for projects that had redundant tags
INSERT OR IGNORE INTO project_tags (project_id, tag_id, created_at)
SELECT pt.project_id, tmm.keeper_id, MIN(pt.created_at)
FROM project_tags pt
JOIN tag_merge_map tmm ON pt.tag_id = tmm.tag_id
GROUP BY pt.project_id, tmm.keeper_id;

-- Step 9: Remove project_tags rows for redundant tags
DELETE FROM project_tags
WHERE tag_id IN (SELECT tag_id FROM tag_merge_map);

-- Step 10: Remove user_tag_colors for redundant tags (keeper keeps its colors)
DELETE FROM user_tag_colors
WHERE tag_id IN (SELECT tag_id FROM tag_merge_map);

-- Step 11: Delete redundant tags
DELETE FROM tags
WHERE id IN (SELECT tag_id FROM tag_merge_map);
