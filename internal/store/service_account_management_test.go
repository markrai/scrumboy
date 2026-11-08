package store

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
)

func TestServiceAccountManagementAuthorizationAndPagination(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()
	second, err := f.st.CreateServiceAccount(ctx, f.admin.ID, "Admin Bot")
	if err != nil {
		t.Fatalf("admin create service account: %v", err)
	}
	third, err := f.st.CreateServiceAccount(ctx, f.owner.ID, "Owner Bot")
	if err != nil {
		t.Fatalf("owner create service account: %v", err)
	}

	page, cursor, err := f.st.ListServiceAccounts(ctx, f.admin.ID, 2, nil)
	if err != nil {
		t.Fatalf("admin list service accounts: %v", err)
	}
	if len(page) != 2 || cursor == nil || !page[0].IsService || page[0].PublicID == "" {
		t.Fatalf("first page = %+v cursor=%v", page, cursor)
	}
	next, finalCursor, err := f.st.ListServiceAccounts(ctx, f.owner.ID, 2, cursor)
	if err != nil {
		t.Fatalf("owner list service accounts: %v", err)
	}
	if len(next) != 1 || finalCursor != nil {
		t.Fatalf("second page = %+v cursor=%v", next, finalCursor)
	}
	for _, account := range append(page, next...) {
		if !account.IsService || account.Email != "" || account.SystemRole != SystemRoleUser {
			t.Fatalf("unsafe service account projection: %+v", account)
		}
	}
	for _, requester := range []int64{f.human.ID, f.svc.ID} {
		if _, _, err := f.st.ListServiceAccounts(ctx, requester, 20, nil); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("requester %d list error = %v, want unauthorized", requester, err)
		}
	}
	if err := f.st.DeleteServiceAccount(ctx, f.admin.ID, second.ID); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("admin delete = %v, want unauthorized", err)
	}
	if err := f.st.DeleteServiceAccount(ctx, f.owner.ID, f.human.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("human target delete = %v, want not found", err)
	}
	if err := f.st.DeleteServiceAccount(ctx, f.owner.ID, third.ID); err != nil {
		t.Fatalf("owner delete service account: %v", err)
	}
}

func TestServiceAccountTokenLifecycleAndHumanCredentialBoundary(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()
	name := "deployment"
	tokenID, plaintext, createdAt, err := f.st.CreateServiceAccountToken(ctx, f.owner.ID, f.svc.ID, &name)
	if err != nil {
		t.Fatalf("owner mint: %v", err)
	}
	if !strings.HasPrefix(plaintext, APITokenPrefix) || len(plaintext) != len(APITokenPrefix)+43 {
		t.Fatalf("token format prefix=%v length=%d", strings.HasPrefix(plaintext, APITokenPrefix), len(plaintext))
	}
	var (
		storedHash      string
		storedUserID    int64
		lifecycleFlag   bool
		createdByUserID int64
		createdByPublic string
	)
	if err := f.db.QueryRow(`SELECT token_hash, user_id, is_service, created_by_user_id, created_by_user_public_id FROM api_tokens WHERE id = ?`, tokenID).Scan(
		&storedHash, &storedUserID, &lifecycleFlag, &createdByUserID, &createdByPublic,
	); err != nil {
		t.Fatal(err)
	}
	if storedHash != hashToken(plaintext) || storedHash == plaintext || storedUserID != f.svc.ID || !lifecycleFlag || createdByUserID != f.owner.ID || createdByPublic == "" {
		t.Fatalf("stored token metadata hashMatches=%v hashIsPlaintext=%v user=%d flag=%v creator=%d hasCreatorPublicID=%v", storedHash == hashToken(plaintext), storedHash == plaintext, storedUserID, lifecycleFlag, createdByUserID, createdByPublic != "")
	}
	if authenticated, err := f.st.GetUserByAPIToken(ctx, plaintext); err != nil || authenticated.ID != f.svc.ID || !authenticated.IsService {
		t.Fatalf("service token authentication = %+v, %v", authenticated, err)
	}

	for _, requester := range []int64{f.admin.ID, f.human.ID, f.svc.ID} {
		if _, _, _, err := f.st.CreateServiceAccountToken(ctx, requester, f.svc.ID, nil); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("requester %d mint = %v, want unauthorized", requester, err)
		}
	}
	if _, _, _, err := f.st.CreateServiceAccountToken(ctx, f.owner.ID, f.human.ID, nil); !errors.Is(err, ErrValidation) {
		t.Fatalf("mint for human = %v, want validation", err)
	}

	humanTokenID, humanPlaintext, _, err := f.st.CreateUserAPIToken(ctx, f.human.ID, stringPtr("human automation"), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.st.ListServiceAccountTokens(ctx, f.owner.ID, f.human.ID, 20, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("list human automation token = %v, want not found", err)
	}
	if err := f.st.RevokeServiceAccountToken(ctx, f.admin.ID, humanTokenID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoke human automation token = %v, want not found", err)
	}
	if _, err := f.st.GetUserByAPIToken(ctx, humanPlaintext); err != nil {
		t.Fatalf("human automation token changed: %v", err)
	}

	secondID, secondPlaintext, _, err := f.st.CreateServiceAccountToken(ctx, f.owner.ID, f.svc.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	items, next, err := f.st.ListServiceAccountTokens(ctx, f.admin.ID, f.svc.ID, 1, 0)
	if err != nil || len(items) != 1 || next == nil || items[0].CreatedByUserPublicID == nil || items[0].CreatedAt.Before(createdAt) {
		t.Fatalf("admin token list = %+v next=%v err=%v", items, next, err)
	}
	if err := f.st.RevokeServiceAccountToken(ctx, f.admin.ID, tokenID); err != nil {
		t.Fatalf("admin revoke: %v", err)
	}
	if err := f.st.RevokeServiceAccountToken(ctx, f.owner.ID, tokenID); err != nil {
		t.Fatalf("idempotent owner revoke: %v", err)
	}
	if _, err := f.st.GetUserByAPIToken(ctx, plaintext); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked token authentication = %v", err)
	}
	if authenticated, err := f.st.GetUserByAPIToken(ctx, secondPlaintext); err != nil || authenticated.ID != f.svc.ID {
		t.Fatalf("sibling token %d authentication = %+v, %v", secondID, authenticated, err)
	}
}

