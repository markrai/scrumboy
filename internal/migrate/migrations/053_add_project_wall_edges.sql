-- Migration: Add edges column to project_walls.
--
-- Edges are simple line connections between two notes on the same wall, drawn
-- by Shift+drag from one note to another (Postbaby parity). They live in the
-- same JSON document layer next to the notes array so the per-project
-- read/modify/write mutex covers both. Edges have no per-record version;
-- they are write-once / delete-once and the document-level `version` is the
-- only realtime fingerprint clients need.
--
-- Stored shape: '[{"id": "e_...", "from": "n_...", "to": "n_..."}]'.

ALTER TABLE project_walls ADD COLUMN edges TEXT NOT NULL DEFAULT '[]';
