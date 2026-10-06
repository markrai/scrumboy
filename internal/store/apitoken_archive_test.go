package store

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDeleteUserArchivesServiceTokensAndCascadesAllTokenRows(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	owner, err := st.BootstrapUser(ctx, "owner@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	departing, err := st.CreateUser(ctx, "departing@example.com", "password123", "Departing")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	svcID, svcPlain, svcCreatedAt, err := st.CreateUserAPIToken(ctx, departing.ID, stringPtr("ci-automation"), true)
	if err != nil {
		t.Fatalf("create service token: %v", err)
	}
	personalID, personalPlain, _, err := st.CreateUserAPIToken(ctx, departing.ID, stringPtr("laptop"), false)
	if err != nil {
		t.Fatalf("create personal token: %v", err)
	}

	if err := st.DeleteUser(ctx, owner.ID, departing.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	// Neither secret authenticates, and neither live token row survives the deletion.
	for label, secret := range map[string]string{"service": svcPlain, "personal": personalPlain} {
		if _, err := st.GetUserByAPIToken(ctx, secret); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s secret authenticated after deletion: got %v want ErrNotFound", label, err)
		}
	}
	assertAPITokenRowGone(t, st.db, svcID)
	assertAPITokenRowGone(t, st.db, personalID)

	// The deleting owner's live token list is not polluted with inherited records.
	ownerTokens, err := st.ListUserAPITokens(ctx, owner.ID)
	if err != nil {
		t.Fatalf("list owner tokens: %v", err)
	}
	if len(ownerTokens) != 0 {
		t.Fatalf("owner inherited live token rows: %+v", ownerTokens)
	}

	records := listAllArchivedServiceAPITokens(t, st, owner.ID)
	if len(records) != 1 {
		t.Fatalf("archived records = %d, want 1 (service only): %+v", len(records), records)
	}
	got := records[0]
	if got.TokenID != svcID || got.Name == nil || *got.Name != "ci-automation" || !got.CreatedAt.Equal(svcCreatedAt) {
		t.Fatalf("archived token metadata = %+v, want token %d named ci-automation created %v", got, svcID, svcCreatedAt)
	}
	if !got.RevokedOnArchive || !got.RevokedAt.Equal(got.ArchivedAt) {
		t.Fatalf("active token should be recorded as revoked by the archival: %+v", got)
	}
	if got.OriginUserID != departing.ID || got.OriginUserEmail != "departing@example.com" || got.OriginUserName != "Departing" {
		t.Fatalf("origin provenance = (%d, %q, %q), want departing user", got.OriginUserID, got.OriginUserEmail, got.OriginUserName)
	}
	if got.ArchivedByUserID != owner.ID || got.ArchivedByUserEmail != "owner@example.com" {
		t.Fatalf("archived-by = (%d, %q), want deleting owner", got.ArchivedByUserID, got.ArchivedByUserEmail)
	}
}

func TestArchivedServiceTokenProvenanceSurvivesRepeatedOwnerDeletionAndUserIDReuse(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	finalOwner, err := st.BootstrapUser(ctx, "final-owner@example.com", "password123", "Final Owner")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	intermediateOwner, err := st.CreateUser(ctx, "intermediate-owner@example.com", "password123", "Intermediate Owner")
	if err != nil {
		t.Fatalf("create intermediate owner: %v", err)
	}
	if err := st.UpdateUserRole(ctx, finalOwner.ID, intermediateOwner.ID, SystemRoleOwner); err != nil {
		t.Fatalf("promote intermediate owner: %v", err)
	}
	original, err := st.CreateUser(ctx, "original@example.com", "password123", "Original Creator")
	if err != nil {
		t.Fatalf("create original creator: %v", err)
	}

	activeID, _, _, err := st.CreateUserAPIToken(ctx, original.ID, stringPtr("active-service"), true)
	if err != nil {
		t.Fatalf("create active service token: %v", err)
	}
	revokedID, _, _, err := st.CreateUserAPIToken(ctx, original.ID, stringPtr("revoked-service"), true)
	if err != nil {
		t.Fatalf("create revoked service token: %v", err)
	}
	if err := st.RevokeUserAPIToken(ctx, original.ID, revokedID); err != nil {
		t.Fatalf("revoke service token: %v", err)
	}
	var originalRevokedAtMs int64
	if err := st.db.QueryRowContext(ctx, `SELECT revoked_at FROM api_tokens WHERE id = ?`, revokedID).Scan(&originalRevokedAtMs); err != nil {
		t.Fatalf("read original revoked_at: %v", err)
	}
	intermediateTokenID, _, _, err := st.CreateUserAPIToken(ctx, intermediateOwner.ID, stringPtr("intermediate-service"), true)
	if err != nil {
		t.Fatalf("create intermediate service token: %v", err)
	}

	// First deletion: the original creator is removed by the intermediate owner.
	if err := st.DeleteUser(ctx, intermediateOwner.ID, original.ID); err != nil {
		t.Fatalf("delete original creator: %v", err)
	}
	afterFirst := archivedByTokenID(t, st, finalOwner.ID)
	for _, tokenID := range []int64{activeID, revokedID} {
		assertArchivedProvenance(t, afterFirst, tokenID, original, intermediateOwner)
	}

	// Second deletion: the custodian that archived them is itself removed.
	if err := st.DeleteUser(ctx, finalOwner.ID, intermediateOwner.ID); err != nil {
		t.Fatalf("delete intermediate owner: %v", err)
	}
	// Reuse the deleted intermediate owner's id (users.id has no AUTOINCREMENT), so any
	// provenance resolved through users(id) would now name the wrong person.
	reused, err := st.CreateUser(ctx, "id-reuser@example.com", "password123", "ID Reuser")
	if err != nil {
		t.Fatalf("create id-reusing user: %v", err)
	}
	if reused.ID != intermediateOwner.ID {
		t.Fatalf("new user id = %d, want reused id %d for this regression", reused.ID, intermediateOwner.ID)
	}

	afterSecond := archivedByTokenID(t, st, finalOwner.ID)
	if len(afterSecond) != 3 {
		t.Fatalf("archived records = %d, want 3: %+v", len(afterSecond), afterSecond)
	}
	for _, tokenID := range []int64{activeID, revokedID} {
		if !reflect.DeepEqual(afterSecond[tokenID], afterFirst[tokenID]) {
			t.Fatalf("archived record for token %d changed across custodian deletion:\nbefore %+v\nafter  %+v", tokenID, afterFirst[tokenID], afterSecond[tokenID])
		}
		assertArchivedProvenance(t, afterSecond, tokenID, original, intermediateOwner)
	}
	assertArchivedProvenance(t, afterSecond, intermediateTokenID, intermediateOwner, finalOwner)

	if !afterSecond[activeID].RevokedOnArchive {
		t.Fatalf("active token should be marked revoked on archive: %+v", afterSecond[activeID])
	}
	if afterSecond[revokedID].RevokedOnArchive || afterSecond[revokedID].RevokedAt.UnixMilli() != originalRevokedAtMs {
		t.Fatalf("previously revoked token lost its original revocation: %+v want revokedAt=%d", afterSecond[revokedID], originalRevokedAtMs)
	}

	// Neither the surviving owner nor the id-reusing user inherits live token rows.
	for _, userID := range []int64{finalOwner.ID, reused.ID} {
		tokens, err := st.ListUserAPITokens(ctx, userID)
		if err != nil {
			t.Fatalf("list tokens for user %d: %v", userID, err)
		}
		if len(tokens) != 0 {
			t.Fatalf("user %d inherited live token rows: %+v", userID, tokens)
		}
	}
}

// archived_service_api_tokens.token_id is UNIQUE, which is only safe because api_tokens.id is
// AUTOINCREMENT: deleting the highest-id token row (by deleting its user) must not let a later
// token reuse that id and make the later user's deletion collide with the old archive record.
func TestServiceTokenArchiveNeverCollidesOnReusedSourceTokenID(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	var ddl string
	if err := st.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'api_tokens'`).Scan(&ddl); err != nil {
		t.Fatalf("read api_tokens schema: %v", err)
	}
	if !strings.Contains(strings.Join(strings.Fields(ddl), " "), "id INTEGER PRIMARY KEY AUTOINCREMENT") {
		t.Fatalf("api_tokens.id is not AUTOINCREMENT, so source token ids can be reused:\n%s", ddl)
	}

	owner, err := st.BootstrapUser(ctx, "reuse-owner@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	first, err := st.CreateUser(ctx, "reuse-first@example.com", "password123", "First")
	if err != nil {
		t.Fatalf("create first user: %v", err)
	}
	firstTokenID, _, _, err := st.CreateUserAPIToken(ctx, first.ID, stringPtr("first"), true)
	if err != nil {
		t.Fatalf("create first token: %v", err)
	}
	// Deleting the user removes the highest (and only) api_tokens row: the exact condition under
	// which a plain INTEGER PRIMARY KEY would hand the same id to the next insert.
	if err := st.DeleteUser(ctx, owner.ID, first.ID); err != nil {
		t.Fatalf("delete first user: %v", err)
	}
	var liveRows int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_tokens`).Scan(&liveRows); err != nil {
		t.Fatalf("count api_tokens: %v", err)
	}
	if liveRows != 0 {
		t.Fatalf("api_tokens rows after deletion = %d, want 0", liveRows)
	}

	second, err := st.CreateUser(ctx, "reuse-second@example.com", "password123", "Second")
	if err != nil {
		t.Fatalf("create second user: %v", err)
	}
	secondTokenID, _, _, err := st.CreateUserAPIToken(ctx, second.ID, stringPtr("second"), true)
	if err != nil {
		t.Fatalf("create second token: %v", err)
	}
	if secondTokenID <= firstTokenID {
		t.Fatalf("source token id reused: second=%d first=%d", secondTokenID, firstTokenID)
	}
	if err := st.DeleteUser(ctx, owner.ID, second.ID); err != nil {
		t.Fatalf("delete second user collided with an existing archive record: %v", err)
	}

	records := archivedByTokenID(t, st, owner.ID)
	if len(records) != 2 {
		t.Fatalf("archived records = %d, want 2: %+v", len(records), records)
	}
	assertArchivedProvenance(t, records, firstTokenID, first, owner)
	assertArchivedProvenance(t, records, secondTokenID, second, owner)
}

