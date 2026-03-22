ALTER TABLE projects ADD COLUMN creator_user_id INTEGER NULL;

CREATE INDEX IF NOT EXISTS idx_projects_creator_user_id
ON projects(creator_user_id);
