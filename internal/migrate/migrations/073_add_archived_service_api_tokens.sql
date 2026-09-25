-- Migration 073: immutable archive of service API token metadata from deleted users
--
-- When an owner deletes a user, DeleteUser copies each of that user's service-token
-- rows (is_service = 1) here and then lets the api_tokens rows cascade-delete with the
-- user, in one transaction. The archive never stores token_hash, so an archived secret
-- can never authenticate.
--
-- Provenance is snapshotted, not referenced: users.id is a plain INTEGER PRIMARY KEY
-- (reusable after deletion) and both the originating user and the archiving owner may
-- be deleted later, so there are deliberately no foreign keys to users, and the
-- snapshots must not be resolved against current users rows. token_id can be UNIQUE
-- because api_tokens.id is AUTOINCREMENT and never reused. Rows are immutable once
-- written; an owner may purge them (retention), but never rewrite them.

CREATE TABLE archived_service_api_tokens (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  token_id INTEGER NOT NULL UNIQUE,
  name TEXT,
  created_at INTEGER NOT NULL,
  last_used_at INTEGER,
  revoked_at INTEGER NOT NULL,
  revoked_on_archive INTEGER NOT NULL,
  origin_user_id INTEGER NOT NULL,
  origin_user_email TEXT NOT NULL,
  origin_user_name TEXT NOT NULL,
  archived_at INTEGER NOT NULL,
  archived_by_user_id INTEGER NOT NULL,
  archived_by_user_email TEXT NOT NULL
);

CREATE INDEX idx_archived_service_api_tokens_archived_at ON archived_service_api_tokens(archived_at);

CREATE TRIGGER IF NOT EXISTS trg_archived_service_api_tokens_no_update
BEFORE UPDATE ON archived_service_api_tokens
BEGIN
  SELECT RAISE(ABORT, 'archived_service_api_tokens is immutable');
END;
