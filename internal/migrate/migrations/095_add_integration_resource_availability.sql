ALTER TABLE integration_resources ADD COLUMN availability TEXT NOT NULL DEFAULT 'available' CHECK (availability IN ('available', 'unavailable'));
CREATE INDEX idx_integration_resources_availability ON integration_resources(connection_id, availability, id);
