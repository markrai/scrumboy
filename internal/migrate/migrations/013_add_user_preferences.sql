-- Migration: Add user preferences table for storing per-user settings
-- This table stores key-value preferences for each user (theme, tag colors, UI preferences, etc.)

CREATE TABLE IF NOT EXISTS user_preferences (
  user_id INTEGER NOT NULL,
  key TEXT NOT NULL,
  value TEXT NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (user_id, key),
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_user_preferences_user_id ON user_preferences(user_id);
