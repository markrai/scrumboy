package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func strp(s string) *string { return &s }

func TestUpdateUserProfile(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	owner, err := st.BootstrapUser(ctx, "owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := st.CreateUser(ctx, "admin@test.com", "password123", "Admin")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateUserRole(ctx, owner.ID, admin.ID, SystemRoleAdmin); err != nil {
		t.Fatal(err)
	}
	target, err := st.CreateUser(ctx, "mars@test.com", "password123", "Mars Yeen")
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateUser(ctx, "other@test.com", "password123", "Other")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := st.CreateServiceAccount(ctx, owner.ID, "ci-bot")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("owner changes name and email, normalized", func(t *testing.T) {
		if err := st.UpdateUserProfile(ctx, owner.ID, target.ID, strp("  Lynix Wolf "), strp("  Lynix@Test.COM ")); err != nil {
			t.Fatalf("UpdateUserProfile: %v", err)
		}
		got, err := st.GetUser(ctx, target.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Name != "Lynix Wolf" || got.Email != "lynix@test.com" {
			t.Fatalf("got name=%q email=%q", got.Name, got.Email)
		}
		if got.SystemRole != SystemRoleUser {
			t.Fatalf("role changed to %q", got.SystemRole)
		}
	})

	t.Run("name only leaves email alone", func(t *testing.T) {
		if err := st.UpdateUserProfile(ctx, owner.ID, other.ID, strp("Other Name"), nil); err != nil {
			t.Fatal(err)
		}
		got, _ := st.GetUser(ctx, other.ID)
		if got.Name != "Other Name" || got.Email != "other@test.com" {
			t.Fatalf("got name=%q email=%q", got.Name, got.Email)
		}
	})

	t.Run("old email is free again and the user can still log in", func(t *testing.T) {
		if _, err := st.CreateUser(ctx, "mars@test.com", "password123", "New Mars"); err != nil {
			t.Fatalf("old address should be reusable: %v", err)
		}
		if _, err := st.AuthenticateUser(ctx, "lynix@test.com", "password123"); err != nil {
			t.Fatalf("login with new email: %v", err)
		}
	})

	t.Run("duplicate email is a conflict and changes nothing", func(t *testing.T) {
		err := st.UpdateUserProfile(ctx, owner.ID, other.ID, strp("Should Not Apply"), strp("LYNIX@test.com"))
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("err = %v, want ErrConflict", err)
		}
		got, _ := st.GetUser(ctx, other.ID)
		if got.Name != "Other Name" || got.Email != "other@test.com" {
			t.Fatalf("partial write: name=%q email=%q", got.Name, got.Email)
		}
	})

	t.Run("non-owners are refused", func(t *testing.T) {
		if err := st.UpdateUserProfile(ctx, admin.ID, other.ID, strp("X"), nil); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("admin: err = %v, want ErrUnauthorized", err)
		}
		if err := st.UpdateUserProfile(ctx, other.ID, other.ID, strp("X"), nil); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("self-service user: err = %v, want ErrUnauthorized", err)
		}
	})

	t.Run("service accounts and missing users are refused", func(t *testing.T) {
		if err := st.UpdateUserProfile(ctx, owner.ID, svc.ID, strp("renamed"), nil); !errors.Is(err, ErrServiceAccountRestricted) {
			t.Fatalf("service account: err = %v, want ErrServiceAccountRestricted", err)
		}
		if err := st.UpdateUserProfile(ctx, owner.ID, 999999, strp("x"), nil); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing user: err = %v, want ErrNotFound", err)
		}
	})

	t.Run("validation", func(t *testing.T) {
		for name, tc := range map[string]struct{ name, email *string }{
			"nothing":           {nil, nil},
			"blank name":        {strp("   "), nil},
			"long name":         {strp(strings.Repeat("a", maxUserNameRunes+1)), nil},
			"blank email":       {nil, strp("  ")},
			"no at sign":        {nil, strp("nope")},
			"service-account":   {nil, strp("bot@" + serviceAccountEmailDomain)},
			"service subdomain": {nil, strp("bot@x." + serviceAccountEmailDomain)},
		} {
			if err := st.UpdateUserProfile(ctx, owner.ID, other.ID, tc.name, tc.email); !errors.Is(err, ErrValidation) {
				t.Errorf("%s: err = %v, want ErrValidation", name, err)
			}
		}
	})
}

