package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"scrumboy/internal/db"
)

var serviceEmailPattern = regexp.MustCompile(`^svc-[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}@service\.invalid$`)

const (
	serviceTestIssuer   = "https://issuer.example"
	serviceTestResource = "https://scrumboy.example/mcp/rpc"
	serviceTestRedirect = "https://client.example/callback"
	serviceTestPassword = "CorrectHorse123!"
)

type serviceFixture struct {
	st    *Store
	db    *sql.DB
	owner User
	admin User
	human User
	svc   User
}

func newServiceFixture(t *testing.T) *serviceFixture {
	t.Helper()
	st, cleanup := newTestStoreWith2FA(t)
	t.Cleanup(cleanup)
	st.configuredOIDCIssuer = serviceTestIssuer
	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "owner@example.com", serviceTestPassword, "Owner")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	admin, err := st.CreateUser(ctx, "admin@example.com", serviceTestPassword, "Admin")
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if err := st.UpdateUserRole(ctx, owner.ID, admin.ID, SystemRoleAdmin); err != nil {
		t.Fatalf("promote admin: %v", err)
	}
	human, err := st.CreateUser(ctx, "human@example.com", serviceTestPassword, "Human")
	if err != nil {
		t.Fatalf("create human: %v", err)
	}
	svc, err := st.CreateServiceAccount(ctx, owner.ID, "Bot")
	if err != nil {
		t.Fatalf("create service account: %v", err)
	}
	return &serviceFixture{st: st, db: st.db, owner: owner, admin: admin, human: human, svc: svc}
}

func (f *serviceFixture) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// assertNoInteractiveState fails if the service account has any interactive credential or has
// drifted from its identity invariants.
func (f *serviceFixture) assertNoInteractiveState(t *testing.T) {
	t.Helper()
	for _, table := range []string{
		"sessions", "user_oidc_identities", "login_2fa_pending", "two_factor_enrollments",
		"user_recovery_codes", "first_password_grants", "oauth_auth_codes", "oauth_access_tokens",
		"oauth_refresh_tokens", "mobile_oidc_handoff_grants",
	} {
		if n := f.count(t, `SELECT COUNT(*) FROM `+table+` WHERE user_id = ?`, f.svc.ID); n != 0 {
			t.Errorf("%d %s rows reference the service account", n, table)
		}
	}
	if n := f.count(t, `SELECT COUNT(*) FROM users WHERE id = ? AND is_service = 1 AND system_role = 'user' AND is_bootstrap = 0 AND password_hash IS NULL AND two_factor_enabled = 0 AND two_factor_secret_enc IS NULL`, f.svc.ID); n != 1 {
		t.Errorf("service account row no longer satisfies its invariants")
	}
}

// dropCredentialTriggers simulates records that exist despite the schema, such as a hand-edited or
// restored database, so that the read-side guards are tested on their own.
func (f *serviceFixture) dropCredentialTriggers(t *testing.T) {
	t.Helper()
	rows, err := f.db.Query(`SELECT name FROM sqlite_master WHERE type = 'trigger' AND name LIKE '%_reject_service_insert'`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	_ = rows.Close()
	if len(names) != 10 {
		t.Fatalf("found %d credential triggers, want 10", len(names))
	}
	for _, name := range names {
		if _, err := f.db.Exec(`DROP TRIGGER ` + name); err != nil {
			t.Fatal(err)
		}
	}
}

// corrupt runs writes that the schema normally forbids, with its CHECK constraints off and the
// immutability trigger removed, to produce malformed identity states.
func (f *serviceFixture) corrupt(t *testing.T, stmts ...string) {
	t.Helper()
	if _, err := f.db.Exec(`DROP TRIGGER IF EXISTS trg_users_is_service_immutable`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`PRAGMA ignore_check_constraints = ON`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := f.db.Exec(`PRAGMA ignore_check_constraints = OFF`); err != nil {
			t.Fatal(err)
		}
	}()
	for _, stmt := range stmts {
		if _, err := f.db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

func recoveryCodes() []string {
	return []string{"AAAA-AAA1", "AAAA-AAA2", "AAAA-AAA3", "AAAA-AAA4", "AAAA-AAA5", "AAAA-AAA6", "AAAA-AAA7", "AAAA-AAA8"}
}

func TestCreateServiceAccount_ProducesValidIdentity(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()

	if !f.svc.IsService || f.svc.SystemRole != SystemRoleUser || f.svc.IsBootstrap || f.svc.Name != "Bot" {
		t.Fatalf("returned identity = %+v", f.svc)
	}
	if !publicIDPattern.MatchString(f.svc.PublicID) {
		t.Fatalf("PublicID = %q, want a UUIDv4", f.svc.PublicID)
	}
	if !serviceEmailPattern.MatchString(f.svc.Email) {
		t.Fatalf("Email = %q, want a placeholder under service.invalid", f.svc.Email)
	}

	got, err := f.st.GetUser(ctx, f.svc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsService || got.PublicID != f.svc.PublicID || got.Email != f.svc.Email || got.SystemRole != SystemRoleUser {
		t.Fatalf("reloaded identity = %+v, created = %+v", got, f.svc)
	}
	if got.HasLocalPassword || got.HasAnyOIDCIdentity || got.OIDCLinked || got.IsTwoFactorActive() {
		t.Fatalf("service account reports a sign-in method: %+v", got)
	}
	f.assertNoInteractiveState(t)

	// Humans are unchanged by the model.
	human, err := f.st.GetUser(ctx, f.human.ID)
	if err != nil {
		t.Fatal(err)
	}
	if human.IsService || !human.HasLocalPassword {
		t.Fatalf("human identity = %+v", human)
	}
}

func TestCreateServiceAccount_Authorization(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()
	before := f.count(t, `SELECT COUNT(*) FROM users`)

	for name, tc := range map[string]struct {
		requester int64
		want      error
	}{
		"plain user":      {f.human.ID, ErrUnauthorized},
		"service account": {f.svc.ID, ErrUnauthorized},
		"missing user":    {999999, ErrNotFound},
	} {
		if _, err := f.st.CreateServiceAccount(ctx, tc.requester, "Nope"); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
	}
	for _, blank := range []string{"", "   "} {
		if _, err := f.st.CreateServiceAccount(ctx, f.owner.ID, blank); !errors.Is(err, ErrValidation) {
			t.Errorf("name %q: err = %v, want ErrValidation", blank, err)
		}
	}
	if got := f.count(t, `SELECT COUNT(*) FROM users`); got != before {
		t.Fatalf("rejected creations changed the user count from %d to %d", before, got)
	}

	if _, err := f.st.CreateServiceAccount(ctx, f.owner.ID, "By owner"); err != nil {
		t.Errorf("owner: %v", err)
	}
	if _, err := f.st.CreateServiceAccount(ctx, f.admin.ID, "By admin"); err != nil {
		t.Errorf("admin: %v", err)
	}
}

// A service account skips everything a human's creation seeds, even when the org has configured
// defaults that a human created at the same moment does receive.
func TestCreateServiceAccount_SeedsNothing(t *testing.T) {
	f := newServiceFixture(t)
	ctx := WithUserID(context.Background(), f.owner.ID)

	board, err := f.st.CreateProject(ctx, "Default board")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.SetDefaultBoardOrgSetting(ctx, f.owner.ID, board.ID, RoleContributor); err != nil {
		t.Fatal(err)
	}
	if err := f.st.SetEmailNotifyOrgDefault(ctx, f.owner.ID, `{"assigned":true}`); err != nil {
		t.Fatal(err)
	}

	svc, err := f.st.CreateServiceAccount(ctx, f.owner.ID, "Fresh bot")
	if err != nil {
		t.Fatal(err)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM project_members WHERE user_id = ?`, svc.ID); n != 0 {
		t.Fatalf("service account got %d project memberships", n)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM user_preferences WHERE user_id = ?`, svc.ID); n != 0 {
		t.Fatalf("service account got %d seeded preferences", n)
	}

	human, err := f.st.CreateUser(ctx, "seeded@example.com", serviceTestPassword, "Seeded")
	if err != nil {
		t.Fatal(err)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM project_members WHERE user_id = ?`, human.ID); n != 1 {
		t.Fatalf("human creation seeding regressed: %d memberships", n)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM user_preferences WHERE user_id = ?`, human.ID); n != 1 {
		t.Fatalf("human creation seeding regressed: %d preferences", n)
	}
}

func TestServiceAccount_NeverBecomesOwnerOrAdmin(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()

	for _, role := range []SystemRole{SystemRoleOwner, SystemRoleAdmin} {
		err := f.st.UpdateUserRole(ctx, f.owner.ID, f.svc.ID, role)
		if !errors.Is(err, ErrServiceAccountRestricted) || !errors.Is(err, ErrForbidden) {
			t.Errorf("promote to %s: err = %v, want ErrServiceAccountRestricted", role, err)
		}
	}
	if err := f.st.UpdateUserRole(ctx, f.owner.ID, f.svc.ID, SystemRoleUser); err != nil {
		t.Errorf("a no-op role write: %v", err)
	}
	// It cannot authorize a role change either, including on itself.
	if err := f.st.UpdateUserRole(ctx, f.svc.ID, f.svc.ID, SystemRoleOwner); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("self-promotion: err = %v, want ErrUnauthorized", err)
	}
	if err := f.st.UpdateUserRole(ctx, f.svc.ID, f.human.ID, SystemRoleAdmin); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("service account promoting a human: err = %v, want ErrUnauthorized", err)
	}
	if _, err := f.st.ListUsers(ctx, f.svc.ID); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("ListUsers as service account: err = %v, want ErrUnauthorized", err)
	}
	if err := f.st.DeleteUser(ctx, f.svc.ID, f.human.ID); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("DeleteUser as service account: err = %v, want ErrUnauthorized", err)
	}
	f.assertNoInteractiveState(t)
	if n, err := f.st.countOwners(ctx); err != nil || n != 1 {
		t.Fatalf("owners = %d, err = %v", n, err)
	}
}

