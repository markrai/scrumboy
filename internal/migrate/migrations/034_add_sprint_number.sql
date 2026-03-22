-- Migration 034: Add per-project stable sprint number for board filter URLs
-- number is 1-based, unique per project, immutable.

ALTER TABLE sprints ADD COLUMN number INTEGER NOT NULL DEFAULT 1;

UPDATE sprints SET number = (
  SELECT COUNT(*) + 1
  FROM sprints s2
  WHERE s2.project_id = sprints.project_id
    AND (
      COALESCE(s2.planned_start_at, 0) < COALESCE(sprints.planned_start_at, 0)
      OR (
        COALESCE(s2.planned_start_at, 0) = COALESCE(sprints.planned_start_at, 0)
        AND s2.id < sprints.id
      )
    )
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_sprints_project_number_lookup ON sprints(project_id, number);
