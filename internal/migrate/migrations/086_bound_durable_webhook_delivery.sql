CREATE TABLE webhook_deliveries_v086 (
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
  http_attempt_count INTEGER NOT NULL DEFAULT 0
    CHECK (http_attempt_count >= 0 AND http_attempt_count <= 8),
  next_attempt_at INTEGER NOT NULL,
  lease_token TEXT,
  lease_expires_at INTEGER,
  last_error_class TEXT CHECK (
    last_error_class IS NULL OR last_error_class IN (
      'network', 'http_3xx', 'http_4xx', 'http_5xx', 'http_other',
      'ssrf', 'redirect', 'unauthorized', 'attempts_exhausted', 'expired', 'internal'
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

INSERT INTO webhook_deliveries_v086(
  id, webhook_id, project_id, event_id, event_type, event_timestamp, body,
  authorization_generation, status, attempt_count, http_attempt_count,
  next_attempt_at, lease_token, lease_expires_at, last_error_class,
  last_http_status, created_at, completed_at, expires_at
)
SELECT
  id, webhook_id, project_id, event_id, event_type, event_timestamp, body,
  authorization_generation, status, attempt_count, 0,
  next_attempt_at, lease_token, lease_expires_at, last_error_class,
  last_http_status, created_at, completed_at,
  CASE
    WHEN status = 'delivered' THEN COALESCE(completed_at, created_at) + 604800000
    WHEN status IN ('failed', 'skipped') THEN COALESCE(completed_at, created_at) + 2592000000
    WHEN expires_at < created_at + 86400000 THEN expires_at
    ELSE created_at + 86400000
  END
FROM webhook_deliveries;

DROP TABLE webhook_deliveries;
ALTER TABLE webhook_deliveries_v086 RENAME TO webhook_deliveries;

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

CREATE INDEX idx_webhook_deliveries_webhook_active
ON webhook_deliveries(webhook_id, id)
WHERE status IN ('pending', 'leased');

CREATE INDEX idx_webhook_deliveries_terminal_expiration
ON webhook_deliveries(expires_at, id)
WHERE status IN ('delivered', 'failed', 'skipped');

CREATE TABLE webhook_delivery_usage (
  singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
  active_rows INTEGER NOT NULL CHECK (active_rows >= 0),
  active_payload_bytes INTEGER NOT NULL CHECK (active_payload_bytes >= 0),
  total_rows INTEGER NOT NULL CHECK (total_rows >= 0),
  total_payload_bytes INTEGER NOT NULL CHECK (total_payload_bytes >= 0)
);

INSERT INTO webhook_delivery_usage(
  singleton, active_rows, active_payload_bytes, total_rows, total_payload_bytes
)
SELECT
  1,
  COALESCE(SUM(CASE WHEN status IN ('pending', 'leased') THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN status IN ('pending', 'leased') THEN length(body) ELSE 0 END), 0),
  COUNT(*),
  COALESCE(SUM(length(body)), 0)
FROM webhook_deliveries;

CREATE TRIGGER webhook_delivery_usage_insert
AFTER INSERT ON webhook_deliveries
BEGIN
  UPDATE webhook_delivery_usage
  SET active_rows = active_rows + CASE WHEN NEW.status IN ('pending', 'leased') THEN 1 ELSE 0 END,
      active_payload_bytes = active_payload_bytes + CASE WHEN NEW.status IN ('pending', 'leased') THEN length(NEW.body) ELSE 0 END,
      total_rows = total_rows + 1,
      total_payload_bytes = total_payload_bytes + length(NEW.body)
  WHERE singleton = 1;
END;

CREATE TRIGGER webhook_delivery_usage_delete
AFTER DELETE ON webhook_deliveries
BEGIN
  UPDATE webhook_delivery_usage
  SET active_rows = active_rows - CASE WHEN OLD.status IN ('pending', 'leased') THEN 1 ELSE 0 END,
      active_payload_bytes = active_payload_bytes - CASE WHEN OLD.status IN ('pending', 'leased') THEN length(OLD.body) ELSE 0 END,
      total_rows = total_rows - 1,
      total_payload_bytes = total_payload_bytes - length(OLD.body)
  WHERE singleton = 1;
END;

CREATE TRIGGER webhook_delivery_usage_status_update
AFTER UPDATE OF status ON webhook_deliveries
WHEN OLD.status <> NEW.status
BEGIN
  UPDATE webhook_delivery_usage
  SET active_rows = active_rows
        + CASE WHEN NEW.status IN ('pending', 'leased') THEN 1 ELSE 0 END
        - CASE WHEN OLD.status IN ('pending', 'leased') THEN 1 ELSE 0 END,
      active_payload_bytes = active_payload_bytes
        + CASE WHEN NEW.status IN ('pending', 'leased') THEN length(NEW.body) ELSE 0 END
        - CASE WHEN OLD.status IN ('pending', 'leased') THEN length(OLD.body) ELSE 0 END
  WHERE singleton = 1;
END;

CREATE TRIGGER webhook_delivery_body_limit_insert
BEFORE INSERT ON webhook_deliveries
WHEN length(NEW.body) > 65536
BEGIN
  SELECT RAISE(ABORT, 'durable webhook payload exceeds 64 KiB');
END;

CREATE TRIGGER webhook_delivery_body_limit_update
BEFORE UPDATE OF body ON webhook_deliveries
WHEN NEW.body IS NOT OLD.body
BEGIN
  SELECT RAISE(ABORT, 'durable webhook payload is immutable');
END;
