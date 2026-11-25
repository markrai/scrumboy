CREATE TABLE integration_github_automation_events (
  id INTEGER PRIMARY KEY,
  delivery_id TEXT NOT NULL UNIQUE CHECK (length(delivery_id) BETWEEN 8 AND 128),
  installation_id TEXT NOT NULL CHECK (length(installation_id) BETWEEN 1 AND 20),
  repository_external_id TEXT NOT NULL CHECK (length(repository_external_id) BETWEEN 1 AND 20),
  item_number INTEGER NOT NULL CHECK (item_number BETWEEN 1 AND 2147483647),
  event_type TEXT NOT NULL CHECK (event_type IN (
    'issue.opened','issue.closed',
    'pull_request.opened','pull_request.closed','pull_request.merged'
  )),
  payload_sha256 TEXT NOT NULL CHECK (length(payload_sha256)=64),
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','leased','completed','dead')),
  attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 8),
  next_attempt_at INTEGER NOT NULL,
  lease_token TEXT,
  lease_until INTEGER,
  error_class TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  completed_at INTEGER
);

CREATE INDEX idx_integration_github_automation_due
  ON integration_github_automation_events(status,next_attempt_at,id);
CREATE INDEX idx_integration_github_automation_installation
  ON integration_github_automation_events(installation_id,repository_external_id,id);

CREATE TABLE integration_github_automation_matches (
  id INTEGER PRIMARY KEY,
  event_id INTEGER NOT NULL REFERENCES integration_github_automation_events(id) ON DELETE CASCADE,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  rule_id INTEGER NOT NULL REFERENCES integration_automation_rules(id) ON DELETE CASCADE,
  resource_id INTEGER NOT NULL REFERENCES integration_resources(id) ON DELETE CASCADE,
  rule_updated_at INTEGER NOT NULL,
  action_type TEXT NOT NULL CHECK (action_type='notify'),
  matched_at INTEGER NOT NULL,
  UNIQUE(event_id,rule_id)
);

CREATE INDEX idx_integration_github_automation_matches_project
  ON integration_github_automation_matches(project_id,id DESC);
