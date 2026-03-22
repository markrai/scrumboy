-- Migration 024: Enforce append-only todo assignee audit events

PRAGMA foreign_keys = ON;

CREATE TRIGGER IF NOT EXISTS trg_todo_assignee_events_no_update
BEFORE UPDATE ON todo_assignee_events
BEGIN
  SELECT RAISE(ABORT, 'todo_assignee_events is append-only');
END;

CREATE TRIGGER IF NOT EXISTS trg_todo_assignee_events_no_delete
BEFORE DELETE ON todo_assignee_events
BEGIN
  SELECT RAISE(ABORT, 'todo_assignee_events is append-only');
END;
