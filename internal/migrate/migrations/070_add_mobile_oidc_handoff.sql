PRAGMA foreign_keys = ON;

-- Trusted marker for an OIDC authorization initiated by the packaged Android
-- app. Only the provider state hash and the app's independent S256 challenge
-- are persisted; neither raw proof is stored.
CREATE TABLE mobile_oidc_flows (
  state_hash          TEXT PRIMARY KEY,
  handoff_challenge   TEXT NOT NULL,
  challenge_method    TEXT NOT NULL CHECK (challenge_method = 'S256'),
  return_to           TEXT NOT NULL,
  created_at          INTEGER NOT NULL,
  expires_at          INTEGER NOT NULL,
  callback_consumed_at INTEGER
);

CREATE INDEX idx_mobile_oidc_flows_expires_at
  ON mobile_oidc_flows(expires_at);

-- Short-lived, single-use proof used only to establish the ordinary
-- scrumboy_session cookie in the native OkHttp cookie jar.
CREATE TABLE mobile_oidc_handoff_grants (
  code_hash          TEXT PRIMARY KEY,
  user_id            INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  flow_state_hash    TEXT NOT NULL REFERENCES mobile_oidc_flows(state_hash) ON DELETE CASCADE,
  handoff_challenge  TEXT NOT NULL,
  return_to          TEXT NOT NULL,
  created_at         INTEGER NOT NULL,
  expires_at         INTEGER NOT NULL,
  consumed_at        INTEGER
);

CREATE INDEX idx_mobile_oidc_handoff_grants_expires_at
  ON mobile_oidc_handoff_grants(expires_at);

CREATE INDEX idx_mobile_oidc_handoff_grants_user_id
  ON mobile_oidc_handoff_grants(user_id);
