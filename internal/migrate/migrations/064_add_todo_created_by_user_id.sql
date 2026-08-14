ALTER TABLE todos ADD COLUMN created_by_user_id INTEGER NULL;

CREATE INDEX IF NOT EXISTS idx_todos_created_by_user_id
ON todos(created_by_user_id);
