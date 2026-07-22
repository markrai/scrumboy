CREATE TABLE IF NOT EXISTS first_password_grants (
  token_hash         TEXT PRIMARY KEY,
  user_id            INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  session_token_hash TEXT NOT NULL REFERENCES sessions(token_hash) ON DELETE CASCADE,
  created_at         INTEGER NOT NULL,
  expires_at         INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_first_password_grants_expires
  ON first_password_grants(expires_at);

CREATE INDEX IF NOT EXISTS idx_first_password_grants_user
  ON first_password_grants(user_id);
