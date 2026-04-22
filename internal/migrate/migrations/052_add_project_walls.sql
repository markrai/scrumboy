-- Migration: Add project_walls table for Scrumbaby (sticky-note wall) feature.
--
-- Scrumbaby is a durable-projects-only scratchpad that stores the entire wall
-- (all sticky notes) as a single JSON document per project. The row is created
-- lazily on the first durable write; GET returns a synthetic empty document
-- when no row exists.
--
-- `version` is a monotonic document-level counter used as a coarse change
-- fingerprint for realtime clients. Per-note version fields live inside the
-- notes JSON and are the authoritative conflict unit for concurrent edits.

CREATE TABLE IF NOT EXISTS project_walls (
  project_id INTEGER PRIMARY KEY,
  notes TEXT NOT NULL DEFAULT '[]',
  version INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL,
  FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);
