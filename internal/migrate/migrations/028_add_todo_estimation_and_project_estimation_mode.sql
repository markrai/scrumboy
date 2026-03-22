-- Story point estimation (v1: Modified Fibonacci only).
-- todos.estimation_points: NULL = unestimated; allowed values enforced in app (1,2,3,5,8,13,20,40).
ALTER TABLE todos ADD COLUMN estimation_points INTEGER;

-- projects.estimation_mode: set at insert only, never updated (no API/admin/import override).
-- NOT NULL + DEFAULT ensure existing rows get 'MODIFIED_FIBONACCI' (SQLite backfills on ALTER).
-- Literal below must match store.EstimationModeModifiedFibonacci (single source of truth in Go).
ALTER TABLE projects
ADD COLUMN estimation_mode TEXT NOT NULL DEFAULT 'MODIFIED_FIBONACCI'
CHECK(estimation_mode IN ('MODIFIED_FIBONACCI'));
