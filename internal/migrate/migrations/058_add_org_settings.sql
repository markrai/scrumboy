-- Migration 058: Add org_settings table for org-wide admin-configurable settings.
-- Phase 1 of #169: a DB-backed org default for the emailNotifications preference,
-- seeded onto newly created users only -- never applied retroactively.

CREATE TABLE IF NOT EXISTS org_settings (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);
