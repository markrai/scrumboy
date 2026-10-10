-- Migration 076: permanent, never-reused public id for users
--
-- users.id is a plain INTEGER PRIMARY KEY (no AUTOINCREMENT), so SQLite may hand a deleted
-- user's id to a later user. That is fine for internal joins, but an external integration that
-- stores a Scrumboy user id to link an account (for example a chat bot) could end up pointing
-- at a different person after a delete. public_id is a random UUID, assigned once and never
-- changed, that such integrations can key on instead; users.id stays the internal key.
--
-- Existing users are backfilled here. New users get one from the insert trigger, so no insert
-- path (password, bootstrap, OIDC, or any future one) can leave it NULL. A second trigger makes
-- it immutable once set.

ALTER TABLE users ADD COLUMN public_id TEXT;

UPDATE users
SET public_id = lower(
  hex(randomblob(4)) || '-' || hex(randomblob(2)) || '-4' || substr(hex(randomblob(2)), 2) || '-' ||
  substr('89ab', 1 + (abs(random()) % 4), 1) || substr(hex(randomblob(2)), 2) || '-' || hex(randomblob(6))
)
WHERE public_id IS NULL;

CREATE UNIQUE INDEX idx_users_public_id ON users(public_id);

CREATE TRIGGER IF NOT EXISTS trg_users_public_id_default
AFTER INSERT ON users
WHEN NEW.public_id IS NULL
BEGIN
  UPDATE users
  SET public_id = lower(
    hex(randomblob(4)) || '-' || hex(randomblob(2)) || '-4' || substr(hex(randomblob(2)), 2) || '-' ||
    substr('89ab', 1 + (abs(random()) % 4), 1) || substr(hex(randomblob(2)), 2) || '-' || hex(randomblob(6))
  )
  WHERE id = NEW.id;
END;

CREATE TRIGGER IF NOT EXISTS trg_users_public_id_immutable
BEFORE UPDATE OF public_id ON users
WHEN OLD.public_id IS NOT NULL AND NEW.public_id IS NOT OLD.public_id
BEGIN
  SELECT RAISE(ABORT, 'users.public_id is immutable');
END;
