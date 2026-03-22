PRAGMA foreign_keys = ON;

-- Users (single-user now, future multi-user ready)
CREATE TABLE IF NOT EXISTS users (
  id            INTEGER PRIMARY KEY,
  email         TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  created_at    INTEGER NOT NULL
);

-- Session storage (cookie holds raw token; DB stores hash)
CREATE TABLE IF NOT EXISTS sessions (
  id           INTEGER PRIMARY KEY,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash   TEXT NOT NULL UNIQUE,
  created_at   INTEGER NOT NULL,
  expires_at   INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_sessions_user_id
  ON sessions(user_id);

CREATE INDEX IF NOT EXISTS idx_sessions_expires_at
  ON sessions(expires_at);

-- Project ownership (NULL means unowned; durable projects will be assigned to first user on first login)
ALTER TABLE projects ADD COLUMN owner_user_id INTEGER NULL REFERENCES users(id);

