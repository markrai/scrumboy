ALTER TABLE calendar_sources
  ADD COLUMN host_kind TEXT NOT NULL DEFAULT 'other'
  CHECK(host_kind IN ('google', 'apple', 'other'));
