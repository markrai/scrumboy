package migrate

import (
	"context"
	"database/sql"
	"testing"
)

const webhookProvenanceMigration = "084_add_webhook_registration_provenance.sql"

func TestMigration084FreshSchemaHasWebhookRegistrationProvenance(t *testing.T) {
	database := openMigratedDB(t)
	if !columnExists(t, database, "webhooks", "registered_by_user_id") {
		t.Fatal("webhooks.registered_by_user_id is missing")
	}
	if !indexExists(t, database, "idx_webhooks_registered_by_user_id") {
		t.Fatal("webhook registrar index is missing")
	}
}

func TestMigration084PreservesExistingWebhooksWithUnknownProvenance(t *testing.T) {
	ctx := context.Background()
	database := openRawTestDB(t)
	applyMigrationsBefore(t, database, webhookProvenanceMigration)
	if _, err := database.ExecContext(ctx, `
INSERT INTO users(id, public_id, email, name, created_at, system_role, is_service)
VALUES (1, '11111111-1111-4111-8111-111111111111', 'owner@example.com', 'Owner', 1, 'owner', 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `
INSERT INTO projects(id, name, slug, created_at, updated_at)
VALUES (2, 'Project', 'project', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `
INSERT INTO webhooks(id, user_id, project_id, url, events_json, secret, created_at)
VALUES (3, 1, 2, 'https://example.com/hook', '["*"]', 'signing-secret', 1)`); err != nil {
		t.Fatal(err)
	}
	if err := applyOne(ctx, database, webhookProvenanceMigration); err != nil {
		t.Fatalf("apply %s: %v", webhookProvenanceMigration, err)
	}
	var (
		ownerID      int64
		url          string
		secret       sql.NullString
		registeredBy sql.NullInt64
	)
	if err := database.QueryRowContext(ctx, `
SELECT user_id, url, secret, registered_by_user_id
FROM webhooks
WHERE id = 3`).Scan(&ownerID, &url, &secret, &registeredBy); err != nil {
		t.Fatal(err)
	}
	if ownerID != 1 || url != "https://example.com/hook" || !secret.Valid || registeredBy.Valid {
		t.Fatalf("existing webhook changed: owner=%d url=%q secretPresent=%v registrarPresent=%v", ownerID, url, secret.Valid, registeredBy.Valid)
	}
}

func TestMigration084RegistrarDeletionClearsReferenceAndKeepsWebhook(t *testing.T) {
	ctx := context.Background()
	database := openMigratedDB(t)
	if _, err := database.ExecContext(ctx, `
INSERT INTO users(id, public_id, email, name, created_at, system_role, is_service)
VALUES
  (1, '11111111-1111-4111-8111-111111111111', 'registrar@example.com', 'Registrar', 1, 'user', 0),
  (2, '22222222-2222-4222-8222-222222222222', 'svc-22222222-2222-4222-8222-222222222222@service.invalid', 'Bot', 1, 'user', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `
INSERT INTO projects(id, name, slug, created_at, updated_at)
VALUES (3, 'Project', 'project', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `
INSERT INTO webhooks(id, user_id, project_id, url, events_json, created_at, registered_by_user_id)
VALUES (4, 2, 3, 'https://example.com/hook', '["*"]', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `DELETE FROM users WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	var registeredBy sql.NullInt64
	if err := database.QueryRowContext(ctx, `SELECT registered_by_user_id FROM webhooks WHERE id = 4`).Scan(&registeredBy); err != nil {
		t.Fatal(err)
	}
	if registeredBy.Valid {
		t.Fatalf("registrar reference remained after deletion: %d", registeredBy.Int64)
	}
}
