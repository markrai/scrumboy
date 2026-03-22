-- Migration 031: Add optional Scrum Sprints per project
-- sprints: project-scoped, state PLANNED|ACTIVE|CLOSED, at most one ACTIVE per project
-- todos.sprint_id: nullable FK to sprints; NULL = backlog

CREATE TABLE IF NOT EXISTS sprints (
  id         INTEGER PRIMARY KEY,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  start_at   INTEGER NOT NULL,
  end_at     INTEGER NOT NULL,
  state      TEXT NOT NULL CHECK(state IN ('PLANNED','ACTIVE','CLOSED')),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_sprints_project_id ON sprints(project_id);
CREATE INDEX IF NOT EXISTS idx_sprints_project_state ON sprints(project_id, state);
CREATE UNIQUE INDEX IF NOT EXISTS idx_sprints_project_active ON sprints(project_id) WHERE state = 'ACTIVE';

ALTER TABLE todos ADD COLUMN sprint_id INTEGER REFERENCES sprints(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_todos_sprint_id ON todos(sprint_id);
