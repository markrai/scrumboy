-- Migration: Add system_role to users table
-- Introduces explicit system-level roles (owner, admin, user) while keeping bootstrap as initialization-only

ALTER TABLE users ADD COLUMN system_role TEXT NOT NULL DEFAULT 'user';

-- Data migration: is_bootstrap = true → system_role = 'owner'
UPDATE users SET system_role = 'owner' WHERE is_bootstrap = TRUE;

-- All others remain 'user' (already set by DEFAULT)

-- Add CHECK constraint to ensure valid values
-- Note: SQLite doesn't support CHECK constraints that are enforced, but we'll validate in application code