// UpdateUserProfile is the first code path that changes users.email after creation, and that column
// is the local sign-in key, the self-service recovery lookup, and the canonical address explicit OIDC
// linking is checked against. Everything else (sessions, OIDC links, 2FA, recovery codes) must keep
// following the user id.
func TestUpdateUserProfilePreservesAuthLifecycle(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	ctx := context.Background()
	const issuer = "https://idp.example.test"

	owner, err := st.BootstrapUser(ctx, "owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	target, err := st.CreateUser(ctx, "old@test.com", "password123", "Old Name")
	if err != nil {
		t.Fatal(err)
	}
	sessionToken, _, err := st.CreateSession(ctx, target.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.LinkOIDCIdentity(ctx, target.ID, issuer, "subject-target", "old@test.com"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserTwoFactor(ctx, target.ID, "encrypted-secret"); err != nil {
		t.Fatal(err)
	}
	if err := st.AddRecoveryCodes(ctx, target.ID, []string{"ABCD-EFGH"}); err != nil {
		t.Fatal(err)
	}

	// A name-only change leaves the sign-in key alone.
	if err := st.UpdateUserProfile(ctx, owner.ID, target.ID, strp("Interim Name"), nil); err != nil {
		t.Fatal(err)
	}
	if u, err := st.AuthenticateUser(ctx, "old@test.com", "password123"); err != nil || u.ID != target.ID {
		t.Fatalf("login after name-only change: user=%d err=%v", u.ID, err)
	}

	if err := st.UpdateUserProfile(ctx, owner.ID, target.ID, nil, strp("New@Test.com")); err != nil {
		t.Fatal(err)
	}

	// Local sign-in moves to the new address with the same password and account.
	if u, err := st.AuthenticateUser(ctx, "new@test.com", "password123"); err != nil || u.ID != target.ID {
		t.Fatalf("login with new email: user=%d err=%v", u.ID, err)
	}
	if _, err := st.AuthenticateUser(ctx, "old@test.com", "password123"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("login with old email: err=%v, want ErrUnauthorized", err)
	}
	if _, err := st.GetUserByEmail(ctx, "old@test.com"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("recovery lookup by old email: err=%v, want ErrNotFound", err)
	}

	// Existing sessions stay with the same account.
	if u, err := st.GetUserBySessionToken(ctx, sessionToken); err != nil || u.ID != target.ID || u.Email != "new@test.com" {
		t.Fatalf("session after email change: user=%+v err=%v", u, err)
	}

	// The existing OIDC link still resolves by (issuer, subject), and an IdP still reporting the old
	// address only refreshes the identity's metadata, not the administrator's canonical email.
	if u, err := st.GetUserByOIDCIdentity(ctx, issuer, "subject-target"); err != nil || u.ID != target.ID {
		t.Fatalf("linked OIDC identity: user=%d err=%v", u.ID, err)
	}
	if err := st.UpdateOIDCIdentityEmailAtLogin(ctx, target.ID, issuer, "subject-target", "old@test.com"); err != nil {
		t.Fatal(err)
	}
	if u, _ := st.GetUser(ctx, target.ID); u.Email != "new@test.com" {
		t.Fatalf("IdP login metadata overwrote canonical email: %q", u.Email)
	}

	// An unlinked IdP identity asserting the new address is neither attached to the account nor
	// allowed to create a second account holding it.
	if _, err := st.CreateUserOIDC(ctx, issuer, issuer, "subject-stranger", "new@test.com", "Stranger"); !errors.Is(err, ErrConflict) {
		t.Fatalf("OIDC signup with reassigned email: err=%v, want ErrConflict", err)
	}
	if _, err := st.GetUserByOIDCIdentity(ctx, issuer, "subject-stranger"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stranger identity was linked: err=%v", err)
	}

	// Explicit linking is checked against the current canonical email.
	if err := st.LinkOIDCIdentityExplicit(ctx, target.ID, issuer, "subject-second", "old@test.com"); !errors.Is(err, ErrValidation) {
		t.Fatalf("explicit link with old email: err=%v, want ErrValidation", err)
	}
	if err := st.LinkOIDCIdentityExplicit(ctx, target.ID, issuer, "subject-second", "new@test.com"); err != nil {
		t.Fatalf("explicit link with new email: %v", err)
	}

	// 2FA enrollment and recovery codes stay with the account.
	if u, _ := st.GetUser(ctx, target.ID); !u.TwoFactorEnabled {
		t.Fatal("2FA enrollment lost after email change")
	}
	if id, _, err := st.MatchRecoveryCodeProof(ctx, target.ID, "ABCD-EFGH"); err != nil || id <= 0 {
		t.Fatalf("recovery code after email change: id=%d err=%v", id, err)
	}

	// Re-applying the current values succeeds, so a client retry after an ambiguous response is safe.
	if err := st.UpdateUserProfile(ctx, owner.ID, target.ID, strp("Interim Name"), strp("new@test.com")); err != nil {
		t.Fatalf("same-value update: %v", err)
	}
}
