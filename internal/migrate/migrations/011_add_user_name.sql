-- Add a display name for users.
-- We use a NOT NULL column with a constant DEFAULT so existing rows are backfilled safely in SQLite.
-- Existing bootstrap user will get "Mark Rai" as a starting value.

ALTER TABLE users ADD COLUMN name TEXT NOT NULL DEFAULT 'Mark Rai';

