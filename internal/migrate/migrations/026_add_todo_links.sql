-- Story-to-story links within a project.
-- Composite primary key (project_id, from_local_id, to_local_id); no surrogate id.
CREATE TABLE IF NOT EXISTS todo_links (
  project_id    INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  from_local_id INTEGER NOT NULL,
  to_local_id   INTEGER NOT NULL,
  link_type     TEXT NOT NULL DEFAULT 'relates_to',
  created_at    INTEGER NOT NULL,
  PRIMARY KEY (project_id, from_local_id, to_local_id),
  CHECK(from_local_id != to_local_id)
);

CREATE INDEX IF NOT EXISTS idx_todo_links_project_from
  ON todo_links(project_id, from_local_id);

CREATE INDEX IF NOT EXISTS idx_todo_links_project_to
  ON todo_links(project_id, to_local_id);
