CREATE TABLE integration_connections (
  id INTEGER PRIMARY KEY,
  provider TEXT NOT NULL CHECK (length(provider) BETWEEN 1 AND 32),
  external_account_id TEXT NOT NULL CHECK (length(external_account_id) BETWEEN 1 AND 256),
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  UNIQUE (provider, external_account_id)
);

CREATE TABLE integration_resources (
  id INTEGER PRIMARY KEY,
  connection_id INTEGER NOT NULL REFERENCES integration_connections(id) ON DELETE CASCADE,
  kind TEXT NOT NULL CHECK (length(kind) BETWEEN 1 AND 32),
  external_id TEXT NOT NULL CHECK (length(external_id) BETWEEN 1 AND 256),
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
  url TEXT NOT NULL CHECK (length(url) BETWEEN 1 AND 2048),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  UNIQUE (connection_id, kind, external_id)
);

CREATE INDEX idx_integration_resources_connection ON integration_resources(connection_id, id);

CREATE TABLE integration_project_resources (
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  resource_id INTEGER NOT NULL REFERENCES integration_resources(id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (project_id, resource_id)
);

CREATE INDEX idx_integration_project_resources_resource ON integration_project_resources(resource_id, project_id);
