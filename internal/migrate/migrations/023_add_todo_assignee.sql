-- Migration 023: Add todo assignee tracking and audit events

PRAGMA foreign_keys = ON;

ALTER TABLE todos ADD COLUMN assignee_user_id INTEGER;

CREATE INDEX IF NOT EXISTS idx_todos_assignee_user_id ON todos(assignee_user_id);

CREATE TABLE todo_assignee_events (
  project_id INTEGER NOT NULL,
  todo_id INTEGER NOT NULL,
  actor_user_id INTEGER NOT NULL,
  from_assignee_user_id INTEGER,
  to_assignee_user_id INTEGER,
  reason TEXT,
  created_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_todo_assignee_events_todo_created
  ON todo_assignee_events(todo_id, created_at);

CREATE INDEX IF NOT EXISTS idx_todo_assignee_events_project_created
  ON todo_assignee_events(project_id, created_at);
