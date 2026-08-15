-- Immutable historical attribution. This value records the authenticated user
-- present when the todo was created; it is not a project-membership or
-- authorization grant. Existing/anonymous rows remain NULL, and deleting the
-- referenced user clears the attribution without deleting the todo.
ALTER TABLE todos ADD COLUMN created_by_user_id INTEGER NULL
  REFERENCES users(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_todos_created_by_user_id
ON todos(created_by_user_id);
