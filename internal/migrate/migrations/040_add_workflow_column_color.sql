ALTER TABLE project_workflow_columns
  ADD COLUMN color TEXT NOT NULL DEFAULT '#64748b';

UPDATE project_workflow_columns
SET color = CASE key
  WHEN 'backlog' THEN '#9CA3AF'
  WHEN 'not_started' THEN '#F59E0B'
  WHEN 'doing' THEN '#10B981'
  WHEN 'testing' THEN '#3B82F6'
  WHEN 'done' THEN '#EF4444'
  ELSE '#64748b'
END;
