PRAGMA foreign_keys = ON;

-- Users: 2FA flag and encrypted TOTP secret. Invariant: enabled=1 implies secret_enc IS NOT NULL; disabling clears both.
ALTER TABLE users ADD COLUMN two_factor_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN two_factor_secret_enc TEXT NULL;

-- Short-lived tokens for "password OK, pending 2FA". One active per user (latest wins). token_hash UNIQUE prevents duplicates.
CREATE TABLE IF NOT EXISTS login_2fa_pending (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash TEXT NOT NULL UNIQUE,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  attempt_count INTEGER NOT NULL DEFAULT 0,
  last_attempt_at INTEGER NULL
);
CREATE INDEX IF NOT EXISTS idx_login_2fa_pending_token ON login_2fa_pending(token_hash);
CREATE INDEX IF NOT EXISTS idx_login_2fa_pending_expires ON login_2fa_pending(expires_at);
CREATE INDEX IF NOT EXISTS idx_login_2fa_pending_user ON login_2fa_pending(user_id);

-- Pending 2FA enrollment: one active per user (latest wins). token_hash UNIQUE. Index on user_id for DELETE BY user_id.
CREATE TABLE IF NOT EXISTS two_factor_enrollments (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash TEXT NOT NULL UNIQUE,
  secret_enc TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  attempt_count INTEGER NOT NULL DEFAULT 0,
  last_attempt_at INTEGER NULL
);
CREATE INDEX IF NOT EXISTS idx_two_factor_enrollments_token ON two_factor_enrollments(token_hash);
CREATE INDEX IF NOT EXISTS idx_two_factor_enrollments_expires ON two_factor_enrollments(expires_at);
CREATE INDEX IF NOT EXISTS idx_two_factor_enrollments_user ON two_factor_enrollments(user_id);

-- Recovery codes: bcrypt hashed; Crockford base32 (no I/L/O/U), 8 chars split 4-4 (xxxx-xxxx); one-time use. Timestamps: INTEGER unix ms.
CREATE TABLE IF NOT EXISTS user_recovery_codes (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  code_hash TEXT NOT NULL,
  used_at INTEGER NULL,
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_user_recovery_codes_user ON user_recovery_codes(user_id);
