ALTER TABLE api_tokens ADD COLUMN created_by_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE api_tokens ADD COLUMN created_by_user_public_id TEXT;

CREATE INDEX idx_api_tokens_created_by_user_id ON api_tokens(created_by_user_id);

ALTER TABLE archived_service_api_tokens ADD COLUMN origin_user_is_service INTEGER
  CHECK (origin_user_is_service IN (0, 1));
ALTER TABLE archived_service_api_tokens ADD COLUMN created_by_user_id INTEGER;
ALTER TABLE archived_service_api_tokens ADD COLUMN created_by_user_public_id TEXT;