func TestDeleteUserServiceTokenArchiveRollsBackWhenUserDeletionFails(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	owner, err := st.BootstrapUser(ctx, "rollback-owner@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	departing, err := st.CreateUser(ctx, "rollback-target@example.com", "password123", "Target")
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	tokenID, secret, _, err := st.CreateUserAPIToken(ctx, departing.ID, stringPtr("rollback-service"), true)
	if err != nil {
		t.Fatalf("create service token: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `
CREATE TRIGGER fail_service_owner_delete
BEFORE DELETE ON users
BEGIN
  SELECT RAISE(FAIL, 'forced user deletion failure');
END`); err != nil {
		t.Fatalf("create deletion failure trigger: %v", err)
	}

	err = st.DeleteUser(ctx, owner.ID, departing.ID)
	if err == nil || !strings.Contains(err.Error(), "forced user deletion failure") {
		t.Fatalf("DeleteUser error=%v want forced failure", err)
	}
	assertAPITokenRow(t, st.db, tokenID, departing.ID, true, false)
	if n := countArchivedServiceAPITokens(t, st.db); n != 0 {
		t.Fatalf("archive rows after failed deletion = %d, want 0", n)
	}
	got, err := st.GetUserByAPIToken(ctx, secret)
	if err != nil {
		t.Fatalf("service secret should remain active after rollback: %v", err)
	}
	if got.ID != departing.ID {
		t.Fatalf("service secret resolved user=%d want %d", got.ID, departing.ID)
	}
}

func TestArchivedServiceAPITokensAreImmutable(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	createOwnerAndArchivedServiceTokens(t, st, 1)

	for _, stmt := range []string{
		`UPDATE archived_service_api_tokens SET origin_user_email = 'forged@example.com'`,
		`UPDATE archived_service_api_tokens SET archived_by_user_id = 999`,
		`UPDATE archived_service_api_tokens SET revoked_on_archive = 0`,
	} {
		_, err := st.db.ExecContext(ctx, stmt)
		if err == nil || !strings.Contains(err.Error(), "archived_service_api_tokens is immutable") {
			t.Fatalf("%s: err=%v, want immutability failure", stmt, err)
		}
	}
}

func TestListArchivedServiceAPITokensPaginatesAndRequiresOwner(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	owner, _ := createOwnerAndArchivedServiceTokens(t, st, 5)
	admin, err := st.CreateUser(ctx, "admin@example.com", "password123", "Admin")
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if err := st.UpdateUserRole(ctx, owner.ID, admin.ID, SystemRoleAdmin); err != nil {
		t.Fatalf("promote admin: %v", err)
	}
	plain, err := st.CreateUser(ctx, "plain@example.com", "password123", "Plain")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	for _, requesterID := range []int64{admin.ID, plain.ID} {
		if _, _, err := st.ListArchivedServiceAPITokens(ctx, requesterID, 10, 0); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("requester %d list err=%v, want ErrUnauthorized", requesterID, err)
		}
	}
	for _, limit := range []int{0, -1, MaxArchivedServiceAPITokensPageSize + 1} {
		if _, _, err := st.ListArchivedServiceAPITokens(ctx, owner.ID, limit, 0); !errors.Is(err, ErrValidation) {
			t.Fatalf("limit %d err=%v, want ErrValidation", limit, err)
		}
	}

	var (
		pageSizes []int
		seen      = map[int64]bool{}
		lastID    int64
		before    int64
	)
	for page := 0; page < 10; page++ {
		items, next, err := st.ListArchivedServiceAPITokens(ctx, owner.ID, 2, before)
		if err != nil {
			t.Fatalf("list page %d: %v", page, err)
		}
		pageSizes = append(pageSizes, len(items))
		for _, item := range items {
			if lastID != 0 && item.ID >= lastID {
				t.Fatalf("archive not ordered newest first: %d after %d", item.ID, lastID)
			}
			lastID = item.ID
			seen[item.TokenID] = true
		}
		if next == nil {
			break
		}
		before = *next
	}
	if len(seen) != 5 || len(pageSizes) != 3 || pageSizes[0] != 2 || pageSizes[1] != 2 || pageSizes[2] != 1 {
		t.Fatalf("pagination returned page sizes %v and %d distinct tokens; want [2 2 1] and 5", pageSizes, len(seen))
	}
}

func TestPurgeArchivedServiceAPITokensRemovesOnlyOlderRecordsAndRequiresOwner(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	owner, _ := createOwnerAndArchivedServiceTokens(t, st, 1)
	first := listAllArchivedServiceAPITokens(t, st, owner.ID)[0]
	cutoff := first.ArchivedAt.Add(time.Millisecond)
	for !time.Now().After(cutoff) {
		time.Sleep(time.Millisecond)
	}

	later, err := st.CreateUser(ctx, "later@example.com", "password123", "Later")
	if err != nil {
		t.Fatalf("create later user: %v", err)
	}
	laterTokenID, _, _, err := st.CreateUserAPIToken(ctx, later.ID, stringPtr("later-service"), true)
	if err != nil {
		t.Fatalf("create later service token: %v", err)
	}
	if err := st.DeleteUser(ctx, owner.ID, later.ID); err != nil {
		t.Fatalf("delete later user: %v", err)
	}

	admin, err := st.CreateUser(ctx, "purge-admin@example.com", "password123", "Admin")
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if err := st.UpdateUserRole(ctx, owner.ID, admin.ID, SystemRoleAdmin); err != nil {
		t.Fatalf("promote admin: %v", err)
	}
	if _, err := st.PurgeArchivedServiceAPITokens(ctx, admin.ID, time.Now().Add(time.Hour)); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("admin purge err=%v, want ErrUnauthorized", err)
	}
	if _, err := st.PurgeArchivedServiceAPITokens(ctx, owner.ID, time.Time{}); !errors.Is(err, ErrValidation) {
		t.Fatalf("zero cutoff err=%v, want ErrValidation", err)
	}
	if n := countArchivedServiceAPITokens(t, st.db); n != 2 {
		t.Fatalf("archive rows after rejected purges = %d, want 2", n)
	}

	deleted, err := st.PurgeArchivedServiceAPITokens(ctx, owner.ID, cutoff)
	if err != nil {
		t.Fatalf("owner purge: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("purged %d records, want 1", deleted)
	}
	remaining := listAllArchivedServiceAPITokens(t, st, owner.ID)
	if len(remaining) != 1 || remaining[0].TokenID != laterTokenID {
		t.Fatalf("remaining archive = %+v, want only token %d", remaining, laterTokenID)
	}
}

// createOwnerAndArchivedServiceTokens bootstraps an owner, gives a second user n service tokens,
// and deletes that user so the tokens are archived.
func createOwnerAndArchivedServiceTokens(t *testing.T, st *Store, n int) (owner, departed User) {
	t.Helper()
	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "archive-owner@example.com", "password123", "Archive Owner")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	departed, err = st.CreateUser(ctx, "archive-departed@example.com", "password123", "Archive Departed")
	if err != nil {
		t.Fatalf("create departing user: %v", err)
	}
	for i := 0; i < n; i++ {
		if _, _, _, err := st.CreateUserAPIToken(ctx, departed.ID, stringPtr("svc"), true); err != nil {
			t.Fatalf("create service token %d: %v", i, err)
		}
	}
	if err := st.DeleteUser(ctx, owner.ID, departed.ID); err != nil {
		t.Fatalf("delete departing user: %v", err)
	}
	return owner, departed
}

func listAllArchivedServiceAPITokens(t *testing.T, st *Store, ownerID int64) []ArchivedServiceAPIToken {
	t.Helper()
	items, next, err := st.ListArchivedServiceAPITokens(context.Background(), ownerID, MaxArchivedServiceAPITokensPageSize, 0)
	if err != nil {
		t.Fatalf("list archived service api tokens: %v", err)
	}
	if next != nil {
		t.Fatalf("unexpected second archive page in test fixture")
	}
	return items
}

func archivedByTokenID(t *testing.T, st *Store, ownerID int64) map[int64]ArchivedServiceAPIToken {
	t.Helper()
	out := map[int64]ArchivedServiceAPIToken{}
	for _, item := range listAllArchivedServiceAPITokens(t, st, ownerID) {
		out[item.TokenID] = item
	}
	return out
}

func assertArchivedProvenance(t *testing.T, records map[int64]ArchivedServiceAPIToken, tokenID int64, origin, archivedBy User) {
	t.Helper()
	got, ok := records[tokenID]
	if !ok {
		t.Fatalf("token %d missing from archive: %+v", tokenID, records)
	}
	if got.OriginUserID != origin.ID || got.OriginUserEmail != origin.Email || got.OriginUserName != origin.Name {
		t.Fatalf("token %d origin = (%d, %q, %q), want (%d, %q, %q)", tokenID, got.OriginUserID, got.OriginUserEmail, got.OriginUserName, origin.ID, origin.Email, origin.Name)
	}
	if got.ArchivedByUserID != archivedBy.ID || got.ArchivedByUserEmail != archivedBy.Email {
		t.Fatalf("token %d archived-by = (%d, %q), want (%d, %q)", tokenID, got.ArchivedByUserID, got.ArchivedByUserEmail, archivedBy.ID, archivedBy.Email)
	}
}

func countArchivedServiceAPITokens(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM archived_service_api_tokens`).Scan(&n); err != nil {
		t.Fatalf("count archived service api tokens: %v", err)
	}
	return n
}

func assertAPITokenRowGone(t *testing.T, db *sql.DB, tokenID int64) {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM api_tokens WHERE id = ?`, tokenID).Scan(&n); err != nil {
		t.Fatalf("count api token %d: %v", tokenID, err)
	}
	if n != 0 {
		t.Fatalf("api token %d still exists after its user was deleted", tokenID)
	}
}

func assertAPITokenRow(t *testing.T, db *sql.DB, tokenID, wantUserID int64, wantService, wantRevoked bool) {
	t.Helper()
	var (
		userID    int64
		isService bool
		revokedAt sql.NullInt64
	)
	if err := db.QueryRow(`SELECT user_id, is_service, revoked_at FROM api_tokens WHERE id = ?`, tokenID).Scan(&userID, &isService, &revokedAt); err != nil {
		t.Fatalf("read api token %d: %v", tokenID, err)
	}
	if userID != wantUserID || isService != wantService || revokedAt.Valid != wantRevoked {
		t.Fatalf("api token %d state user=%d service=%v revoked=%v; want user=%d service=%v revoked=%v", tokenID, userID, isService, revokedAt.Valid, wantUserID, wantService, wantRevoked)
	}
}

func stringPtr(value string) *string {
	return &value
}
