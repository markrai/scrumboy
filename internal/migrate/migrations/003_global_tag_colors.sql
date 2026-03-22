-- Remove color column from tags table (per-project colors)
-- Create new global tag_colors table
CREATE TABLE IF NOT EXISTS tag_colors (
  name  TEXT PRIMARY KEY,
  color TEXT NOT NULL
);

-- Migrate existing colors to global table (if any exist)
INSERT OR IGNORE INTO tag_colors (name, color)
SELECT DISTINCT name, color
FROM tags
WHERE color IS NOT NULL AND color != '';

-- Remove color column from tags table
-- Note: SQLite doesn't support DROP COLUMN directly, so we'll need to recreate the table
-- For now, we'll just stop using it and use the new tag_colors table
