-- Migration: Add is_bootstrap flag to users table
-- First user (bootstrap user) can create other users

ALTER TABLE users ADD COLUMN is_bootstrap BOOLEAN NOT NULL DEFAULT FALSE;

-- Set first user as bootstrap (if users table has any rows)
UPDATE users SET is_bootstrap = TRUE WHERE id = (SELECT MIN(id) FROM users);
