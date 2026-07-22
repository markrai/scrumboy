package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

// TestClaimTemporaryBoard_ExpiredNotClaimable_StoreEnforcesCondition verifies the store's
// atomic `expires_at > ?` guard directly. HTTP routing / project-context lookup rejects an
// expired board before ClaimTemporaryBoard runs, so an HTTP 404 alone does not prove the store
// enforces expiry. This calls ClaimTemporaryBoard directly, as the recorded creator, on a board
// forced past its expiry and requires ErrNotFound with no state change.
func TestClaimTemporaryBoard_ExpiredNotClaimable_StoreEnforcesCondition(t *testing.T) {
	st, sqlDB, cleanup := newTestStoreWithSQL(t)
	defer cleanup()
	ctx := context.Background()

	creator, err := st.BootstrapUser(ctx, "creator@example.com", "password", "Creator")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}

	// Creator-attributed Temporary Board (expires_at set, creator_user_id = creator).
	p, err := st.CreateAnonymousBoard(WithUserID(ctx, creator.ID))
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}

	// Force expires_at into the past so ONLY the store's `expires_at > ?` condition can reject it.
	pastMs := time.Now().UTC().Add(-time.Hour).UnixMilli()
	if _, err := sqlDB.Exec(`UPDATE projects SET expires_at = ? WHERE id = ?`, pastMs, p.ID); err != nil {
		t.Fatalf("expire board: %v", err)
	}

	if err := st.ClaimTemporaryBoard(ctx, p.ID, creator.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for expired claim, got %v", err)
	}

	var owner, expires, creatorCol sql.NullInt64
	if err := sqlDB.QueryRow(
		`SELECT owner_user_id, expires_at, creator_user_id FROM projects WHERE id = ?`, p.ID,
	).Scan(&owner, &expires, &creatorCol); err != nil {
		t.Fatalf("read project: %v", err)
	}
	if owner.Valid {
		t.Fatalf("expected owner_user_id still NULL, got %+v", owner)
	}
	if !expires.Valid || expires.Int64 != pastMs {
		t.Fatalf("expected expires_at unchanged (%d), got %+v", pastMs, expires)
	}
	if !creatorCol.Valid || creatorCol.Int64 != creator.ID {
		t.Fatalf("expected creator_user_id unchanged (%d), got %+v", creator.ID, creatorCol)
	}

	var memberCount int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM project_members WHERE project_id = ?`, p.ID).Scan(&memberCount); err != nil {
		t.Fatalf("count members: %v", err)
	}
	if memberCount != 0 {
		t.Fatalf("expected no membership rows after failed claim, got %d", memberCount)
	}
}

// TestClaimTemporaryBoard_PromotesExistingMembershipToMaintainer verifies the membership upsert:
// a successful claim on a Temporary Board that already has a lower-role membership row for the
// creator must promote that row to maintainer (ON CONFLICT ... DO UPDATE), not silently ignore it.
func TestClaimTemporaryBoard_PromotesExistingMembershipToMaintainer(t *testing.T) {
	st, sqlDB, cleanup := newTestStoreWithSQL(t)
	defer cleanup()
	ctx := context.Background()

	creator, err := st.BootstrapUser(ctx, "creator@example.com", "password", "Creator")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}

	p, err := st.CreateAnonymousBoard(WithUserID(ctx, creator.ID))
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}

	// Seed a pre-existing LOWER-role (viewer) membership for the creator, so we can prove the
	// claim's upsert promotes it rather than being a no-op.
	nowMs := time.Now().UTC().UnixMilli()
	if _, err := sqlDB.Exec(
		`INSERT INTO project_members (project_id, user_id, role, created_at) VALUES (?, ?, 'viewer', ?)`,
		p.ID, creator.ID, nowMs,
	); err != nil {
		t.Fatalf("seed viewer membership: %v", err)
	}

	if err := st.ClaimTemporaryBoard(ctx, p.ID, creator.ID); err != nil {
		t.Fatalf("ClaimTemporaryBoard: %v", err)
	}

	role, err := st.GetProjectRole(ctx, p.ID, creator.ID)
	if err != nil {
		t.Fatalf("GetProjectRole: %v", err)
	}
	if role != RoleMaintainer {
		t.Fatalf("expected role promoted to maintainer, got %q", role)
	}

	// Exactly one membership row for the creator (upsert, not a duplicate insert).
	var count int
	if err := sqlDB.QueryRow(
		`SELECT COUNT(*) FROM project_members WHERE project_id = ? AND user_id = ?`, p.ID, creator.ID,
	).Scan(&count); err != nil {
		t.Fatalf("count creator memberships: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 membership row, got %d", count)
	}
}
