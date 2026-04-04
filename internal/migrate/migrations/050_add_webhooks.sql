-- Migration 050: Webhook subscriptions for event-driven delivery

PRAGMA foreign_keys = ON;

CREATE TABLE webhooks (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  url TEXT NOT NULL,
  events_json TEXT NOT NULL DEFAULT '[]',
  secret TEXT,
  created_at INTEGER NOT NULL
);

CREATE INDEX idx_webhooks_user_id ON webhooks(user_id);
CREATE INDEX idx_webhooks_project_id ON webhooks(project_id);