func TestServiceTokenRevocationWorksForSoleMaintainer(t *testing.T) {
	f := newServiceProjectFixture(t)
	ctx := context.Background()
	tokenID, plaintext, _, err := f.st.CreateServiceAccountToken(ctx, f.owner.ID, f.svc.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.AddProjectMember(f.ownerCtx, f.owner.ID, f.durable.ID, f.svc.ID, RoleMaintainer); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`DELETE FROM project_members WHERE project_id = ? AND user_id = ?`, f.durable.ID, f.owner.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.st.DeleteServiceAccount(ctx, f.owner.ID, f.svc.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("sole maintainer deletion = %v, want conflict", err)
	}
	if err := f.st.RevokeServiceAccountToken(ctx, f.admin.ID, tokenID); err != nil {
		t.Fatalf("incident revocation: %v", err)
	}
	if _, err := f.st.GetUserByAPIToken(ctx, plaintext); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked sole maintainer token = %v", err)
	}
}

func TestServiceTokenConcurrentRevocationIsIdempotent(t *testing.T) {
	f := newServiceFixture(t)
	tokenID, _, _, err := f.st.CreateServiceAccountToken(context.Background(), f.owner.ID, f.svc.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, requester := range []int64{f.owner.ID, f.admin.ID} {
		wg.Add(1)
		go func(requester int64) {
			defer wg.Done()
			<-start
			results <- f.st.RevokeServiceAccountToken(context.Background(), requester, tokenID)
		}(requester)
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("concurrent revoke: %v", err)
		}
	}
	if n := f.count(t, `SELECT COUNT(*) FROM api_tokens WHERE id = ? AND revoked_at IS NOT NULL`, tokenID); n != 1 {
		t.Fatalf("revoked rows = %d", n)
	}
}

func TestServiceAccountMintAndDeletionCannotLeaveCredential(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()
	start := make(chan struct{})
	mintResult := make(chan error, 1)
	deleteResult := make(chan error, 1)
	go func() {
		<-start
		_, _, _, err := f.st.CreateServiceAccountToken(ctx, f.owner.ID, f.svc.ID, nil)
		mintResult <- err
	}()
	go func() {
		<-start
		deleteResult <- f.st.DeleteServiceAccount(ctx, f.owner.ID, f.svc.ID)
	}()
	close(start)
	mintErr := <-mintResult
	deleteErr := <-deleteResult
	if mintErr != nil && !errors.Is(mintErr, ErrNotFound) {
		t.Fatalf("mint result = %v", mintErr)
	}
	if deleteErr != nil {
		t.Fatalf("delete result = %v", deleteErr)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM users WHERE id = ?`, f.svc.ID); n != 0 {
		t.Fatalf("service account survived: %d", n)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM api_tokens WHERE user_id = ?`, f.svc.ID); n != 0 {
		t.Fatalf("orphan credentials = %d", n)
	}
}

