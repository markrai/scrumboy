ALTER TABLE webhooks
ADD COLUMN registered_by_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL;

CREATE INDEX idx_webhooks_registered_by_user_id
ON webhooks(registered_by_user_id);
