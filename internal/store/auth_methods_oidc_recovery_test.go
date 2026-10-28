package store

import (
	"context"
	"errors"
	"sync"
	"testing"
)

const oidcRecoveryIssuer = "https://idp.example"

func enrollOIDCRecoveryUser(t *testing.T, st *Store, email string) (User, []string) {
	t.Helper()
	st.configuredOIDCIssuer = oidcRecoveryIssuer
	u, err := st.BootstrapUser(context.Background(), email, "Password123!", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	token := seedTwoFactorEnrollment(t, st, u.ID)
	codes := GenerateRecoveryCodes(RecoveryCodeCount)
	if err := st.CompleteTwoFactorEnrollment(context.Background(), u.ID, token, codes); err != nil {
		t.Fatalf("CompleteTwoFactorEnrollment: %v", err)
	}
	return u, codes
}

func assertOIDCIdentityOwner(t *testing.T, st *Store, issuer, subject string, wantUserID int64) {
	t.Helper()
	linked, err := st.GetUserByOIDCIdentity(context.Background(), issuer, subject)
	if err != nil {
		t.Fatalf("GetUserByOIDCIdentity: %v", err)
	}
	if linked.ID != wantUserID {
		t.Fatalf("linked user id=%d, want %d", linked.ID, wantUserID)
	}
}

func assertOIDCIdentityMissing(t *testing.T, st *Store, issuer, subject string) {
	t.Helper()
	if _, err := st.GetUserByOIDCIdentity(context.Background(), issuer, subject); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetUserByOIDCIdentity err=%v, want ErrNotFound", err)
	}
}

func TestLinkOIDCIdentityExplicitDoesNotConsumeRecoveryCodes(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	u, codes := enrollOIDCRecoveryUser(t, st, "explicit-no-consume@x.com")
	ctx := context.Background()

	if err := st.LinkOIDCIdentityExplicit(ctx, u.ID, oidcRecoveryIssuer, "sub-explicit", u.Email); err != nil {
		t.Fatalf("LinkOIDCIdentityExplicit: %v", err)
	}
	assertOIDCIdentityOwner(t, st, oidcRecoveryIssuer, "sub-explicit", u.ID)

	consumed, err := st.ConsumeRecoveryCode(ctx, u.ID, codes[0])
	if err != nil {
		t.Fatalf("ConsumeRecoveryCode: %v", err)
	}
	if !consumed {
		t.Fatal("explicit link without proof consumed a recovery code")
	}
}

func TestLinkOIDCIdentityExplicitWithRecoveryProofConsumesOnce(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	u, codes := enrollOIDCRecoveryUser(t, st, "link-consume-once@x.com")
	ctx := context.Background()
	proof := matchRecoveryProof(t, st, u.ID, codes[0])

	if err := st.LinkOIDCIdentityExplicitWithRecoveryProof(ctx, u.ID, oidcRecoveryIssuer, "sub-once", u.Email, proof); err != nil {
		t.Fatalf("LinkOIDCIdentityExplicitWithRecoveryProof: %v", err)
	}
	assertOIDCIdentityOwner(t, st, oidcRecoveryIssuer, "sub-once", u.ID)

	consumed, err := st.ConsumeRecoveryCode(ctx, u.ID, codes[0])
	if err != nil {
		t.Fatalf("ConsumeRecoveryCode used proof: %v", err)
	}
	if consumed {
		t.Fatal("successful link left the recovery proof unused")
	}
	err = st.LinkOIDCIdentityExplicitWithRecoveryProof(ctx, u.ID, oidcRecoveryIssuer, "sub-once-replay", u.Email, proof)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("replay proof err=%v, want ErrUnauthorized", err)
	}
	assertOIDCIdentityMissing(t, st, oidcRecoveryIssuer, "sub-once-replay")

	second, err := st.ConsumeRecoveryCode(ctx, u.ID, codes[1])
	if err != nil {
		t.Fatalf("ConsumeRecoveryCode sibling: %v", err)
	}
	if !second {
		t.Fatal("unrelated recovery code was consumed by the link")
	}
}

func TestLinkOIDCIdentityExplicitWithRecoveryProofEmailMismatchLeavesCodeUnused(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	u, codes := enrollOIDCRecoveryUser(t, st, "link-email-mismatch@x.com")
	ctx := context.Background()
	proof := matchRecoveryProof(t, st, u.ID, codes[0])

	err := st.LinkOIDCIdentityExplicitWithRecoveryProof(ctx, u.ID, oidcRecoveryIssuer, "sub-mismatch", "other@x.com", proof)
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("email mismatch err=%v, want ErrValidation", err)
	}
	assertOIDCIdentityMissing(t, st, oidcRecoveryIssuer, "sub-mismatch")
	consumed, err := st.ConsumeRecoveryCode(ctx, u.ID, codes[0])
	if err != nil {
		t.Fatalf("ConsumeRecoveryCode: %v", err)
	}
	if !consumed {
		t.Fatal("email mismatch consumed the recovery code")
	}
}

func TestLinkOIDCIdentityExplicitWithRecoveryProofConflictLeavesCodeUnused(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	st.configuredOIDCIssuer = oidcRecoveryIssuer
	ctx := context.Background()
	owner, codes := enrollOIDCRecoveryUser(t, st, "link-conflict-owner@x.com")
	other, err := st.CreateUser(ctx, "link-conflict-other@x.com", "Password123!", "Other")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := st.LinkOIDCIdentityExplicit(ctx, other.ID, oidcRecoveryIssuer, "shared-sub", other.Email); err != nil {
		t.Fatalf("seed identity: %v", err)
	}
	proof := matchRecoveryProof(t, st, owner.ID, codes[0])

	err = st.LinkOIDCIdentityExplicitWithRecoveryProof(ctx, owner.ID, oidcRecoveryIssuer, "shared-sub", owner.Email, proof)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("identity conflict err=%v, want ErrConflict", err)
	}
	assertOIDCIdentityOwner(t, st, oidcRecoveryIssuer, "shared-sub", other.ID)
	consumed, err := st.ConsumeRecoveryCode(ctx, owner.ID, codes[0])
	if err != nil {
		t.Fatalf("ConsumeRecoveryCode: %v", err)
	}
	if !consumed {
		t.Fatal("identity conflict consumed the recovery code")
	}
}

