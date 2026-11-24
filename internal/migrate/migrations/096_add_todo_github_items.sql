CREATE TABLE integration_todo_github_items (
  project_id INTEGER NOT NULL,
  todo_id INTEGER NOT NULL REFERENCES todos(id) ON DELETE CASCADE,
  resource_id INTEGER NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('issue','pr')),
  number INTEGER NOT NULL CHECK (number > 0),
  title TEXT NOT NULL CHECK (length(title) BETWEEN 1 AND 500),
  state TEXT NOT NULL CHECK (state IN ('open','closed')),
  draft INTEGER NOT NULL DEFAULT 0 CHECK (draft IN (0,1)),
  merged INTEGER NOT NULL DEFAULT 0 CHECK (merged IN (0,1)),
  url TEXT NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (todo_id, resource_id, kind, number),
  FOREIGN KEY (project_id, resource_id) REFERENCES integration_project_resources(project_id, resource_id) ON DELETE CASCADE
);
CREATE INDEX idx_github_items_by_project ON integration_todo_github_items(project_id, todo_id);
