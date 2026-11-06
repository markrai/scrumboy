-- Migration 081: refuse the legacy project_members 'owner' role for service accounts.
--
-- Migration 080 is immutable and does not contain these triggers. Databases that
-- applied the original migration 080 receive this protection through ordinary
-- forward migration here. Existing human 'owner' memberships are unaffected:
-- both triggers only fire for service accounts holding the 'owner' role.
--
-- The statements below are idempotent. A database that already has these
-- triggers (for example from a development copy of migration 080) applies this
-- migration without error, ending with exactly one copy of each trigger.
-- A later rebuild of project_members must recreate both triggers.
--
-- They do no lookup unless the role is 'owner', which no current store path
-- writes for a service account.

DROP TRIGGER IF EXISTS trg_project_members_reject_service_owner_insert;
DROP TRIGGER IF EXISTS trg_project_members_reject_service_owner_update;

CREATE TRIGGER trg_project_members_reject_service_owner_insert
BEFORE INSERT ON project_members
WHEN NEW.role = 'owner' AND EXISTS (SELECT 1 FROM users WHERE id = NEW.user_id AND is_service <> 0)
BEGIN
  SELECT RAISE(ABORT, 'service identity: project ownership rejected');
END;

CREATE TRIGGER trg_project_members_reject_service_owner_update
BEFORE UPDATE OF role, user_id ON project_members
WHEN NEW.role = 'owner' AND EXISTS (SELECT 1 FROM users WHERE id = NEW.user_id AND is_service <> 0)
BEGIN
  SELECT RAISE(ABORT, 'service identity: project ownership rejected');
END;
