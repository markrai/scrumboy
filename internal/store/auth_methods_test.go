package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDerivedAuthenticationMethodsUseConfiguredIssuer(t *testing.T) {
	st, _, cleanup := newTestStoreWithSQL(t)
	defer cleanup()
	st.configuredOIDCIssuer = "https://current.example"
	ctx := context.Background()
	local, err := st.BootstrapUser(ctx, "local@example.com", "Password123!", "Local")
	if err != nil {
		t.Fatal(err)
	}
	oidcOnly, err := st.CreateUserOIDC(ctx, st.configuredOIDCIssuer, st.configuredOIDCIssuer, "current-sub", "sso@example.com", "SSO")
	if err != nil {
		t.Fatal(err)
	}
	dual, err := st.CreateUser(ctx, "dual@example.com", "Password123!", "Dual")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.LinkOIDCIdentityExplicit(ctx, dual.ID, st.configuredOIDCIssuer, "dual-sub", dual.Email); err != nil {
		t.Fatal(err)
	}
	historical, err := st.CreateUser(ctx, "historical@example.com", "Password123!", "Historical")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.LinkOIDCIdentityExplicit(ctx, historical.ID, "https://old.example", "old-sub", historical.Email); err != nil {
		t.Fatal(err)
	}

	assertMethods := func(id int64, local, configured, any bool) {
		t.Helper()
		u, err := st.GetUser(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if u.HasLocalPassword != local || u.OIDCLinked != configured || u.HasAnyOIDCIdentity != any {
			t.Fatalf("user %d methods local=%v configured=%v any=%v", id, u.HasLocalPassword, u.OIDCLinked, u.HasAnyOIDCIdentity)
		}
	}
	assertMethods(local.ID, true, false, false)
	assertMethods(oidcOnly.ID, false, true, true)
	assertMethods(dual.ID, true, true, true)
	assertMethods(historical.ID, true, false, true)

	st.configuredOIDCIssuer = "https://different.example"
	u, err := st.GetUser(ctx, oidcOnly.ID)
	if err != nil {
		t.Fatal(err)
	}
	if u.OIDCLinked || !u.HasAnyOIDCIdentity {
		t.Fatalf("issuer change made historical identity appear usable: %+v", u)
	}
}

func TestPasswordHashEligibilityAndFirstPasswordCAS(t *testing.T) {
	for _, tc := range []struct {
		name string
		old  any
	}{{"null", nil}, {"empty", ""}, {"malformed", "not-bcrypt"}} {
		t.Run(tc.name, func(t *testing.T) {
			st, db, cleanup := newTestStoreWithSQL(t)
			defer cleanup()
			st.configuredOIDCIssuer = "https://idp.example"
			ctx := context.Background()
			u, err := st.CreateUserOIDC(ctx, st.configuredOIDCIssuer, st.configuredOIDCIssuer, "sub", "user@example.com", "User")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, tc.old, u.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := st.GetUserPasswordHash(ctx, u.ID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("unusable hash remained resettable: %v", err)
			}
			session, _, err := st.CreateSession(ctx, u.ID, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			grant, _, err := st.CreateFirstPasswordGrant(ctx, u.ID, session, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if err := st.SetFirstPassword(ctx, u.ID, grant, session, "NewPassword123!"); err != nil {
				t.Fatal(err)
			}
			got, err := st.GetUser(ctx, u.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !got.HasLocalPassword || !got.OIDCLinked {
				t.Fatalf("first password did not produce dual-auth account: %+v", got)
			}
			if _, err := st.AuthenticateUser(ctx, u.Email, "NewPassword123!"); err != nil {
				t.Fatalf("local login after first password: %v", err)
			}
			if _, err := st.GetUserByOIDCIdentity(ctx, st.configuredOIDCIssuer, "sub"); err != nil {
				t.Fatalf("OIDC identity lost: %v", err)
			}
			if n, _ := st.CountUsers(ctx); n != 1 {
				t.Fatalf("first password created %d users", n)
			}
		})
	}

	st, _, cleanup := newTestStoreWithSQL(t)
	defer cleanup()
	st.configuredOIDCIssuer = "https://idp.example"
	ctx := context.Background()
	u, _ := st.CreateUserOIDC(ctx, st.configuredOIDCIssuer, st.configuredOIDCIssuer, "valid-sub", "valid@example.com", "Valid")
	if err := st.UpdateUserPassword(ctx, u.ID, "ExistingPassword123!"); err != nil {
		t.Fatal(err)
	}
	session, _, _ := st.CreateSession(ctx, u.ID, time.Hour)
	grant, _, _ := st.CreateFirstPasswordGrant(ctx, u.ID, session, time.Minute)
	if err := st.SetFirstPassword(ctx, u.ID, grant, session, "Replacement123!"); !errors.Is(err, ErrConflict) {
		t.Fatalf("valid password was replaceable: %v", err)
	}
}

func TestOwnerRecoveryPostureUsesEffectiveMethods(t *testing.T) {
	st, db, cleanup := newTestStoreWithSQL(t)
	defer cleanup()
	st.configuredOIDCIssuer = "https://current.example"
	ctx := context.Background()
	owner, err := st.CreateUserOIDC(ctx, st.configuredOIDCIssuer, st.configuredOIDCIssuer, "owner-sub", "owner@example.com", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.OwnerRecoveryPosture(ctx, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if p.EffectiveOwnerCount != 1 || p.EffectiveSSOOwners != 1 || p.EffectiveLocalOwners != 0 || p.ProviderOnlyOwners != 1 {
		t.Fatalf("provider-only posture: %+v", p)
	}
	p, _ = st.OwnerRecoveryPosture(ctx, true, false)
	if p.EffectiveOwnerCount != 0 {
		t.Fatalf("disabled OIDC remained effective: %+v", p)
	}
	if _, err := db.ExecContext(ctx, `UPDATE users SET password_hash='malformed' WHERE id=?`, owner.ID); err != nil {
		t.Fatal(err)
	}
	p, _ = st.OwnerRecoveryPosture(ctx, false, false)
	if p.EffectiveOwnerCount != 0 {
		t.Fatalf("malformed/disabled methods remained effective: %+v", p)
	}
	st.configuredOIDCIssuer = "https://changed.example"
	p, _ = st.OwnerRecoveryPosture(ctx, true, true)
	if p.EffectiveOwnerCount != 0 {
		t.Fatalf("historical identity remained effective after issuer change: %+v", p)
	}
}

func TestOIDCLoginMetadataDoesNotChangeCanonicalOwnership(t *testing.T) {
	st, db, cleanup := newTestStoreWithSQL(t)
	defer cleanup()
	st.configuredOIDCIssuer = "https://idp.example"
	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "canonical@example.com", "Password123!", "Canonical Name")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.LinkOIDCIdentityExplicit(ctx, owner.ID, st.configuredOIDCIssuer, "subject", owner.Email); err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateUser(ctx, "collision@example.com", "Password123!", "Collision")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateOIDCIdentityEmailAtLogin(ctx, owner.ID, st.configuredOIDCIssuer, "subject", other.Email); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetUser(ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Email != "canonical@example.com" || got.Name != "Canonical Name" || got.SystemRole != SystemRoleOwner {
		t.Fatalf("OIDC metadata changed canonical account: %+v", got)
	}
	var emailAtLogin string
	if err := db.QueryRowContext(ctx, `SELECT email_at_login FROM user_oidc_identities WHERE user_id=? AND issuer=? AND subject=?`, owner.ID, st.configuredOIDCIssuer, "subject").Scan(&emailAtLogin); err != nil {
		t.Fatal(err)
	}
	if emailAtLogin != other.Email {
		t.Fatalf("email_at_login=%q want %q", emailAtLogin, other.Email)
	}
	if _, err := st.AuthenticateUser(ctx, owner.Email, "Password123!"); err != nil {
		t.Fatalf("canonical local login failed: %v", err)
	}
}

func TestResetLocalPasswordRevokesSessionsAndPreservesOIDC(t *testing.T) {
	st, db, cleanup := newTestStoreWithSQL(t)
	defer cleanup()
	st.configuredOIDCIssuer = "https://idp.example"
	ctx := context.Background()
	u, err := st.BootstrapUser(ctx, "dual@example.com", "OldPassword123!", "Dual")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.LinkOIDCIdentityExplicit(ctx, u.ID, st.configuredOIDCIssuer, "subject", u.Email); err != nil {
		t.Fatal(err)
	}
	if err := st.LinkOIDCIdentityExplicit(ctx, u.ID, "https://old.example", "old-subject", u.Email); err != nil {
		t.Fatal(err)
	}
	hash, err := st.GetUserPasswordHash(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	session, _, err := st.CreateSession(ctx, u.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.CreateLogin2FAPending(ctx, u.ID, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := st.ResetLocalPassword(ctx, u.ID, hash, "ResetPassword123!"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AuthenticateUser(ctx, u.Email, "ResetPassword123!"); err != nil {
		t.Fatalf("reset local login failed: %v", err)
	}
	if _, err := st.GetUserBySessionToken(ctx, session); !errors.Is(err, ErrNotFound) {
		t.Fatalf("session survived reset: %v", err)
	}
	var pending, identities int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM login_2fa_pending WHERE user_id=?`, u.ID).Scan(&pending)
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_oidc_identities WHERE user_id=?`, u.ID).Scan(&identities)
	if pending != 0 || identities != 2 {
		t.Fatalf("reset pending=%d identities=%d", pending, identities)
	}
}

func TestFirstPasswordGrantBindingExpiryAndSingleUse(t *testing.T) {
	st, db, cleanup := newTestStoreWithSQL(t)
	defer cleanup()
	st.configuredOIDCIssuer = "https://idp.example"
	ctx := context.Background()
	u1, _ := st.CreateUserOIDC(ctx, st.configuredOIDCIssuer, st.configuredOIDCIssuer, "sub-1", "one@example.com", "One")
	u2, _ := st.CreateUserOIDC(ctx, st.configuredOIDCIssuer, st.configuredOIDCIssuer, "sub-2", "two@example.com", "Two")
	session1, _, _ := st.CreateSession(ctx, u1.ID, time.Hour)
	session2, _, _ := st.CreateSession(ctx, u2.ID, time.Hour)
	grant, _, err := st.CreateFirstPasswordGrant(ctx, u1.ID, session1, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetFirstPassword(ctx, u2.ID, grant, session2, "Password123!"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("grant substituted to another user: %v", err)
	}
	if err := st.SetFirstPassword(ctx, u1.ID, grant, session2, "Password123!"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("grant substituted to another session: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE first_password_grants SET expires_at=0 WHERE token_hash=?`, hashToken(grant)); err != nil {
		t.Fatal(err)
	}
	if err := st.SetFirstPassword(ctx, u1.ID, grant, session1, "Password123!"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expired grant accepted: %v", err)
	}
	grant, _, _ = st.CreateFirstPasswordGrant(ctx, u1.ID, session1, time.Minute)
	if err := st.SetFirstPassword(ctx, u1.ID, grant, session1, "Password123!"); err != nil {
		t.Fatal(err)
	}
	valid, err := st.FirstPasswordGrantValid(ctx, grant, session1, u1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if valid {
		t.Fatal("consumed grant remained valid")
	}
}

func TestFirstPasswordFailureDoesNotConsumeRecoveryCode(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	ctx := context.Background()
	u, err := st.CreateUserOIDC(ctx, "https://issuer.example", "https://issuer.example", "recovery-user", "recovery@example.com", "Recovery")
	if err != nil {
		t.Fatalf("CreateUserOIDC: %v", err)
	}
	session, _, err := st.CreateSession(ctx, u.ID, time.Hour)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	grant, _, err := st.CreateFirstPasswordGrant(ctx, u.ID, session, time.Minute)
	if err != nil {
		t.Fatalf("CreateFirstPasswordGrant: %v", err)
	}
	const recoveryCode = "ABCD-EFGH"
	if err := st.AddRecoveryCodes(ctx, u.ID, []string{recoveryCode}); err != nil {
		t.Fatalf("AddRecoveryCodes: %v", err)
	}
	recoveryCodeID, err := st.MatchRecoveryCode(ctx, u.ID, recoveryCode)
	if err != nil || recoveryCodeID == 0 {
		t.Fatalf("MatchRecoveryCode: id=%d err=%v", recoveryCodeID, err)
	}
	if err := st.SetFirstPasswordWithRecoveryCode(ctx, u.ID, "wrong-grant", session, "NewPassword123!", recoveryCodeID); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong grant error = %v, want ErrUnauthorized", err)
	}
	consumed, err := st.ConsumeRecoveryCode(ctx, u.ID, recoveryCode)
	if err != nil {
		t.Fatalf("ConsumeRecoveryCode after failed transition: %v", err)
	}
	if !consumed {
		t.Fatal("recovery code was consumed by a failed first-password transition")
	}

	const secondCode = "JKLM-NPQR"
	if err := st.AddRecoveryCodes(ctx, u.ID, []string{secondCode}); err != nil {
		t.Fatalf("AddRecoveryCodes second code: %v", err)
	}
	secondID, err := st.MatchRecoveryCode(ctx, u.ID, secondCode)
	if err != nil || secondID == 0 {
		t.Fatalf("MatchRecoveryCode second code: id=%d err=%v", secondID, err)
	}
	if err := st.SetFirstPasswordWithRecoveryCode(ctx, u.ID, grant, session, "weak", secondID); err == nil {
		t.Fatal("weak first password unexpectedly succeeded")
	}
	consumed, err = st.ConsumeRecoveryCode(ctx, u.ID, secondCode)
	if err != nil || !consumed {
		t.Fatalf("weak password consumed recovery code: consumed=%v err=%v", consumed, err)
	}
}
