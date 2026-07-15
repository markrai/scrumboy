-- Migration 055: OAuth 2.1 authorization server support for MCP clients (DCR + PKCE)

PRAGMA foreign_keys = ON;

-- Dynamically-registered OAuth clients (RFC 7591). Public clients only (no
-- client_secret): MCP clients like Claude Code register as public clients and
-- use PKCE instead of a confidential client secret.
CREATE TABLE oauth_clients (
  id TEXT PRIMARY KEY,
  client_name TEXT,
  redirect_uri TEXT NOT NULL,
  created_at INTEGER NOT NULL
);

-- Short-lived, single-use authorization codes.
CREATE TABLE oauth_auth_codes (
  code_hash TEXT PRIMARY KEY,
  client_id TEXT NOT NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  redirect_uri TEXT NOT NULL,
  code_challenge TEXT NOT NULL,
  code_challenge_method TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  consumed_at INTEGER
);

CREATE INDEX idx_oauth_auth_codes_expires_at ON oauth_auth_codes(expires_at);

-- Opaque OAuth-issued access tokens, kept separate from api_tokens so
-- OAuth-issued and manually-created tokens are never conflated for
-- revocation/introspection.
CREATE TABLE oauth_access_tokens (
  token_hash TEXT PRIMARY KEY,
  client_id TEXT NOT NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  revoked_at INTEGER
);

CREATE INDEX idx_oauth_access_tokens_user_id ON oauth_access_tokens(user_id);
CREATE INDEX idx_oauth_access_tokens_expires_at ON oauth_access_tokens(expires_at);

-- Opaque refresh tokens, rotated on use (no reuse-detection chain in v1).
CREATE TABLE oauth_refresh_tokens (
  token_hash TEXT PRIMARY KEY,
  client_id TEXT NOT NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  revoked_at INTEGER
);

CREATE INDEX idx_oauth_refresh_tokens_user_id ON oauth_refresh_tokens(user_id);
CREATE INDEX idx_oauth_refresh_tokens_expires_at ON oauth_refresh_tokens(expires_at);
