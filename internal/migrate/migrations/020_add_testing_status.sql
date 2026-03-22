-- Migration: Add Testing status (3) between In Progress (2) and Done (4)
-- Existing statuses: 0=Backlog, 1=Not Started, 2=In Progress, 3=DONE (now 4)
-- New status: 3=Testing
-- 
-- Strategy: Table rebuild + data transform in one pass
-- This guarantees no invalid values exist at any point

-- Step 1: Create new todos table with updated constraint
-- Note: local_id was added in migration 008 and is nullable in schema but backfilled
-- All existing rows should have local_id set, but we preserve it as-is (nullable)
CREATE TABLE todos_new (
  id         INTEGER PRIMARY KEY,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  local_id   INTEGER,  -- Added in migration 008, nullable but backfilled
  title      TEXT NOT NULL,
  body       TEXT NOT NULL DEFAULT '',
  status     INTEGER NOT NULL CHECK (status IN (0,1,2,3,4)),
  rank       INTEGER NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

-- Step 2: Copy all data with status transformation
-- Transform: status=3 (old Done) → status=4 (new Done)
-- All other statuses remain unchanged
-- CRITICAL: rank, local_id, created_at, updated_at, and all other columns are preserved exactly
-- The index idx_todos_project_status_rank depends on rank ordering being unchanged
-- Column order matches table definition to prevent misalignment
INSERT INTO todos_new (id, project_id, local_id, title, body, status, rank, created_at, updated_at)
SELECT
  id,
  project_id,
  local_id,  -- Preserved unchanged (added in migration 008)
  title,
  body,
  CASE WHEN status = 3 THEN 4 ELSE status END AS status,
  rank,  -- Preserved unchanged - critical for index ordering
  created_at,
  updated_at
FROM todos;

-- Step 3: Drop old table and rename
DROP TABLE todos;
ALTER TABLE todos_new RENAME TO todos;

-- Step 4: Recreate indexes
-- Note: rank values are preserved from original table, so index ordering is maintained
CREATE INDEX IF NOT EXISTS idx_todos_project_status_rank
  ON todos(project_id, status, rank, id);

CREATE INDEX IF NOT EXISTS idx_todos_project_updated
  ON todos(project_id, updated_at);

-- Recreate local_id unique index (added in migration 008)
CREATE UNIQUE INDEX IF NOT EXISTS idx_todos_project_local_id
  ON todos(project_id, local_id);

-- Step 5: Recreate any triggers if they exist
-- (No triggers exist on todos table, so nothing to recreate)
