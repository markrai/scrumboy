CREATE TABLE integration_github_inbound_events (
  id INTEGER PRIMARY KEY,
  delivery_id TEXT NOT NULL UNIQUE CHECK (length(delivery_id) BETWEEN 1 AND 128),
  installation_id TEXT NOT NULL CHECK (length(installation_id) BETWEEN 1 AND 256),
  event_type TEXT NOT NULL CHECK (event_type IN ('installation', 'installation_repositories', 'repository')),
  event_action TEXT NOT NULL CHECK (length(event_action) BETWEEN 1 AND 64),
  payload_sha256 TEXT NOT NULL CHECK (length(payload_sha256) = 64),
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'leased', 'completed', 'dead')),
  attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 8),
  next_attempt_at INTEGER NOT NULL,
  lease_token TEXT,
  lease_until INTEGER,
  error_class TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  completed_at INTEGER
);

CREATE INDEX idx_integration_github_inbound_due ON integration_github_inbound_events(status, next_attempt_at, id);
CREATE INDEX idx_integration_github_inbound_installation ON integration_github_inbound_events(installation_id, id);
