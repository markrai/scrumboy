-- Migration 085: opt-in durable webhook delivery for todo.created

ALTER TABLE webhooks
ADD COLUMN delivery_mode TEXT NOT NULL DEFAULT 'best_effort'
CHECK (delivery_mode IN ('best_effort', 'durable'));

-- Pending work captures this generation. Losing delivery eligibility advances
-- the subscription generation, so work admitted before a removal/downgrade can
-- never become eligible again after the owner is re-added.
ALTER TABLE webhooks
ADD COLUMN authorization_generation INTEGER NOT NULL DEFAULT 1
CHECK (authorization_generation > 0);

CREATE TABLE webhook_deliveries (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  webhook_id INTEGER NOT NULL REFERENCES webhooks(id) ON DELETE CASCADE,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  event_id TEXT NOT NULL,
  event_type TEXT NOT NULL,
  event_timestamp INTEGER NOT NULL,
  body BLOB NOT NULL,
  authorization_generation INTEGER NOT NULL CHECK (authorization_generation > 0),
  status TEXT NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending', 'leased', 'delivered', 'failed', 'skipped')),
  attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
  next_attempt_at INTEGER NOT NULL,
  lease_token TEXT,
  lease_expires_at INTEGER,
  last_error_class TEXT CHECK (
    last_error_class IS NULL OR last_error_class IN (
      'network', 'http_4xx', 'http_5xx', 'http_other',
      'ssrf', 'redirect', 'unauthorized', 'attempts_exhausted', 'internal'
    )
  ),
  last_http_status INTEGER CHECK (
    last_http_status IS NULL OR (last_http_status >= 100 AND last_http_status <= 599)
  ),
  created_at INTEGER NOT NULL,
  completed_at INTEGER,
  expires_at INTEGER NOT NULL,
  UNIQUE(webhook_id, event_id),
  CHECK (length(event_id) > 0),
  CHECK (event_type = 'todo.created'),
  CHECK (length(body) > 0),
  CHECK (
    (status = 'leased' AND lease_token IS NOT NULL AND lease_expires_at IS NOT NULL) OR
    (status <> 'leased' AND lease_token IS NULL AND lease_expires_at IS NULL)
  ),
  CHECK (
    (status IN ('delivered', 'failed', 'skipped') AND completed_at IS NOT NULL) OR
    (status IN ('pending', 'leased') AND completed_at IS NULL)
  )
);

CREATE INDEX idx_webhook_deliveries_due
ON webhook_deliveries(status, next_attempt_at, id)
WHERE status = 'pending';

CREATE INDEX idx_webhook_deliveries_expired_leases
ON webhook_deliveries(status, lease_expires_at, id)
WHERE status = 'leased';

CREATE INDEX idx_webhook_deliveries_webhook_history
ON webhook_deliveries(webhook_id, created_at DESC, id DESC);

CREATE INDEX idx_webhook_deliveries_expiration
ON webhook_deliveries(expires_at, id);

CREATE TRIGGER webhooks_authorization_generation_member_delete
AFTER DELETE ON project_members
WHEN (
  (EXISTS (SELECT 1 FROM users u WHERE u.id = OLD.user_id AND u.is_service = 0)
    AND OLD.role IN ('maintainer', 'owner'))
  OR
  (EXISTS (SELECT 1 FROM users u WHERE u.id = OLD.user_id AND u.is_service = 1)
    AND OLD.role IN ('viewer', 'contributor', 'maintainer'))
)
BEGIN
  UPDATE webhooks
  SET authorization_generation = authorization_generation + 1
  WHERE project_id = OLD.project_id AND user_id = OLD.user_id;
END;

CREATE TRIGGER webhooks_authorization_generation_member_update
AFTER UPDATE OF project_id, user_id, role ON project_members
WHEN (
  OLD.project_id <> NEW.project_id OR OLD.user_id <> NEW.user_id OR
  (
    EXISTS (SELECT 1 FROM users u WHERE u.id = OLD.user_id AND u.is_service = 0)
    AND OLD.role IN ('maintainer', 'owner')
    AND NOT (NEW.project_id = OLD.project_id AND NEW.user_id = OLD.user_id AND NEW.role IN ('maintainer', 'owner'))
  ) OR
  (
    EXISTS (SELECT 1 FROM users u WHERE u.id = OLD.user_id AND u.is_service = 1)
    AND OLD.role IN ('viewer', 'contributor', 'maintainer')
    AND NOT (NEW.project_id = OLD.project_id AND NEW.user_id = OLD.user_id AND NEW.role IN ('viewer', 'contributor', 'maintainer'))
  )
)
BEGIN
  UPDATE webhooks
  SET authorization_generation = authorization_generation + 1
  WHERE project_id = OLD.project_id AND user_id = OLD.user_id;
END;
