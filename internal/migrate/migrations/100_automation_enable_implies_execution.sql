ALTER TABLE integration_automation_rules
  ADD COLUMN execution_since INTEGER NOT NULL DEFAULT 0;

UPDATE integration_automation_rules
SET execution_since = CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER)
WHERE enabled = 1;

ALTER TABLE integration_automation_actions
  RENAME COLUMN approval_id TO activation_version;

DROP TRIGGER revoke_automation_approval_on_member_delete;
DROP TRIGGER revoke_automation_approval_on_member_demotion;
DROP TRIGGER revoke_automation_approval_on_rule_update;
DROP TRIGGER revoke_automation_approval_on_installation_loss;
DROP TRIGGER revoke_automation_approval_on_repository_loss;

DROP TABLE integration_automation_execution_approvals;
