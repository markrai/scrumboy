CREATE TABLE integration_automation_rules (
  id INTEGER PRIMARY KEY,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  created_by_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
  resource_id INTEGER NOT NULL,
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
  event_type TEXT NOT NULL CHECK (event_type IN (
    'issue.opened', 'issue.closed',
    'pull_request.opened', 'pull_request.closed', 'pull_request.merged'
  )),
  action_type TEXT NOT NULL DEFAULT 'notify' CHECK (action_type = 'notify'),
  enabled INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
  execution_approved_at INTEGER DEFAULT NULL CHECK (execution_approved_at IS NULL),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  FOREIGN KEY (project_id, resource_id) REFERENCES integration_project_resources(project_id, resource_id) ON DELETE CASCADE
);

CREATE INDEX idx_integration_automation_rules_project
  ON integration_automation_rules(project_id, id);
CREATE INDEX idx_integration_automation_rules_resource
  ON integration_automation_rules(project_id, resource_id, event_type);
