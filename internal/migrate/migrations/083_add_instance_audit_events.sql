CREATE TABLE instance_audit_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  actor_user_id INTEGER NOT NULL,
  actor_public_id TEXT NOT NULL,
  action TEXT NOT NULL,
  target_type TEXT NOT NULL,
  target_user_id INTEGER,
  target_public_id TEXT NOT NULL,
  token_id INTEGER,
  metadata TEXT,
  created_at INTEGER NOT NULL
);

CREATE INDEX idx_instance_audit_events_created_at ON instance_audit_events(created_at);
CREATE INDEX idx_instance_audit_events_action_created_at ON instance_audit_events(action, created_at);

CREATE TRIGGER trg_instance_audit_events_no_update
BEFORE UPDATE ON instance_audit_events
BEGIN
  SELECT RAISE(ABORT, 'instance_audit_events is append-only');
END;

CREATE TRIGGER trg_instance_audit_events_no_delete
BEFORE DELETE ON instance_audit_events
BEGIN
  SELECT RAISE(ABORT, 'instance_audit_events is append-only');
END;
