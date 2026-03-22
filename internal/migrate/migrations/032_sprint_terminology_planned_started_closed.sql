-- Migration 032: sprint terminology alignment (planned vs actual)
-- Rename start/end columns to planned_*, add actual lifecycle timestamps.

ALTER TABLE sprints RENAME COLUMN start_at TO planned_start_at;
ALTER TABLE sprints RENAME COLUMN end_at TO planned_end_at;

ALTER TABLE sprints ADD COLUMN started_at INTEGER NULL;
ALTER TABLE sprints ADD COLUMN closed_at INTEGER NULL;

-- Backfill: historical ACTIVE rows used start_at as effective execution start.
-- After rename, preserve that behavior by copying planned_start_at into started_at for ACTIVE.
UPDATE sprints
SET started_at = planned_start_at
WHERE state = 'ACTIVE' AND started_at IS NULL;
