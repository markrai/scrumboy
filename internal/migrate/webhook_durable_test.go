package migrate

import (
	"context"
	"testing"
)

const durableWebhookMigration = "085_add_durable_webhook_deliveries.sql"

func TestMigration085FreshDatabaseHasDurableWebhookSchema(t *testing.T) {
	database := openMigratedDB(t)
	for _, column := range []string{"delivery_mode", "authorization_generation"} {
		if !columnExists(t, database, "webhooks", column) {
			t.Fatalf("webhooks.%s is missing", column)
		}
	}
	for _, index := range []string{
		"idx_webhook_deliveries_due",
		"idx_webhook_deliveries_expired_leases",
		"idx_webhook_deliveries_webhook_history",
		"idx_webhook_deliveries_expiration",
	} {
		if !indexExists(t, database, index) {
			t.Fatalf("%s is missing", index)
		}
	}
	var mode string
	if err := database.QueryRow(`
SELECT dflt_value FROM pragma_table_info('webhooks') WHERE name = 'delivery_mode'`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "'best_effort'" {
		t.Fatalf("delivery_mode default=%q", mode)
	}
}

func TestMigration085UpgradeKeepsExistingWebhooksBestEffort(t *testing.T) {
	ctx := context.Background()
	database := openRawTestDB(t)
	applyMigrationsBefore(t, database, durableWebhookMigration)
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
INSERT INTO webhooks(id, user_id, project_id, url, events_json, secret, created_at, registered_by_user_id)
VALUES (3, 1, 2, 'https://user:credential@example.com/private?token=secret', '["*"]', 'signing-secret', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if err := applyOne(ctx, database, durableWebhookMigration); err != nil {
		t.Fatalf("apply %s: %v", durableWebhookMigration, err)
	}
	var mode string
	var generation int64
	if err := database.QueryRowContext(ctx, `
SELECT delivery_mode, authorization_generation FROM webhooks WHERE id = 3`).Scan(&mode, &generation); err != nil {
		t.Fatal(err)
	}
	if mode != "best_effort" || generation != 1 {
		t.Fatalf("upgraded webhook mode=%q generation=%d", mode, generation)
	}
	var deliveries int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM webhook_deliveries`).Scan(&deliveries); err != nil {
		t.Fatal(err)
	}
	if deliveries != 0 {
		t.Fatalf("upgrade synthesized %d deliveries", deliveries)
	}
}
