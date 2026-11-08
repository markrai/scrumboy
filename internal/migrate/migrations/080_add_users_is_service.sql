-- Migration 080: first-class service-account identities on users
--
-- A service account is a users row that can never sign in interactively. users.is_service is the
-- discriminator; it is unrelated to api_tokens.is_service, which marks an automation token.
-- docs/draft/service-accounts-architecture.md records why each mechanism here exists.
--
--   users_service_identity CHECK: a service row is role 'user', not bootstrap, has no password, no
--   2FA state, and a placeholder email. It also rejects any is_service value other than 0 or 1.
--   Existing rows pass because the new column defaults to 0.
--
--   Immutability trigger: an account can never change type in either direction.
--
--   Credential triggers: no interactive credential row can be created for a service account.
--   api_tokens is deliberately absent: it is the machine-authentication channel.
--
--   Project triggers: a service account can never be a project's owner or creator, and cannot
--   inherit a deleted user's boards or cards by reusing their id. users.id can be reused, and
--   projects.creator_user_id and todos.assignee_user_id have no foreign key.
--
-- A later rebuild of a table below must recreate its triggers, and a new table holding an
-- interactive credential must add its own. Rebuilding users must also drop and recreate every
-- trigger here, because they refer to it. TestMigration080ProtectionsSurviveChain fails if any of
-- this goes missing, and TestEveryUserReferenceIsClassified fails until a new column referring to a
-- user has been classified.

ALTER TABLE users ADD COLUMN is_service BOOLEAN NOT NULL DEFAULT FALSE
  CONSTRAINT users_service_identity CHECK (
    is_service = 0 OR (
      is_service = 1
      AND system_role = 'user'
      AND is_bootstrap = 0
      AND password_hash IS NULL
      AND two_factor_enabled = 0
      AND two_factor_secret_enc IS NULL
      AND email GLOB 'svc-*@service.invalid'
    )
  );

CREATE TRIGGER trg_users_is_service_immutable
BEFORE UPDATE OF is_service ON users
WHEN NEW.is_service IS NOT OLD.is_service
BEGIN
  SELECT RAISE(ABORT, 'service identity: users.is_service is immutable');
END;

CREATE TRIGGER trg_sessions_reject_service_insert
BEFORE INSERT ON sessions
WHEN EXISTS (SELECT 1 FROM users WHERE id = NEW.user_id AND is_service <> 0)
BEGIN
  SELECT RAISE(ABORT, 'service identity: interactive credential rejected');
END;

CREATE TRIGGER trg_user_oidc_identities_reject_service_insert
BEFORE INSERT ON user_oidc_identities
WHEN EXISTS (SELECT 1 FROM users WHERE id = NEW.user_id AND is_service <> 0)
BEGIN
  SELECT RAISE(ABORT, 'service identity: interactive credential rejected');
END;

CREATE TRIGGER trg_login_2fa_pending_reject_service_insert
BEFORE INSERT ON login_2fa_pending
WHEN EXISTS (SELECT 1 FROM users WHERE id = NEW.user_id AND is_service <> 0)
BEGIN
  SELECT RAISE(ABORT, 'service identity: interactive credential rejected');
END;

CREATE TRIGGER trg_two_factor_enrollments_reject_service_insert
BEFORE INSERT ON two_factor_enrollments
WHEN EXISTS (SELECT 1 FROM users WHERE id = NEW.user_id AND is_service <> 0)
BEGIN
  SELECT RAISE(ABORT, 'service identity: interactive credential rejected');
END;

CREATE TRIGGER trg_user_recovery_codes_reject_service_insert
BEFORE INSERT ON user_recovery_codes
WHEN EXISTS (SELECT 1 FROM users WHERE id = NEW.user_id AND is_service <> 0)
BEGIN
  SELECT RAISE(ABORT, 'service identity: interactive credential rejected');
END;

CREATE TRIGGER trg_first_password_grants_reject_service_insert
BEFORE INSERT ON first_password_grants
WHEN EXISTS (SELECT 1 FROM users WHERE id = NEW.user_id AND is_service <> 0)
BEGIN
  SELECT RAISE(ABORT, 'service identity: interactive credential rejected');
END;

CREATE TRIGGER trg_oauth_auth_codes_reject_service_insert
BEFORE INSERT ON oauth_auth_codes
WHEN EXISTS (SELECT 1 FROM users WHERE id = NEW.user_id AND is_service <> 0)
BEGIN
  SELECT RAISE(ABORT, 'service identity: interactive credential rejected');
END;

CREATE TRIGGER trg_oauth_access_tokens_reject_service_insert
BEFORE INSERT ON oauth_access_tokens
WHEN EXISTS (SELECT 1 FROM users WHERE id = NEW.user_id AND is_service <> 0)
BEGIN
  SELECT RAISE(ABORT, 'service identity: interactive credential rejected');
END;

CREATE TRIGGER trg_oauth_refresh_tokens_reject_service_insert
BEFORE INSERT ON oauth_refresh_tokens
WHEN EXISTS (SELECT 1 FROM users WHERE id = NEW.user_id AND is_service <> 0)
BEGIN
  SELECT RAISE(ABORT, 'service identity: interactive credential rejected');
END;

CREATE TRIGGER trg_mobile_oidc_handoff_grants_reject_service_insert
BEFORE INSERT ON mobile_oidc_handoff_grants
WHEN EXISTS (SELECT 1 FROM users WHERE id = NEW.user_id AND is_service <> 0)
BEGIN
  SELECT RAISE(ABORT, 'service identity: interactive credential rejected');
END;

CREATE TRIGGER trg_projects_reject_service_owner_insert
BEFORE INSERT ON projects
WHEN EXISTS (SELECT 1 FROM users WHERE id IN (NEW.owner_user_id, NEW.creator_user_id) AND is_service <> 0)
BEGIN
  SELECT RAISE(ABORT, 'service identity: project ownership rejected');
END;

CREATE TRIGGER trg_projects_reject_service_owner_update
BEFORE UPDATE OF owner_user_id, creator_user_id ON projects
WHEN EXISTS (SELECT 1 FROM users WHERE id IN (NEW.owner_user_id, NEW.creator_user_id) AND is_service <> 0)
BEGIN
  SELECT RAISE(ABORT, 'service identity: project ownership rejected');
END;

CREATE TRIGGER trg_users_service_reject_inherited_references
AFTER INSERT ON users
WHEN NEW.is_service <> 0 AND EXISTS (
  SELECT 1 FROM projects WHERE owner_user_id = NEW.id OR creator_user_id = NEW.id
  UNION ALL
  SELECT 1 FROM todos WHERE assignee_user_id = NEW.id
)
BEGIN
  SELECT RAISE(ABORT, 'service identity: inherited references rejected');
END;