func TestLinkOIDCIdentityExplicitWithRecoveryProofInsertFailureRollsBackConsume(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	u, codes := enrollOIDCRecoveryUser(t, st, "link-insert-fail@x.com")
	ctx := context.Background()
	proof := matchRecoveryProof(t, st, u.ID, codes[0])

	if _, err := st.db.Exec(`CREATE TRIGGER fail_oidc_identity BEFORE INSERT ON user_oidc_identities BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
		t.Fatalf("install trigger: %v", err)
	}
	err := st.LinkOIDCIdentityExplicitWithRecoveryProof(ctx, u.ID, oidcRecoveryIssuer, "sub-fail", u.Email, proof)
	if err == nil {
		t.Fatal("expected insert failure")
	}
	if errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrValidation) || errors.Is(err, ErrConflict) {
		t.Fatalf("insert failure was classified as a caller error: %v", err)
	}
	assertOIDCIdentityMissing(t, st, oidcRecoveryIssuer, "sub-fail")
	consumed, err := st.ConsumeRecoveryCode(ctx, u.ID, codes[0])
	if err != nil {
		t.Fatalf("ConsumeRecoveryCode after rollback: %v", err)
	}
	if !consumed {
		t.Fatal("insert failure consumed the recovery code")
	}
}

func TestLinkOIDCIdentityExplicitWithRecoveryProofRejectsStaleProofAfterRowIDReuse(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	u, original := enrollOIDCRecoveryUser(t, st, "link-stale-id@x.com")
	ctx := context.Background()
	stale := matchRecoveryProof(t, st, u.ID, original[1])
	current := matchRecoveryProof(t, st, u.ID, original[0])

	replacement := GenerateRecoveryCodes(RecoveryCodeCount)
	if err := st.ReplaceRecoveryCodes(ctx, u.ID, replacement, current); err != nil {
		t.Fatalf("ReplaceRecoveryCodes: %v", err)
	}
	ids := recoveryCodeIDs(t, st, u.ID)
	if !containsRecoveryID(ids, stale.ID) {
		t.Fatalf("replacement ids %v did not reuse stale proof id %d", ids, stale.ID)
	}

	err := st.LinkOIDCIdentityExplicitWithRecoveryProof(ctx, u.ID, oidcRecoveryIssuer, "sub-stale", u.Email, stale)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("stale proof err=%v, want ErrUnauthorized", err)
	}
	assertOIDCIdentityMissing(t, st, oidcRecoveryIssuer, "sub-stale")
	if !hashesMatchCodeSet(recoveryCodeHashes(t, st, u.ID), replacement) {
		t.Fatal("stale proof mutated replacement recovery codes")
	}
	consumed, err := st.ConsumeRecoveryCode(ctx, u.ID, replacement[0])
	if err != nil {
		t.Fatalf("ConsumeRecoveryCode replacement: %v", err)
	}
	if !consumed {
		t.Fatal("stale proof consumed a replacement recovery code")
	}
}

func TestLinkOIDCIdentityExplicitWithRecoveryProofConcurrentSameProof(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	u, codes := enrollOIDCRecoveryUser(t, st, "link-concurrent@x.com")
	ctx := context.Background()
	proof := matchRecoveryProof(t, st, u.ID, codes[0])

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- st.LinkOIDCIdentityExplicitWithRecoveryProof(ctx, u.ID, oidcRecoveryIssuer, "sub-concurrent", u.Email, proof)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	var succeeded, unauthorized, other int
	for err := range errs {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrUnauthorized):
			unauthorized++
		default:
			other++
			t.Errorf("unexpected concurrent error: %v", err)
		}
	}
	if succeeded != 1 || unauthorized != 1 || other != 0 {
		t.Fatalf("concurrent results succeeded=%d unauthorized=%d other=%d, want 1/1/0", succeeded, unauthorized, other)
	}
	assertOIDCIdentityOwner(t, st, oidcRecoveryIssuer, "sub-concurrent", u.ID)
	consumed, err := st.ConsumeRecoveryCode(ctx, u.ID, codes[0])
	if err != nil {
		t.Fatalf("ConsumeRecoveryCode: %v", err)
	}
	if consumed {
		t.Fatal("winning concurrent link left the recovery proof unused")
	}
}

func TestLinkOIDCIdentityExplicitWithRecoveryProofRejectsIncompleteProof(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	u, codes := enrollOIDCRecoveryUser(t, st, "link-incomplete-proof@x.com")
	ctx := context.Background()
	proof := matchRecoveryProof(t, st, u.ID, codes[0])

	err := st.LinkOIDCIdentityExplicitWithRecoveryProof(ctx, u.ID, oidcRecoveryIssuer, "sub-id-only", u.Email, RecoveryCodeProof{ID: proof.ID})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("id-only proof err=%v, want ErrUnauthorized", err)
	}
	assertOIDCIdentityMissing(t, st, oidcRecoveryIssuer, "sub-id-only")
	consumed, err := st.ConsumeRecoveryCode(ctx, u.ID, codes[0])
	if err != nil {
		t.Fatalf("ConsumeRecoveryCode: %v", err)
	}
	if !consumed {
		t.Fatal("id-only proof consumed the recovery code")
	}
}
