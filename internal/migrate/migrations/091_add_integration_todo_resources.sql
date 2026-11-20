CREATE TABLE integration_todo_resources (
  project_id INTEGER NOT NULL,
  todo_id INTEGER NOT NULL REFERENCES todos(id) ON DELETE CASCADE,
  resource_id INTEGER NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (todo_id, resource_id),
  FOREIGN KEY (project_id, resource_id) REFERENCES integration_project_resources(project_id, resource_id) ON DELETE CASCADE
);

CREATE INDEX idx_integration_todo_resources_project ON integration_todo_resources(project_id, todo_id);
CREATE INDEX idx_integration_todo_resources_resource ON integration_todo_resources(resource_id, project_id);
