-- Migration 022: Add performance indexes
-- This migration adds indexes to improve query performance on tag lookups.

PRAGMA foreign_keys = ON;

-- Add index for tag name lookups (used in tag filtering EXISTS subquery)
-- Existing indexes cover (project_id, name) and (user_id, name) but not name alone
-- This helps queries like: WHERE g.name = ? in the tag filter EXISTS clause
CREATE INDEX IF NOT EXISTS idx_tags_name ON tags(name);
