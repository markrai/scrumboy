CREATE TABLE IF NOT EXISTS calendar_feed_snapshots (
  source_id INTEGER PRIMARY KEY REFERENCES calendar_sources(id) ON DELETE CASCADE,
  etag TEXT,
  last_modified TEXT,
  fetched_at INTEGER NOT NULL,
  status TEXT NOT NULL CHECK(status IN ('ok', 'error')),
  error TEXT,
  events_json TEXT NOT NULL DEFAULT '[]'
);
