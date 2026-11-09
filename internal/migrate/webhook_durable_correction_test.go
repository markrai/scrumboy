package migrate

import (
	"context"
	"testing"
)

const durableWebhookCorrectionMigration = "086_bound_durable_webhook_delivery.sql"

func TestMigration086FreshDatabaseHasBoundedDurableSchema(t *testing.T) {
	database := openMigratedDB(t)
	if !columnExists(t, database, "webhook_deliveries", "http_attempt_count") {
		t.Fatal("webhook_deliveries.http_attempt_count is missing")
	}
	for _, index := range []string{
		"idx_webhook_deliveries_webhook_active",
		"idx_webhook_deliveries_terminal_expiration",
	} {
		if !indexExists(t, database, index) {
			t.Fatalf("%s is missing", index)
		}
	}
	var usageRows int
	if err := database.QueryRow(`SELECT COUNT(*) FROM webhook_delivery_usage WHERE singleton = 1`).Scan(&usageRows); err != nil {
		t.Fatal(err)
	}
	if usageRows != 1 {
		t.Fatalf("usage rows=%d", usageRows)
	}
	for _, trigger := range []string{
		"webhook_delivery_usage_insert",
		"webhook_delivery_usage_delete",
		"webhook_delivery_usage_status_update",
		"webhook_delivery_body_limit_insert",
	} {
		var count int
		if err := database.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name = ?`, trigger).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("trigger %s count=%d", trigger, count)
		}
	}
}

func TestMigration086Upgrades085RowsAndPreservesBestEffortSubscriptions(t *testing.T) {
	ctx := context.Background()
	database := openRawTestDB(t)
	applyMigrationsBefore(t, database, durableWebhookCorrectionMigration)
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
INSERT INTO project_members(project_id, user_id, role, created_at)
VALUES (2, 1, 'maintainer', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `
INSERT INTO webhooks(
  id, user_id, project_id, url, events_json, secret, created_at,
  registered_by_user_id, delivery_mode, authorization_generation
) VALUES
  (3, 1, 2, 'https://example.com/best', '["*"]', NULL, 1, 1, 'best_effort', 1),
  (4, 1, 2, 'https://example.com/durable', '["todo.created"]', NULL, 1, 1, 'durable', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `
INSERT INTO webhook_deliveries(
  webhook_id, project_id, event_id, event_type, event_timestamp, body,
  authorization_generation, status, attempt_count, next_attempt_at,
  created_at, expires_at
) VALUES (4, 2, 'existing-event', 'todo.created', 1, X'7B7D', 1, 'pending', 3, 1, 1, 2592000001)`); err != nil {
		t.Fatal(err)
	}
	if err := applyOne(ctx, database, durableWebhookCorrectionMigration); err != nil {
		t.Fatalf("apply %s: %v", durableWebhookCorrectionMigration, err)
	}
	var mode string
	if err := database.QueryRowContext(ctx, `SELECT delivery_mode FROM webhooks WHERE id = 3`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "best_effort" {
		t.Fatalf("legacy mode=%q", mode)
	}
	var leaseCount, httpAttempts int
	var expiresAt int64
	if err := database.QueryRowContext(ctx, `
SELECT attempt_count, http_attempt_count, expires_at
FROM webhook_deliveries WHERE event_id = 'existing-event'`).Scan(&leaseCount, &httpAttempts, &expiresAt); err != nil {
		t.Fatal(err)
	}
	if leaseCount != 3 || httpAttempts != 0 || expiresAt != 86400001 {
		t.Fatalf("lease_count=%d http_attempts=%d expires_at=%d", leaseCount, httpAttempts, expiresAt)
	}
	var activeRows, totalRows int
	if err := database.QueryRowContext(ctx, `
SELECT active_rows, total_rows FROM webhook_delivery_usage WHERE singleton = 1`).Scan(&activeRows, &totalRows); err != nil {
		t.Fatal(err)
	}
	if activeRows != 1 || totalRows != 1 {
		t.Fatalf("active=%d total=%d", activeRows, totalRows)
	}
}
