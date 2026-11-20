CREATE TABLE integration_automation_execution_approvals (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  rule_id INTEGER NOT NULL UNIQUE REFERENCES integration_automation_rules(id) ON DELETE CASCADE,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  approved_by_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
  rule_updated_at INTEGER NOT NULL,
  approved_at INTEGER NOT NULL
);

CREATE TABLE integration_automation_actions (
  id INTEGER PRIMARY KEY,
  event_id INTEGER NOT NULL REFERENCES integration_github_automation_events(id) ON DELETE CASCADE,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  rule_id INTEGER NOT NULL REFERENCES integration_automation_rules(id) ON DELETE CASCADE,
  resource_id INTEGER NOT NULL REFERENCES integration_resources(id) ON DELETE CASCADE,
  rule_updated_at INTEGER NOT NULL,
  approval_id INTEGER NOT NULL,
  action_type TEXT NOT NULL CHECK (action_type='notify'),
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','leased','completed','dead','cancelled')),
  attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 8),
  next_attempt_at INTEGER NOT NULL,
  lease_token TEXT,
  lease_until INTEGER,
  error_class TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  completed_at INTEGER,
  UNIQUE(event_id,rule_id)
);

CREATE INDEX idx_integration_automation_actions_due
  ON integration_automation_actions(status,next_attempt_at,id);
CREATE INDEX idx_integration_automation_actions_project
  ON integration_automation_actions(project_id,id DESC);

CREATE TABLE integration_automation_action_audit (
  id INTEGER PRIMARY KEY,
  action_id INTEGER NOT NULL REFERENCES integration_automation_actions(id) ON DELETE CASCADE,
  transition TEXT NOT NULL CHECK (transition IN ('queued','claimed','completed','retry','dead','cancelled')),
  error_class TEXT,
  created_at INTEGER NOT NULL
);
CREATE INDEX idx_integration_automation_action_audit_action
  ON integration_automation_action_audit(action_id,id);
