-- Migration 046: Add audit_events table for general audit trail

PRAGMA foreign_keys = ON;

CREATE TABLE audit_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id INTEGER NOT NULL,
  actor_user_id INTEGER NULL,
  action TEXT NOT NULL,
  target_type TEXT NOT NULL,
  target_id INTEGER,
  metadata TEXT,
  created_at INTEGER NOT NULL
);

CREATE INDEX idx_audit_events_project_created ON audit_events(project_id, created_at);
CREATE INDEX idx_audit_events_action ON audit_events(action);
CREATE INDEX idx_audit_events_actor_created ON audit_events(actor_user_id, created_at);
