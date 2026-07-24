-- Migration 059: Add provenance to user_preferences.
-- Records how each preference row was written so a future bulk-apply can safely
-- distinguish org-seeded rows from user-customized rows.
-- Existing rows default to 'legacy' (unknown writer) and must never be
-- auto-updated by bulk-apply. Values are application-defined; unknown values
-- are treated conservatively (ineligible for automatic bulk updates). No SQLite
-- CHECK constraint, so adding a future provenance value stays cheap.

ALTER TABLE user_preferences
  ADD COLUMN provenance TEXT NOT NULL DEFAULT 'legacy';
