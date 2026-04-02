-- Migration 048: API access tokens for MCP (opaque bearer secrets; DB stores hash only)

PRAGMA foreign_keys = ON;

CREATE TABLE api_tokens (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash TEXT NOT NULL UNIQUE,
  name TEXT,
  created_at INTEGER NOT NULL,
  last_used_at INTEGER,
  revoked_at INTEGER
);

CREATE INDEX idx_api_tokens_user_id ON api_tokens(user_id);
