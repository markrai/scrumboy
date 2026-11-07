package migrate

import (
	"context"
	"database/sql"
	"testing"
)

func TestMigration082PreservesExistingTokensAndUnknownArchiveProvenance(t *testing.T) {
	ctx := context.Background()
	database := openRawTestDB(t)
	applyMigrationsBefore(t, database, "082_add_service_token_provenance.sql")
	if _, err := database.ExecContext(ctx, `
INSERT INTO users(id, public_id, email, name, created_at, system_role, is_service)
VALUES
  (1, '11111111-1111-4111-8111-111111111111', 'owner@example.com', 'Owner', 1, 'owner', 0),
  (2, '22222222-2222-4222-8222-222222222222', 'human@example.com', 'Human', 1, 'user', 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO api_tokens(id, user_id, token_hash, name, created_at, is_service) VALUES (7, 2, 'existing-hash', 'existing', 2, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `
INSERT INTO archived_service_api_tokens(
  token_id, name, created_at, revoked_at, revoked_on_archive,
  origin_user_id, origin_user_email, origin_user_name,
  archived_at, archived_by_user_id, archived_by_user_email
) VALUES (6, 'legacy', 1, 2, 1, 2, 'human@example.com', 'Human', 2, 1, 'owner@example.com')`); err != nil {
		t.Fatal(err)
	}
	if err := applyOne(ctx, database, "082_add_service_token_provenance.sql"); err != nil {
		t.Fatalf("apply migration 082: %v", err)
	}

	var (
		userID          int64
		tokenHash       string
		isService       bool
		createdByID     sql.NullInt64
		createdByPublic sql.NullString
	)
	if err := database.QueryRowContext(ctx, `SELECT user_id, token_hash, is_service, created_by_user_id, created_by_user_public_id FROM api_tokens WHERE id = 7`).Scan(
		&userID, &tokenHash, &isService, &createdByID, &createdByPublic,
	); err != nil {
		t.Fatal(err)
	}
	if userID != 2 || tokenHash != "existing-hash" || !isService || createdByID.Valid || createdByPublic.Valid {
		t.Fatalf("existing token changed: user=%d hashMatches=%v service=%v creatorIDPresent=%v creatorPublicIDPresent=%v", userID, tokenHash == "existing-hash", isService, createdByID.Valid, createdByPublic.Valid)
	}
	var originService sql.NullBool
	if err := database.QueryRowContext(ctx, `SELECT origin_user_is_service FROM archived_service_api_tokens WHERE token_id = 6`).Scan(&originService); err != nil {
		t.Fatal(err)
	}
	if originService.Valid {
		t.Fatalf("legacy archive provenance = %v, want unknown", originService)
	}
}

func TestMigration082CreatorDeletionClearsReusableIDButKeepsSnapshot(t *testing.T) {
	ctx := context.Background()
	database := openRawTestDB(t)
	if err := Apply(ctx, database); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `
INSERT INTO users(id, public_id, email, name, created_at, system_role, is_service)
VALUES
  (1, '11111111-1111-4111-8111-111111111111', 'creator@example.com', 'Creator', 1, 'user', 0),
  (2, '22222222-2222-4222-8222-222222222222', 'svc-22222222-2222-4222-8222-222222222222@service.invalid', 'Bot', 1, 'user', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `
INSERT INTO api_tokens(user_id, token_hash, created_at, is_service, created_by_user_id, created_by_user_public_id)
VALUES (2, 'hash', 1, 1, 1, '11111111-1111-4111-8111-111111111111')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `DELETE FROM users WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	var (
		ownerID         int64
		createdByID     sql.NullInt64
		createdByPublic sql.NullString
	)
	if err := database.QueryRowContext(ctx, `SELECT user_id, created_by_user_id, created_by_user_public_id FROM api_tokens WHERE token_hash = 'hash'`).Scan(
		&ownerID, &createdByID, &createdByPublic,
	); err != nil {
		t.Fatal(err)
	}
	if ownerID != 2 || createdByID.Valid || !createdByPublic.Valid || createdByPublic.String != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("token provenance after creator deletion = owner %d creator %v/%v", ownerID, createdByID, createdByPublic)
	}
}

func TestMigration083InstanceAuditEventsAreAppendOnly(t *testing.T) {
	database := openMigratedDB(t)
	if _, err := database.Exec(`
INSERT INTO instance_audit_events(
  actor_user_id, actor_public_id, action, target_type,
  target_user_id, target_public_id, created_at
) VALUES (1, 'actor', 'service_account_created', 'service_account', 2, 'target', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE instance_audit_events SET action = 'forged'`); err == nil {
		t.Fatal("instance audit update succeeded")
	}
	if _, err := database.Exec(`DELETE FROM instance_audit_events`); err == nil {
		t.Fatal("instance audit delete succeeded")
	}
}
