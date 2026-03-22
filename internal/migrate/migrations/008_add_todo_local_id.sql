-- Add project-scoped, user-facing todo numbers.
-- We keep todos.id as the internal immutable key, and introduce todos.local_id as a per-project counter.

ALTER TABLE todos ADD COLUMN local_id INTEGER;

-- Backfill local_id deterministically per project.
-- NULL-safe ordering (COALESCE created_at to 0) and stable tie-breaker by id.
UPDATE todos AS t
SET local_id = (
  SELECT COUNT(*)
  FROM todos t2
  WHERE t2.project_id = t.project_id
    AND (
      COALESCE(t2.created_at, 0) < COALESCE(t.created_at, 0)
      OR (COALESCE(t2.created_at, 0) = COALESCE(t.created_at, 0) AND t2.id <= t.id)
    )
);

-- Enforce uniqueness per project (note: SQLite UNIQUE allows multiple NULLs, but migration backfills all rows).
CREATE UNIQUE INDEX IF NOT EXISTS idx_todos_project_local_id ON todos(project_id, local_id);