func TestServiceCredentialArchiveDistinguishesIdentityType(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()
	serviceTokenID, _, _, err := f.st.CreateServiceAccountToken(ctx, f.owner.ID, f.svc.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	humanTokenID, _, _, err := f.st.CreateUserAPIToken(ctx, f.human.ID, stringPtr("automation"), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.DeleteServiceAccount(ctx, f.owner.ID, f.svc.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.st.DeleteUser(ctx, f.owner.ID, f.human.ID); err != nil {
		t.Fatal(err)
	}
	items, _, err := f.st.ListArchivedServiceAPITokens(ctx, f.owner.ID, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	classification := map[int64]*bool{}
	for _, item := range items {
		classification[item.TokenID] = item.OriginUserIsService
		if item.CreatedByUserPublicID == nil || *item.CreatedByUserPublicID == "" {
			t.Fatalf("missing issuer provenance: %+v", item)
		}
	}
	if classification[serviceTokenID] == nil || !*classification[serviceTokenID] {
		t.Fatalf("service token classification = %v", classification[serviceTokenID])
	}
	if classification[humanTokenID] == nil || *classification[humanTokenID] {
		t.Fatalf("human automation token classification = %v", classification[humanTokenID])
	}
}

func TestInstanceAuditLifecycleAndSecretExclusion(t *testing.T) {
	f := newServiceFixture(t)
	if n := f.count(t, `SELECT COUNT(*) FROM instance_audit_events`); n != 0 {
		t.Fatalf("disabled audit events = %d", n)
	}
	f.st.instanceAuditEnabled = true
	ctx := context.Background()
	account, err := f.st.CreateServiceAccount(ctx, f.admin.ID, "Audited Bot")
	if err != nil {
		t.Fatal(err)
	}
	tokenID, plaintext, _, err := f.st.CreateServiceAccountToken(ctx, f.owner.ID, account.ID, stringPtr("audited"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.RevokeServiceAccountToken(ctx, f.admin.ID, tokenID); err != nil {
		t.Fatal(err)
	}
	if err := f.st.DeleteServiceAccount(ctx, f.owner.ID, account.ID); err != nil {
		t.Fatal(err)
	}
	rows, err := f.db.Query(`SELECT action, actor_public_id, target_public_id, COALESCE(token_id, 0), COALESCE(metadata, '') FROM instance_audit_events ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var serialized strings.Builder
	var actions []string
	for rows.Next() {
		var action, actorPublicID, targetPublicID, metadata string
		var storedTokenID int64
		if err := rows.Scan(&action, &actorPublicID, &targetPublicID, &storedTokenID, &metadata); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, action)
		serialized.WriteString(action + actorPublicID + targetPublicID + metadata)
		if strings.Contains(action, "token") && storedTokenID != tokenID {
			t.Fatalf("audit token id = %d, want %d", storedTokenID, tokenID)
		}
	}
	want := []string{instanceActionServiceAccountCreated, instanceActionServiceTokenCreated, instanceActionServiceTokenRevoked, instanceActionServiceAccountDeleted}
	if strings.Join(actions, ",") != strings.Join(want, ",") {
		t.Fatalf("actions = %v, want %v", actions, want)
	}
	stored := serialized.String()
	if strings.Contains(stored, plaintext) || strings.Contains(stored, hashToken(plaintext)) {
		t.Fatal("audit stream contains credential material")
	}
}

func TestDeleteUserAuditsServiceIdentityButNotHumanAutomationOwner(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()
	owner, err := f.st.GetUser(ctx, f.owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = f.st.CreateUserAPIToken(ctx, f.human.ID, stringPtr("automation"), true)
	if err != nil {
		t.Fatal(err)
	}
	f.st.instanceAuditEnabled = true
	if err := f.st.DeleteUser(ctx, f.owner.ID, f.human.ID); err != nil {
		t.Fatal(err)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM instance_audit_events WHERE action = 'service_account_deleted'`); n != 0 {
		t.Fatalf("human deletion service events=%d, want 0", n)
	}
	if err := f.st.DeleteUser(ctx, f.owner.ID, f.svc.ID); err != nil {
		t.Fatal(err)
	}
	var actorPublicID, targetPublicID string
	if err := f.db.QueryRow(`SELECT actor_public_id, target_public_id FROM instance_audit_events WHERE action = 'service_account_deleted'`).Scan(&actorPublicID, &targetPublicID); err != nil {
		t.Fatal(err)
	}
	if actorPublicID != owner.PublicID || targetPublicID != f.svc.PublicID {
		t.Fatalf("audit identifiers match actor=%v target=%v", actorPublicID == owner.PublicID, targetPublicID == f.svc.PublicID)
	}
}

func TestEnabledInstanceAuditFailureRollsBackLifecycleMutations(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()
	tokenID, plaintext, _, err := f.st.CreateServiceAccountToken(ctx, f.owner.ID, f.svc.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.st.instanceAuditEnabled = true
	if _, err := f.db.Exec(`
CREATE TRIGGER fail_instance_audit_insert
BEFORE INSERT ON instance_audit_events
BEGIN
  SELECT RAISE(ABORT, 'forced instance audit failure');
END`); err != nil {
		t.Fatal(err)
	}
	beforeAccounts := f.count(t, `SELECT COUNT(*) FROM users WHERE is_service <> 0`)
	if _, err := f.st.CreateServiceAccount(ctx, f.admin.ID, "Must Roll Back"); err == nil {
		t.Fatal("service account creation succeeded despite audit failure")
	}
	if n := f.count(t, `SELECT COUNT(*) FROM users WHERE is_service <> 0`); n != beforeAccounts {
		t.Fatalf("service account count = %d, want %d", n, beforeAccounts)
	}
	if _, _, _, err := f.st.CreateServiceAccountToken(ctx, f.owner.ID, f.svc.ID, nil); err == nil {
		t.Fatal("token creation succeeded despite audit failure")
	}
	if n := f.count(t, `SELECT COUNT(*) FROM api_tokens WHERE user_id = ?`, f.svc.ID); n != 1 {
		t.Fatalf("token count = %d, want 1", n)
	}
	if err := f.st.RevokeServiceAccountToken(ctx, f.admin.ID, tokenID); err == nil {
		t.Fatal("revocation succeeded despite audit failure")
	}
	if _, err := f.st.GetUserByAPIToken(ctx, plaintext); err != nil {
		t.Fatalf("failed revocation changed authentication: %v", err)
	}
	if err := f.st.DeleteUser(ctx, f.owner.ID, f.svc.ID); err == nil {
		t.Fatal("deletion succeeded despite audit failure")
	}
	if _, err := f.st.GetUser(ctx, f.svc.ID); err != nil {
		t.Fatalf("failed deletion removed account: %v", err)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM archived_service_api_tokens WHERE token_id = ?`, tokenID); n != 0 {
		t.Fatalf("failed deletion archived %d token rows", n)
	}
	if _, err := f.st.GetUserByAPIToken(ctx, plaintext); err != nil {
		t.Fatalf("failed generic deletion invalidated credential: %v", err)
	}
}

func TestServiceAccountListPaginationBounds(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()
	for _, name := range []string{"Bot A", "Bot B", "Bot C", "Bot D"} {
		if _, err := f.st.CreateServiceAccount(ctx, f.owner.ID, name); err != nil {
			t.Fatal(err)
		}
	}
	for _, limit := range []int{0, -3} {
		page, cursor, err := f.st.ListServiceAccounts(ctx, f.owner.ID, limit, nil)
		if err != nil || len(page) != 5 || cursor != nil {
			t.Fatalf("limit %d default page = %d cursor=%v err=%v", limit, len(page), cursor, err)
		}
	}
	for _, limit := range []int{101, math.MaxInt} {
		if _, _, err := f.st.ListServiceAccounts(ctx, f.owner.ID, limit, nil); !errors.Is(err, ErrValidation) {
			t.Fatalf("limit %d err=%v", limit, err)
		}
	}
	if _, _, err := f.st.ListServiceAccounts(ctx, f.owner.ID, 20, stringPointer("bogus")); !errors.Is(err, ErrValidation) {
		t.Fatalf("bogus cursor err=%v", err)
	}
	var cursor *string
	seen := map[int64]bool{}
	var previous int64
	for round := 0; ; round++ {
		page, next, err := f.st.ListServiceAccounts(ctx, f.owner.ID, 2, cursor)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 || len(page) > 2 {
			t.Fatalf("round %d page size = %d", round, len(page))
		}
		for _, account := range page {
			if seen[account.ID] {
				t.Fatalf("duplicate account %d", account.ID)
			}
			seen[account.ID] = true
			if previous != 0 && account.ID >= previous {
				t.Fatalf("order broke at %d after %d", account.ID, previous)
			}
			previous = account.ID
			if !account.IsService {
				t.Fatalf("non-service account listed: %+v", account)
			}
		}
		cursor = next
		if next == nil {
			break
		}
		if round > 10 {
			t.Fatal("cursor did not terminate")
		}
	}
	if len(seen) != 5 {
		t.Fatalf("walked %d of 5 accounts", len(seen))
	}
	exact, exactCursor, err := f.st.ListServiceAccounts(ctx, f.owner.ID, 5, nil)
	if err != nil || len(exact) != 5 || exactCursor != nil {
		t.Fatalf("exact page = %d cursor=%v err=%v", len(exact), exactCursor, err)
	}
	full, _, err := f.st.ListServiceAccounts(ctx, f.owner.ID, 100, nil)
	if err != nil || len(full) != 5 {
		t.Fatalf("limit 100 = %d err=%v", len(full), err)
	}
	blank, blankCleanup := newTestStoreWith2FA(t)
	t.Cleanup(blankCleanup)
	blankOwner, err := blank.BootstrapUser(ctx, "blank-owner@example.com", serviceTestPassword, "Blank Owner")
	if err != nil {
		t.Fatal(err)
	}
	none, noneCursor, err := blank.ListServiceAccounts(ctx, blankOwner.ID, 20, nil)
	if err != nil || len(none) != 0 || noneCursor != nil {
		t.Fatalf("empty store list = %d cursor=%v err=%v", len(none), noneCursor, err)
	}
}

func TestServiceTokenListPaginationBounds(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()
	empty, emptyCursor, err := f.st.ListServiceAccountTokens(ctx, f.owner.ID, f.svc.ID, 20, 0)
	if err != nil || len(empty) != 0 || emptyCursor != nil {
		t.Fatalf("empty token list = %d cursor=%v err=%v", len(empty), emptyCursor, err)
	}
	var plaintexts []string
	for i := 0; i < 3; i++ {
		_, plaintext, _, err := f.st.CreateServiceAccountToken(ctx, f.owner.ID, f.svc.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		plaintexts = append(plaintexts, plaintext)
	}
	for _, limit := range []int{0, -1} {
		page, cursor, err := f.st.ListServiceAccountTokens(ctx, f.owner.ID, f.svc.ID, limit, 0)
		if err != nil || len(page) != 3 || cursor != nil {
			t.Fatalf("limit %d default page = %d cursor=%v err=%v", limit, len(page), cursor, err)
		}
	}
	for _, limit := range []int{101, math.MaxInt} {
		if _, _, err := f.st.ListServiceAccountTokens(ctx, f.owner.ID, f.svc.ID, limit, 0); !errors.Is(err, ErrValidation) {
			t.Fatalf("limit %d err=%v", limit, err)
		}
	}
	if _, _, err := f.st.ListServiceAccountTokens(ctx, f.owner.ID, f.svc.ID, 20, -1); !errors.Is(err, ErrValidation) {
		t.Fatalf("beforeID -1 err=%v", err)
	}
	var beforeID int64
	seen := map[int64]bool{}
	var collected []APITokenMeta
	for round := 0; ; round++ {
		page, next, err := f.st.ListServiceAccountTokens(ctx, f.owner.ID, f.svc.ID, 2, beforeID)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 || len(page) > 2 {
			t.Fatalf("round %d page size = %d", round, len(page))
		}
		for _, item := range page {
			if seen[item.ID] {
				t.Fatalf("duplicate token %d", item.ID)
			}
			seen[item.ID] = true
			collected = append(collected, item)
		}
		if next == nil {
			break
		}
		beforeID = *next
		if round > 10 {
			t.Fatal("cursor did not terminate")
		}
	}
	if len(seen) != 3 {
		t.Fatalf("walked %d of 3 tokens", len(seen))
	}
	for _, item := range collected {
		blob, marshalErr := json.Marshal(item)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		for _, secret := range plaintexts {
			if strings.Contains(string(blob), secret) {
				t.Fatal("token list exposed plaintext secret")
			}
		}
	}
	exact, exactCursor, err := f.st.ListServiceAccountTokens(ctx, f.owner.ID, f.svc.ID, 3, 0)
	if err != nil || len(exact) != 3 || exactCursor != nil {
		t.Fatalf("exact page = %d cursor=%v err=%v", len(exact), exactCursor, err)
	}
}