// Every store method that issues interactive state or a credential refuses a service account.
// These are direct store calls, so no HTTP-layer check can be doing the work.
func TestServiceAccount_StoreMethodsRefuseInteractiveCredentials(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()
	id := f.svc.ID

	secretEnc, err := f.st.EncryptTOTPSecret([]byte("JBSWY3DPEHPK3PXP"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.CreateOAuthClient(ctx, "client", "Client", serviceTestRedirect); err != nil {
		t.Fatal(err)
	}
	if err := f.st.CreateMobileOIDCFlow(ctx, "state", "challenge", "/", time.Minute); err != nil {
		t.Fatal(err)
	}

	restricted := map[string]func() error{
		"CreateSession": func() error { _, _, err := f.st.CreateSession(ctx, id, time.Hour); return err },
		"CreateLogin2FAPending": func() error {
			_, _, err := f.st.CreateLogin2FAPending(ctx, id, time.Minute)
			return err
		},
		"CreateTwoFactorEnrollment": func() error {
			_, _, err := f.st.CreateTwoFactorEnrollment(ctx, id, secretEnc, time.Minute)
			return err
		},
		"AddRecoveryCodes": func() error { return f.st.AddRecoveryCodes(ctx, id, recoveryCodes()) },
		"LinkOIDCIdentity": func() error { return f.st.LinkOIDCIdentity(ctx, id, serviceTestIssuer, "sub", f.svc.Email) },
		"CreateOAuthAuthCode": func() error {
			_, err := f.st.CreateOAuthAuthCode(ctx, "client", id, serviceTestRedirect, "challenge", "S256", serviceTestResource)
			return err
		},
		"IssueOAuthTokenPair": func() error {
			_, err := f.st.IssueOAuthTokenPair(ctx, "client", id, serviceTestResource)
			return err
		},
		"CreateMobileOIDCHandoffGrant": func() error {
			_, _, _, err := f.st.CreateMobileOIDCHandoffGrant(ctx, "state", id, time.Minute)
			return err
		},
		"CreateUserAPIToken": func() error {
			_, _, _, err := f.st.CreateUserAPIToken(ctx, id, nil, false)
			return err
		},
	}
	for name, call := range restricted {
		if err := call(); !errors.Is(err, ErrServiceAccountRestricted) {
			t.Errorf("%s: err = %v, want ErrServiceAccountRestricted", name, err)
		}
	}

	hash, err := bcrypt.GenerateFromPassword([]byte("x"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	rejected := map[string]struct {
		call func() error
		want error
	}{
		"SetUserTwoFactor":   {func() error { return f.st.SetUserTwoFactor(ctx, id, secretEnc) }, ErrNotFound},
		"UpdateUserPassword": {func() error { return f.st.UpdateUserPassword(ctx, id, serviceTestPassword) }, ErrNotFound},
		"ResetLocalPassword": {func() error { return f.st.ResetLocalPassword(ctx, id, string(hash), serviceTestPassword) }, ErrConflict},
		"LinkOIDCIdentityExplicit": {func() error {
			return f.st.LinkOIDCIdentityExplicit(ctx, id, serviceTestIssuer, "sub", f.svc.Email)
		}, ErrNotFound},
		"ReplaceRecoveryCodes": {func() error { return f.st.ReplaceRecoveryCodes(ctx, id, recoveryCodes(), RecoveryCodeProof{}) }, ErrNotFound},
		"RecoverOwnerPassword": {func() error { return f.st.RecoverOwnerPassword(ctx, f.svc.Email, serviceTestPassword) }, ErrValidation},
	}
	for name, tc := range rejected {
		if err := tc.call(); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
	}
	f.assertNoInteractiveState(t)
}

// The placeholder address must not work as a login or recovery handle, and must look exactly like
// an account that does not exist.
func TestServiceAccount_PlaceholderEmailIsNotAnAuthHandle(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()

	for _, password := range []string{"", serviceTestPassword, "anything"} {
		if _, err := f.st.AuthenticateUser(ctx, f.svc.Email, password); !errors.Is(err, ErrUnauthorized) {
			t.Errorf("AuthenticateUser(%q): err = %v, want ErrUnauthorized", password, err)
		}
	}
	if _, err := f.st.GetUserByEmail(ctx, f.svc.Email); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetUserByEmail: err = %v, want ErrNotFound", err)
	}
	if _, err := f.st.GetUserPasswordHash(ctx, f.svc.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetUserPasswordHash: err = %v, want ErrNotFound", err)
	}

	// No new human account can claim the domain by any creation path.
	for _, email := range []string{f.svc.Email, "attacker@service.invalid", "ATTACKER@Service.Invalid", "a@sub.service.invalid", "a@service.invalid."} {
		if _, err := f.st.CreateUser(ctx, email, serviceTestPassword, "X"); !errors.Is(err, ErrValidation) {
			t.Errorf("CreateUser(%q): err = %v, want ErrValidation", email, err)
		}
		if _, err := f.st.BootstrapUser(ctx, email, serviceTestPassword, "X"); !errors.Is(err, ErrValidation) {
			t.Errorf("BootstrapUser(%q): err = %v, want ErrValidation", email, err)
		}
		if _, err := f.st.CreateUserOIDC(ctx, serviceTestIssuer, serviceTestIssuer, "sub-"+email, email, "X"); !errors.Is(err, ErrValidation) {
			t.Errorf("CreateUserOIDC(%q): err = %v, want ErrValidation", email, err)
		}
	}
	// An IdP asserting the placeholder cannot attach itself through the explicit link either.
	err := f.st.LinkOIDCIdentityExplicit(ctx, f.human.ID, serviceTestIssuer, "sub", f.svc.Email)
	if !errors.Is(err, ErrValidation) {
		t.Errorf("linking a human using the placeholder: err = %v, want ErrValidation", err)
	}
	f.assertNoInteractiveState(t)
}

// Authentication records that exist in spite of the schema (the triggers are dropped here) must not
// make a service account interactive, and must not yield any new credential.
func TestServiceAccount_StaleCredentialRowsFailClosed(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()
	f.dropCredentialTriggers(t)
	id := f.svc.ID

	session, _, err := f.st.CreateSession(ctx, id, time.Hour)
	if err != nil {
		t.Fatalf("seed session: %v", err)
	}
	pending, _, err := f.st.CreateLogin2FAPending(ctx, id, time.Minute)
	if err != nil {
		t.Fatalf("seed pending 2fa: %v", err)
	}
	if err := f.st.IncrementLogin2FAPendingAttempt(ctx, pending); err != nil {
		t.Fatalf("seed attempt: %v", err)
	}
	secretEnc, err := f.st.EncryptTOTPSecret([]byte("JBSWY3DPEHPK3PXP"))
	if err != nil {
		t.Fatal(err)
	}
	setup, _, err := f.st.CreateTwoFactorEnrollment(ctx, id, secretEnc, time.Minute)
	if err != nil {
		t.Fatalf("seed enrollment: %v", err)
	}
	if err := f.st.AddRecoveryCodes(ctx, id, recoveryCodes()[:1]); err != nil {
		t.Fatalf("seed recovery code: %v", err)
	}
	var codeID int64
	var codeHash string
	if err := f.db.QueryRow(`SELECT id, code_hash FROM user_recovery_codes WHERE user_id = ?`, id).Scan(&codeID, &codeHash); err != nil {
		t.Fatal(err)
	}
	if err := f.st.LinkOIDCIdentity(ctx, id, serviceTestIssuer, "stale-sub", f.svc.Email); err != nil {
		t.Fatalf("seed oidc identity: %v", err)
	}
	const rawGrant = "stale-first-password-grant"
	if _, err := f.db.Exec(`INSERT INTO first_password_grants(token_hash, user_id, session_token_hash, created_at, expires_at) VALUES (?, ?, ?, 1, ?)`,
		hashToken(rawGrant), id, hashToken(session), time.Now().Add(time.Hour).UnixMilli()); err != nil {
		t.Fatalf("seed first-password grant: %v", err)
	}
	if _, err := f.st.CreateOAuthClient(ctx, "client", "Client", serviceTestRedirect); err != nil {
		t.Fatal(err)
	}
	const verifier = "stale-pkce-verifier"
	authCode, err := f.st.CreateOAuthAuthCode(ctx, "client", id, serviceTestRedirect, s256Challenge(verifier), "S256", serviceTestResource)
	if err != nil {
		t.Fatalf("seed auth code: %v", err)
	}
	pair, err := f.st.IssueOAuthTokenPair(ctx, "client", id, serviceTestResource)
	if err != nil {
		t.Fatalf("seed oauth tokens: %v", err)
	}
	if err := f.st.CreateMobileOIDCFlow(ctx, "state", s256Challenge(verifier), "/", time.Minute); err != nil {
		t.Fatal(err)
	}
	mobileCode, _, _, err := f.st.CreateMobileOIDCHandoffGrant(ctx, "state", id, time.Minute)
	if err != nil {
		t.Fatalf("seed mobile grant: %v", err)
	}
	sessionsBefore := f.count(t, `SELECT COUNT(*) FROM sessions WHERE user_id = ?`, id)
	accessBefore := f.count(t, `SELECT COUNT(*) FROM oauth_access_tokens WHERE user_id = ?`, id)

	notFound := map[string]func() error{
		"GetUserBySessionToken": func() error { _, err := f.st.GetUserBySessionToken(ctx, session); return err },
		"GetUserByLogin2FAPendingToken": func() error {
			_, _, err := f.st.GetUserByLogin2FAPendingToken(ctx, pending)
			return err
		},
		"CompleteLogin2FA": func() error {
			_, _, err := f.st.CompleteLogin2FA(ctx, pending, id, RecoveryCodeProof{}, time.Hour)
			return err
		},
		"GetTwoFactorEnrollmentByToken": func() error {
			_, _, err := f.st.GetTwoFactorEnrollmentByToken(ctx, setup)
			return err
		},
		"CompleteTwoFactorEnrollment": func() error {
			return f.st.CompleteTwoFactorEnrollment(ctx, id, setup, recoveryCodes())
		},
		"GetUserByOIDCIdentity": func() error {
			_, err := f.st.GetUserByOIDCIdentity(ctx, serviceTestIssuer, "stale-sub")
			return err
		},
		"UpdateOIDCIdentityEmailAtLogin": func() error {
			return f.st.UpdateOIDCIdentityEmailAtLogin(ctx, id, serviceTestIssuer, "stale-sub", "attacker@example.com")
		},
		"LinkOIDCIdentityExplicit": func() error {
			return f.st.LinkOIDCIdentityExplicit(ctx, id, serviceTestIssuer, "stale-sub", f.svc.Email)
		},
		"RedeemOAuthAuthCodeAndIssue": func() error {
			_, err := f.st.RedeemOAuthAuthCodeAndIssue(ctx, authCode, "client", serviceTestRedirect, serviceTestResource, verifier)
			return err
		},
		"RedeemOAuthAuthCode": func() error {
			_, err := f.st.RedeemOAuthAuthCode(ctx, authCode, "client", serviceTestRedirect, serviceTestResource, verifier)
			return err
		},
		"ConsumeOAuthRefreshTokenAndIssue": func() error {
			_, err := f.st.ConsumeOAuthRefreshTokenAndIssue(ctx, pair.RefreshToken, "client", serviceTestResource)
			return err
		},
		"ConsumeOAuthRefreshToken": func() error {
			_, err := f.st.ConsumeOAuthRefreshToken(ctx, pair.RefreshToken, "client", serviceTestResource)
			return err
		},
		"GetUserByOAuthAccessToken": func() error {
			_, err := f.st.GetUserByOAuthAccessToken(ctx, pair.AccessToken, serviceTestResource)
			return err
		},
		"ExchangeMobileOIDCHandoff": func() error {
			_, err := f.st.ExchangeMobileOIDCHandoff(ctx, mobileCode, "state", verifier, time.Hour)
			return err
		},
		"SetFirstPassword": func() error {
			return f.st.SetFirstPassword(ctx, id, rawGrant, session, serviceTestPassword)
		},
	}
	for name, call := range notFound {
		if err := call(); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: err = %v, want ErrNotFound", name, err)
		}
	}

	if valid, err := f.st.FirstPasswordGrantValid(ctx, rawGrant, session, id); err != nil || valid {
		t.Errorf("FirstPasswordGrantValid = %v, %v, want false", valid, err)
	}
	if _, _, err := f.st.CreateFirstPasswordGrant(ctx, id, session, time.Minute); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("CreateFirstPasswordGrant: err = %v, want ErrUnauthorized", err)
	}
	if matchID, _, err := f.st.MatchRecoveryCodeProof(ctx, id, recoveryCodes()[0]); err != nil || matchID != 0 {
		t.Errorf("MatchRecoveryCodeProof = %d, %v, want no match", matchID, err)
	}
	if ok, err := f.st.ConsumeRecoveryCodeProof(ctx, id, RecoveryCodeProof{ID: codeID, Hash: codeHash}); err != nil || ok {
		t.Errorf("ConsumeRecoveryCodeProof = %v, %v, want false", ok, err)
	}
	if ok, err := f.st.ConsumeRecoveryCode(ctx, id, recoveryCodes()[0]); err != nil || ok {
		t.Errorf("ConsumeRecoveryCode = %v, %v, want false", ok, err)
	}

	if got := f.count(t, `SELECT COUNT(*) FROM sessions WHERE user_id = ?`, id); got != sessionsBefore {
		t.Errorf("sessions changed from %d to %d", sessionsBefore, got)
	}
	if got := f.count(t, `SELECT COUNT(*) FROM oauth_access_tokens WHERE user_id = ?`, id); got != accessBefore {
		t.Errorf("oauth access tokens changed from %d to %d", accessBefore, got)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM users WHERE id = ? AND password_hash IS NULL AND two_factor_enabled = 0`, id); n != 1 {
		t.Error("a stale record turned the service account interactive")
	}
	if n := f.count(t, `SELECT COUNT(*) FROM oauth_refresh_tokens WHERE user_id = ? AND revoked_at IS NULL`, id); n != 1 {
		t.Error("stale refresh token was rotated")
	}
}

// Even a row that violates every invariant (a password, Owner, bootstrap, 2FA) must fail closed.
func TestServiceAccount_MalformedIdentityFailsClosed(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()
	hash, err := bcrypt.GenerateFromPassword([]byte(serviceTestPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	secretEnc, err := f.st.EncryptTOTPSecret([]byte("JBSWY3DPEHPK3PXP"))
	if err != nil {
		t.Fatal(err)
	}
	f.corrupt(t,
		fmt.Sprintf(`UPDATE users SET password_hash = '%s', system_role = 'owner', is_bootstrap = 1, two_factor_enabled = 1, two_factor_secret_enc = '%s' WHERE id = %d`, hash, secretEnc, f.svc.ID),
	)
	id := f.svc.ID

	if _, err := f.st.AuthenticateUser(ctx, f.svc.Email, serviceTestPassword); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("AuthenticateUser with the correct password: err = %v, want ErrUnauthorized", err)
	}
	if _, err := f.st.GetUserPasswordHash(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetUserPasswordHash: err = %v, want ErrNotFound", err)
	}
	if _, err := f.st.GetUserTwoFactorSecret(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetUserTwoFactorSecret: err = %v, want ErrNotFound", err)
	}
	u, err := f.st.GetUser(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !u.IsService || u.SystemRole != SystemRoleUser || u.HasLocalPassword || u.IsTwoFactorActive() {
		t.Errorf("GetUser did not fail closed: %+v", u)
	}

	// The forged Owner role grants nothing: not as requester, and not in owner counting.
	if err := f.st.requireOwner(ctx, id); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("requireOwner: err = %v", err)
	}
	if err := f.st.requireAdmin(ctx, id); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("requireAdmin: err = %v", err)
	}
	if err := f.st.UpdateUserRole(ctx, id, f.human.ID, SystemRoleAdmin); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("UpdateUserRole as forged owner: err = %v", err)
	}
	if err := f.st.DeleteUser(ctx, id, f.human.ID); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("DeleteUser as forged owner: err = %v", err)
	}
	if _, err := f.st.CreateServiceAccount(ctx, id, "Escalated"); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("CreateServiceAccount as forged owner: err = %v", err)
	}
	if n, err := f.st.countOwners(ctx); err != nil || n != 1 {
		t.Errorf("countOwners = %d, %v, want the one real owner", n, err)
	}
	posture, err := f.st.OwnerRecoveryPosture(ctx, true, true)
	if err != nil || posture.OwnerCount != 1 {
		t.Errorf("OwnerRecoveryPosture = %+v, %v, want one owner", posture, err)
	}
	// The real last owner stays protected: a forged owner does not count towards the minimum.
	if err := f.st.UpdateUserRole(ctx, f.owner.ID, f.owner.ID, SystemRoleUser); !errors.Is(err, ErrValidation) {
		t.Errorf("demoting the last real owner: err = %v, want ErrValidation", err)
	}

	// Recovery paths must not overwrite the credential or hand it to the row.
	if err := f.st.RecoverOwnerPassword(ctx, f.svc.Email, "AttackerPassword1!"); !errors.Is(err, ErrValidation) {
		t.Errorf("RecoverOwnerPassword: err = %v, want ErrValidation", err)
	}
	if err := f.st.ResetLocalPassword(ctx, id, string(hash), "AttackerPassword1!"); !errors.Is(err, ErrConflict) {
		t.Errorf("ResetLocalPassword: err = %v, want ErrConflict", err)
	}
	if err := f.st.UpdateUserPassword(ctx, id, "AttackerPassword1!"); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateUserPassword: err = %v, want ErrNotFound", err)
	}
	var stored string
	if err := f.db.QueryRow(`SELECT password_hash FROM users WHERE id = ?`, id).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != string(hash) {
		t.Error("a recovery path replaced the malformed row's password")
	}

	users, err := f.st.ListUsers(ctx, f.owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, listed := range users {
		if listed.ID == id && (!listed.IsService || listed.SystemRole != SystemRoleUser) {
			t.Errorf("ListUsers did not fail closed: %+v", listed)
		}
	}

	// A forged owner does not block its own removal as "the last owner".
	if err := f.st.DeleteUser(ctx, f.owner.ID, id); err != nil {
		t.Errorf("deleting the malformed row: %v", err)
	}
}

// Any is_service value other than exactly 0 is treated as a service account, not as a human.
func TestServiceAccount_UnexpectedFlagValuesAreNotHuman(t *testing.T) {
	for _, flag := range []string{"2", "-1", "'yes'", "'0x'", "x'00'"} {
		t.Run(flag, func(t *testing.T) {
			f := newServiceFixture(t)
			ctx := context.Background()
			hash, err := bcrypt.GenerateFromPassword([]byte(serviceTestPassword), bcrypt.MinCost)
			if err != nil {
				t.Fatal(err)
			}
			f.corrupt(t, fmt.Sprintf(`UPDATE users SET is_service = %s, password_hash = '%s', system_role = 'owner' WHERE id = %d`, flag, hash, f.human.ID))

			if _, err := f.st.AuthenticateUser(ctx, f.human.Email, serviceTestPassword); !errors.Is(err, ErrUnauthorized) {
				t.Errorf("AuthenticateUser: err = %v, want ErrUnauthorized", err)
			}
			u, err := f.st.GetUser(ctx, f.human.ID)
			if err != nil {
				t.Fatalf("GetUser: %v", err)
			}
			if !u.IsService || u.SystemRole != SystemRoleUser {
				t.Errorf("GetUser = %+v, want a plain service identity", u)
			}
			if _, _, err := f.st.CreateSession(ctx, f.human.ID, time.Hour); !errors.Is(err, ErrServiceAccountRestricted) {
				t.Errorf("CreateSession: err = %v, want ErrServiceAccountRestricted", err)
			}
			if err := f.st.requireOwner(ctx, f.human.ID); !errors.Is(err, ErrUnauthorized) {
				t.Errorf("requireOwner: err = %v, want ErrUnauthorized", err)
			}
		})
	}
}

// With no human on the instance, a service row must neither re-open anonymous bootstrap nor leave
// any first-user path handing out Owner. Failing closed here means bootstrap stays unavailable.
func TestServiceAccount_DoesNotParticipateInBootstrap(t *testing.T) {
	st, sqlDB, cleanup := newTestStoreWithSQL(t)
	defer cleanup()
	ctx := context.Background()

	const email = "svc-only@service.invalid"
	if _, err := sqlDB.Exec(`INSERT INTO users(email, name, created_at, system_role, is_service) VALUES (?, 'Bot', 1, 'user', 1)`, email); err != nil {
		t.Fatal(err)
	}
	if _, err := st.BootstrapUser(ctx, "first@example.com", serviceTestPassword, "First"); !errors.Is(err, ErrConflict) {
		t.Fatalf("BootstrapUser: err = %v, want ErrConflict", err)
	}
	if err := st.RecoverOwnerPassword(ctx, email, serviceTestPassword); !errors.Is(err, ErrValidation) {
		t.Fatalf("RecoverOwnerPassword: err = %v, want ErrValidation", err)
	}
	sso, err := st.CreateUserOIDC(ctx, serviceTestIssuer, serviceTestIssuer, "sub", "first@example.com", "First")
	if err != nil {
		t.Fatal(err)
	}
	if sso.SystemRole == SystemRoleOwner || sso.IsBootstrap {
		t.Fatalf("OIDC signup claimed first-user Owner: %+v", sso)
	}
	if _, err := sqlDB.Exec(`INSERT INTO users(email, name, created_at, system_role, is_bootstrap, is_service) VALUES ('svc-b@service.invalid', 'Bot', 1, 'user', 1, 1)`); err == nil {
		t.Fatal("a bootstrap service account must be rejected")
	}
}

// The store's own checks do not lean on the schema: with the CHECK off and the credential triggers
// gone, the Go guards still refuse.
func TestServiceAccount_GuardsHoldWithoutSchemaEnforcement(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()
	secretEnc, err := f.st.EncryptTOTPSecret([]byte("JBSWY3DPEHPK3PXP"))
	if err != nil {
		t.Fatal(err)
	}
	f.dropCredentialTriggers(t)
	f.corrupt(t, fmt.Sprintf(`UPDATE users SET two_factor_enabled = 1, two_factor_secret_enc = '%s' WHERE id = %d`, secretEnc, f.svc.ID))

	if _, err := f.db.Exec(`PRAGMA ignore_check_constraints = ON`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = f.db.Exec(`PRAGMA ignore_check_constraints = OFF`) })

	if err := f.st.UpdateUserRole(ctx, f.owner.ID, f.svc.ID, SystemRoleAdmin); !errors.Is(err, ErrServiceAccountRestricted) {
		t.Errorf("UpdateUserRole: err = %v, want ErrServiceAccountRestricted", err)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM users WHERE id = ? AND system_role = 'user'`, f.svc.ID); n != 1 {
		t.Error("the service account was promoted")
	}
	if err := f.st.ReplaceRecoveryCodes(ctx, f.svc.ID, recoveryCodes(), RecoveryCodeProof{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("ReplaceRecoveryCodes: err = %v, want ErrNotFound", err)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM user_recovery_codes WHERE user_id = ?`, f.svc.ID); n != 0 {
		t.Errorf("%d recovery codes were stored", n)
	}
}

func TestServiceAccount_NoImplicitProjectAccess(t *testing.T) {
	f := newServiceFixture(t)
	ownerCtx := WithUserID(context.Background(), f.owner.ID)
	svcCtx := WithUserID(context.Background(), f.svc.ID)

	allowed, err := f.st.CreateProject(ownerCtx, "Allowed")
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.st.CreateProject(ownerCtx, "Other")
	if err != nil {
		t.Fatal(err)
	}
	// An ownerless durable project is what sign-in hands to a user.
	if _, err := f.db.Exec(`UPDATE projects SET owner_user_id = NULL WHERE id = ?`, other.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`DELETE FROM project_members WHERE project_id = ?`, other.ID); err != nil {
		t.Fatal(err)
	}

	assertNoAccess := func(projectID int64) {
		t.Helper()
		if role, err := f.st.GetProjectRole(svcCtx, projectID, f.svc.ID); err != nil || role != "" {
			t.Errorf("project %d: role = %q, %v, want none", projectID, role, err)
		}
		if err := f.st.CheckProjectRole(svcCtx, projectID, f.svc.ID, RoleViewer); !errors.Is(err, ErrNotFound) {
			t.Errorf("project %d: CheckProjectRole err = %v, want ErrNotFound", projectID, err)
		}
	}
	assertNoAccess(allowed.ID)
	assertNoAccess(other.ID)
	listed, err := f.st.ListProjects(svcCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 0 {
		t.Fatalf("service account sees %d projects, want 0", len(listed))
	}

	if err := f.st.AssignUnownedDurableProjectsToUser(svcCtx, f.svc.ID); !errors.Is(err, ErrServiceAccountRestricted) {
		t.Fatalf("AssignUnownedDurableProjectsToUser: err = %v", err)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM projects WHERE owner_user_id = ?`, f.svc.ID); n != 0 {
		t.Fatalf("service account owns %d projects", n)
	}
	assertNoAccess(other.ID)

	// Explicit membership is the one way in, and it reaches only that project.
	if err := f.st.AddProjectMember(ownerCtx, f.owner.ID, allowed.ID, f.svc.ID, RoleContributor); err != nil {
		t.Fatalf("explicit membership: %v", err)
	}
	if err := f.st.CheckProjectRole(svcCtx, allowed.ID, f.svc.ID, RoleContributor); err != nil {
		t.Errorf("after explicit membership: %v", err)
	}
	if err := f.st.CheckProjectRole(svcCtx, allowed.ID, f.svc.ID, RoleMaintainer); !errors.Is(err, ErrNotFound) {
		t.Errorf("membership exceeded its role: %v", err)
	}
	assertNoAccess(other.ID)
}

// Machine authentication stays possible for the next slice, without becoming a privilege path.
func TestServiceAccount_APITokenLookupRemainsAvailable(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()

	const raw = APITokenPrefix + "service-account-machine-token"
	if _, err := f.db.Exec(`INSERT INTO api_tokens(user_id, token_hash, created_at) VALUES (?, ?, 1)`, f.svc.ID, hashToken(raw)); err != nil {
		t.Fatal(err)
	}
	u, err := f.st.GetUserByAPIToken(ctx, raw)
	if err != nil {
		t.Fatalf("GetUserByAPIToken: %v", err)
	}
	if u.ID != f.svc.ID || !u.IsService || u.SystemRole != SystemRoleUser {
		t.Fatalf("identity = %+v", u)
	}
	if _, err := f.db.Exec(`UPDATE api_tokens SET revoked_at = 2 WHERE token_hash = ?`, hashToken(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.GetUserByAPIToken(ctx, raw); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked token: err = %v, want ErrNotFound", err)
	}

	// A malformed row authenticating by token still gets no privilege.
	const raw2 = APITokenPrefix + "second-machine-token"
	if _, err := f.db.Exec(`INSERT INTO api_tokens(user_id, token_hash, created_at) VALUES (?, ?, 1)`, f.svc.ID, hashToken(raw2)); err != nil {
		t.Fatal(err)
	}
	f.corrupt(t, fmt.Sprintf(`UPDATE users SET system_role = 'owner' WHERE id = %d`, f.svc.ID))
	u, err = f.st.GetUserByAPIToken(ctx, raw2)
	if err != nil {
		t.Fatal(err)
	}
	if u.SystemRole != SystemRoleUser {
		t.Fatalf("forged role leaked through token lookup: %+v", u)
	}
}

// api_tokens.is_service keeps meaning "automation token owned by a human".
func TestServiceAccount_HumanServiceTokensAreUnchanged(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()

	id, plain, _, err := f.st.CreateUserAPIToken(ctx, f.human.ID, nil, true)
	if err != nil {
		t.Fatalf("human service token: %v", err)
	}
	u, err := f.st.GetUserByAPIToken(ctx, plain)
	if err != nil {
		t.Fatal(err)
	}
	if u.ID != f.human.ID || u.IsService {
		t.Fatalf("a human-owned service token must authenticate as the human: %+v", u)
	}
	metas, err := f.st.ListUserAPITokens(ctx, f.human.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 1 || metas[0].ID != id || !metas[0].IsService {
		t.Fatalf("token metadata = %+v", metas)
	}
	// A token for a user that does not exist still fails as it always did.
	if _, _, _, err := f.st.CreateUserAPIToken(ctx, 999999, nil, false); err == nil || errors.Is(err, ErrServiceAccountRestricted) {
		t.Fatalf("missing user: err = %v, want the foreign key failure", err)
	}
}

func TestServiceAccount_DeletionFollowsExistingSemantics(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()

	const raw = APITokenPrefix + "to-be-deleted"
	if _, err := f.db.Exec(`INSERT INTO api_tokens(user_id, token_hash, created_at, is_service) VALUES (?, ?, 1, 1)`, f.svc.ID, hashToken(raw)); err != nil {
		t.Fatal(err)
	}
	if err := f.st.DeleteUser(ctx, f.admin.ID, f.svc.ID); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("delete as admin: err = %v, want ErrUnauthorized", err)
	}
	if err := f.st.DeleteUser(ctx, f.owner.ID, f.svc.ID); err != nil {
		t.Fatalf("delete as owner: %v", err)
	}
	if _, err := f.st.GetUser(ctx, f.svc.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetUser after delete: err = %v", err)
	}
	if _, err := f.st.GetUserByAPIToken(ctx, raw); !errors.Is(err, ErrNotFound) {
		t.Fatalf("token after delete: err = %v, want ErrNotFound", err)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM archived_service_api_tokens WHERE origin_user_id = ?`, f.svc.ID); n != 1 {
		t.Fatalf("archived %d service tokens, want 1", n)
	}

	// The integer id may be reused, but not the public id, and not as a service identity.
	human, err := f.st.CreateUser(ctx, "successor@example.com", serviceTestPassword, "Successor")
	if err != nil {
		t.Fatal(err)
	}
	if human.ID != f.svc.ID {
		t.Fatalf("expected SQLite to reuse id %d, got %d", f.svc.ID, human.ID)
	}
	got, err := f.st.GetUser(ctx, human.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.IsService || got.PublicID == f.svc.PublicID {
		t.Fatalf("successor inherited service identity: %+v", got)
	}
}

func TestReservedServiceEmail(t *testing.T) {
	for email, want := range map[string]bool{
		"svc-x@service.invalid":     true,
		"a@service.invalid":         true,
		"a@sub.service.invalid":     true,
		"a@service.invalid.":        true,
		"a@notservice.invalid":      false,
		"a@service.invalid.example": false,
		"service.invalid@gmail.com": false,
		"a@example.com":             false,
	} {
		if got := isReservedServiceEmail(email); got != want {
			t.Errorf("isReservedServiceEmail(%q) = %v, want %v", email, got, want)
		}
	}
}

// openSecondConnection returns a separate handle on the same database file. The store's own pool has
// one connection, so this is how a transaction can overlap with one the store is running.
func openSecondConnection(t *testing.T, f *serviceFixture) *sql.Conn {
	t.Helper()
	var seq int
	var name, path string
	if err := f.db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	second, err := db.Open(path, db.Options{BusyTimeout: 5000, JournalMode: "WAL", Synchronous: "FULL"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	conn, err := second.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// A requester demoted by a transaction still in flight must not be authorized by a stale read: the
// store's call overlaps the demotion and has to be refused however the two interleave. A check made
// before the transaction would pass here, wait on the write lock, and then commit.
func TestServiceAccountMutations_RejectRequesterDemotedConcurrently(t *testing.T) {
	cases := map[string]func(ctx context.Context, f *serviceFixture, requester int64) error{
		"CreateServiceAccount": func(ctx context.Context, f *serviceFixture, requester int64) error {
			_, err := f.st.CreateServiceAccount(ctx, requester, "Raced")
			return err
		},
		"UpdateUserRole": func(ctx context.Context, f *serviceFixture, requester int64) error {
			return f.st.UpdateUserRole(ctx, requester, f.human.ID, SystemRoleAdmin)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newServiceFixture(t)
			ctx := context.Background()
			if err := f.st.UpdateUserRole(ctx, f.owner.ID, f.admin.ID, SystemRoleOwner); err != nil {
				t.Fatal(err)
			}
			serviceBefore := f.count(t, `SELECT COUNT(*) FROM users WHERE is_service = 1`)

			conn := openSecondConnection(t, f)
			if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
				t.Fatal(err)
			}
			if _, err := conn.ExecContext(ctx, `UPDATE users SET system_role = 'user' WHERE id = ?`, f.admin.ID); err != nil {
				t.Fatal(err)
			}

			done := make(chan error, 1)
			go func() { done <- mutate(ctx, f, f.admin.ID) }()
			time.Sleep(150 * time.Millisecond) // Let the call read the pre-demotion state first.
			if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
				t.Fatal(err)
			}

			err := <-done
			if err == nil {
				t.Fatal("mutation succeeded on a stale authorization")
			}
			if got := f.count(t, `SELECT COUNT(*) FROM users WHERE is_service = 1`); got != serviceBefore {
				t.Errorf("service accounts went from %d to %d", serviceBefore, got)
			}
			if n := f.count(t, `SELECT COUNT(*) FROM users WHERE id = ? AND system_role = 'user'`, f.human.ID); n != 1 {
				t.Error("target was promoted on a stale authorization")
			}
		})
	}
}

// Two owners demoting each other used to be able to pass the last-owner check together, because the
// owner count was read before the transaction.
func TestUpdateUserRole_ConcurrentDemotionsKeepAnOwner(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()
	for round := 0; round < 40; round++ {
		if _, err := f.db.Exec(`UPDATE users SET system_role = 'owner' WHERE id IN (?, ?)`, f.owner.ID, f.admin.ID); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = f.st.UpdateUserRole(ctx, f.owner.ID, f.admin.ID, SystemRoleUser)
		}()
		go func() {
			defer wg.Done()
			_ = f.st.UpdateUserRole(ctx, f.admin.ID, f.owner.ID, SystemRoleUser)
		}()
		wg.Wait()
		if n := f.count(t, `SELECT COUNT(*) FROM users WHERE system_role = 'owner'`); n != 1 {
			t.Fatalf("round %d: %d owners after mutual demotion, want exactly 1", round, n)
		}
	}
}

// A burst of every credential path, overlapping legitimate work for humans, leaves the service
// account non-interactive and leaves the human paths working.
func TestServiceAccount_ConcurrentCredentialAttempts(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()
	id := f.svc.ID
	secretEnc, err := f.st.EncryptTOTPSecret([]byte("JBSWY3DPEHPK3PXP"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.CreateOAuthClient(ctx, "client", "Client", serviceTestRedirect); err != nil {
		t.Fatal(err)
	}

	attempts := []func(i int) error{
		func(int) error { _, _, err := f.st.CreateSession(ctx, id, time.Hour); return err },
		func(int) error { _, _, err := f.st.CreateLogin2FAPending(ctx, id, time.Minute); return err },
		func(int) error {
			_, _, err := f.st.CreateTwoFactorEnrollment(ctx, id, secretEnc, time.Minute)
			return err
		},
		func(int) error { return f.st.SetUserTwoFactor(ctx, id, secretEnc) },
		func(int) error { return f.st.UpdateUserPassword(ctx, id, serviceTestPassword) },
		func(i int) error {
			return f.st.LinkOIDCIdentity(ctx, id, serviceTestIssuer, fmt.Sprintf("sub-%d", i), f.svc.Email)
		},
		func(int) error { return f.st.UpdateUserRole(ctx, f.owner.ID, id, SystemRoleOwner) },
		func(int) error {
			_, err := f.st.IssueOAuthTokenPair(ctx, "client", id, serviceTestResource)
			return err
		},
		func(int) error { return f.st.RecoverOwnerPassword(ctx, f.svc.Email, serviceTestPassword) },
	}

	const rounds = 4
	var mu sync.Mutex
	var successes []string
	var humanSessions []string
	var wg sync.WaitGroup
	for round := 0; round < rounds; round++ {
		for i, attempt := range attempts {
			wg.Add(1)
			go func(i, round int, attempt func(int) error) {
				defer wg.Done()
				if err := attempt(round); err == nil {
					mu.Lock()
					successes = append(successes, fmt.Sprintf("attempt %d", i))
					mu.Unlock()
				}
			}(i, round, attempt)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, _, err := f.st.CreateSession(ctx, f.human.ID, time.Hour)
			if err != nil {
				t.Errorf("human session: %v", err)
				return
			}
			mu.Lock()
			humanSessions = append(humanSessions, token)
			mu.Unlock()
		}()
	}
	wg.Wait()

	if len(successes) != 0 {
		t.Errorf("attempts succeeded for the service account: %v", successes)
	}
	f.assertNoInteractiveState(t)
	if len(humanSessions) != rounds {
		t.Fatalf("human got %d sessions, want %d", len(humanSessions), rounds)
	}
	for _, token := range humanSessions {
		if u, err := f.st.GetUserBySessionToken(ctx, token); err != nil || u.ID != f.human.ID {
			t.Errorf("human multi-session lookup = %+v, %v", u, err)
		}
	}
}

func TestCreateServiceAccount_ConcurrentCreationsAreDistinct(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()
	const n = 12
	results := make([]User, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			u, err := f.st.CreateServiceAccount(ctx, f.owner.ID, fmt.Sprintf("Bot %d", i))
			if err != nil {
				t.Errorf("create %d: %v", i, err)
				return
			}
			results[i] = u
		}(i)
	}
	wg.Wait()
	seen := map[string]bool{}
	for _, u := range results {
		for _, key := range []string{u.PublicID, u.Email} {
			if key == "" || seen[key] {
				t.Fatalf("missing or duplicate identifier %q", key)
			}
			seen[key] = true
		}
	}
}

// Human accounts keep every authentication path that service accounts lose.
func TestHumanAuthenticationUnaffectedByServiceAccounts(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()

	u, err := f.st.AuthenticateUser(ctx, f.human.Email, serviceTestPassword)
	if err != nil || u.IsService {
		t.Fatalf("AuthenticateUser = %+v, %v", u, err)
	}
	first, _, err := f.st.CreateSession(ctx, f.human.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := f.st.CreateSession(ctx, f.human.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{first, second} {
		if _, err := f.st.GetUserBySessionToken(ctx, token); err != nil {
			t.Fatalf("multi-device session: %v", err)
		}
	}
	if err := f.st.LinkOIDCIdentityExplicit(ctx, f.human.ID, serviceTestIssuer, "human-sub", f.human.Email); err != nil {
		t.Fatalf("link: %v", err)
	}
	if linked, err := f.st.GetUserByOIDCIdentity(ctx, serviceTestIssuer, "human-sub"); err != nil || linked.ID != f.human.ID {
		t.Fatalf("GetUserByOIDCIdentity = %+v, %v", linked, err)
	}
	if err := f.st.UpdateOIDCIdentityEmailAtLogin(ctx, f.human.ID, serviceTestIssuer, "human-sub", "new@example.com"); err != nil {
		t.Fatalf("UpdateOIDCIdentityEmailAtLogin: %v", err)
	}

	secretEnc, err := f.st.EncryptTOTPSecret([]byte("JBSWY3DPEHPK3PXP"))
	if err != nil {
		t.Fatal(err)
	}
	setup, _, err := f.st.CreateTwoFactorEnrollment(ctx, f.human.ID, secretEnc, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if enrolled, _, err := f.st.GetTwoFactorEnrollmentByToken(ctx, setup); err != nil || enrolled != f.human.ID {
		t.Fatalf("GetTwoFactorEnrollmentByToken = %d, %v", enrolled, err)
	}
	if err := f.st.CompleteTwoFactorEnrollment(ctx, f.human.ID, setup, recoveryCodes()); err != nil {
		t.Fatalf("CompleteTwoFactorEnrollment: %v", err)
	}
	if _, err := f.st.GetUserTwoFactorSecret(ctx, f.human.ID); err != nil {
		t.Fatalf("GetUserTwoFactorSecret: %v", err)
	}
	if id, _, err := f.st.MatchRecoveryCodeProof(ctx, f.human.ID, recoveryCodes()[0]); err != nil || id == 0 {
		t.Fatalf("MatchRecoveryCodeProof = %d, %v", id, err)
	}
	if ok, err := f.st.ConsumeRecoveryCode(ctx, f.human.ID, recoveryCodes()[0]); err != nil || !ok {
		t.Fatalf("ConsumeRecoveryCode = %v, %v", ok, err)
	}
	if err := f.st.ReplaceRecoveryCodes(ctx, f.human.ID, recoveryCodes(), RecoveryCodeProof{}); err != nil {
		t.Fatalf("ReplaceRecoveryCodes: %v", err)
	}

	if _, err := f.st.CreateOAuthClient(ctx, "client", "Client", serviceTestRedirect); err != nil {
		t.Fatal(err)
	}
	const verifier = "human-pkce-verifier"
	code, err := f.st.CreateOAuthAuthCode(ctx, "client", f.human.ID, serviceTestRedirect, s256Challenge(verifier), "S256", serviceTestResource)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := f.st.RedeemOAuthAuthCodeAndIssue(ctx, code, "client", serviceTestRedirect, serviceTestResource, verifier)
	if err != nil {
		t.Fatalf("RedeemOAuthAuthCodeAndIssue: %v", err)
	}
	if got, err := f.st.GetUserByOAuthAccessToken(ctx, pair.AccessToken, serviceTestResource); err != nil || got.ID != f.human.ID {
		t.Fatalf("GetUserByOAuthAccessToken = %+v, %v", got, err)
	}
	if _, err := f.st.ConsumeOAuthRefreshTokenAndIssue(ctx, pair.RefreshToken, "client", serviceTestResource); err != nil {
		t.Fatalf("ConsumeOAuthRefreshTokenAndIssue: %v", err)
	}

	if err := f.st.CreateMobileOIDCFlow(ctx, "state", s256Challenge(verifier), "/", time.Minute); err != nil {
		t.Fatal(err)
	}
	mobile, _, _, err := f.st.CreateMobileOIDCHandoffGrant(ctx, "state", f.human.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.ExchangeMobileOIDCHandoff(ctx, mobile, "state", verifier, time.Hour); err != nil {
		t.Fatalf("ExchangeMobileOIDCHandoff: %v", err)
	}

	hash, err := f.st.GetUserPasswordHash(ctx, f.human.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.ResetLocalPassword(ctx, f.human.ID, hash, "BrandNewPassword1!"); err != nil {
		t.Fatalf("ResetLocalPassword: %v", err)
	}
	if err := f.st.UpdateUserPassword(ctx, f.human.ID, serviceTestPassword); err != nil {
		t.Fatalf("UpdateUserPassword: %v", err)
	}
	if err := f.st.RecoverOwnerPassword(ctx, f.owner.Email, "RecoveredPassword1!"); err != nil {
		t.Fatalf("RecoverOwnerPassword: %v", err)
	}
	if strings.HasSuffix(f.owner.Email, serviceAccountEmailDomain) {
		t.Fatal("fixture email must not be on the reserved domain")
	}
}
