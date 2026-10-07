-- Canonical Todo placements on a project's Wall.
--
-- Workflow/content stays on todos. This relation owns only Wall presence and
-- coordinates. todo_id is globally unique, while the composite primary key
-- makes the project scope explicit for reads and backup/restore.

CREATE TABLE IF NOT EXISTS wall_story_placements (
  project_id INTEGER NOT NULL,
  todo_id INTEGER NOT NULL,
  x REAL NOT NULL,
  y REAL NOT NULL,
  version INTEGER NOT NULL DEFAULT 1,
  PRIMARY KEY (project_id, todo_id),
  UNIQUE (todo_id),
  FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
  FOREIGN KEY (todo_id) REFERENCES todos(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_wall_story_placements_project
  ON wall_story_placements(project_id);
