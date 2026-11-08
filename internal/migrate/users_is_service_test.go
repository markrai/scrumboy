package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"scrumboy/internal/store"
)

const usersIsServiceMigration = "080_add_users_is_service.sql"

// interactiveCredentialTables lists every table holding a credential or proof usable for
// interactive authentication. api_tokens is deliberately absent: it is the machine channel.
var interactiveCredentialTables = []string{
	"sessions",
	"user_oidc_identities",
	"login_2fa_pending",
	"two_factor_enrollments",
	"user_recovery_codes",
	"first_password_grants",
	"oauth_auth_codes",
	"oauth_access_tokens",
	"oauth_refresh_tokens",
	"mobile_oidc_handoff_grants",
}

const validServiceEmail = "svc-0f8e2c4a-1b3d-4e5f-8a9b-0c1d2e3f4a5b@service.invalid"

func insertServiceRow(t *testing.T, sqlDB *sql.DB, id int64, email string) {
	t.Helper()
	_, err := sqlDB.Exec(`INSERT INTO users(id, email, name, created_at, system_role, is_service) VALUES (?, ?, 'svc', 1, 'user', 1)`, id, email)
	if err != nil {
		t.Fatalf("insert service row: %v", err)
	}
}

func TestMigration080ExistingUsersRemainHuman(t *testing.T) {
	ctx := context.Background()
	sqlDB := openRawTestDB(t)
	applyMigrationsBefore(t, sqlDB, usersIsServiceMigration)

	if columnExists(t, sqlDB, "users", "is_service") {
		t.Fatal("is_service exists before migration 080")
	}
	now := time.Now().UTC().UnixMilli()
	// Every kind of existing account, including one already on the now-reserved domain: the
	// migration must not fail on it, and it must keep working as an ordinary human account.
	if _, err := sqlDB.ExecContext(ctx, `
INSERT INTO users(id, email, name, password_hash, is_bootstrap, system_role, two_factor_enabled, two_factor_secret_enc, created_at) VALUES
  (1, 'owner@example.com',        'Owner',  'hash', 1, 'owner', 1, 'secret', ?),
  (2, 'admin@example.com',        'Admin',  'hash', 0, 'admin', 0, NULL,     ?),
  (3, 'sso@example.com',          'SSO',    NULL,   0, 'user',  0, NULL,     ?),
  (4, 'legacy@service.invalid',   'Legacy', 'hash', 0, 'user',  0, NULL,     ?)
`, now, now, now, now); err != nil {
		t.Fatalf("insert pre-080 users: %v", err)
	}
	publicIDs := map[int64]string{}
	rows, err := sqlDB.QueryContext(ctx, `SELECT id, public_id FROM users`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id int64
		var pid string
		if err := rows.Scan(&id, &pid); err != nil {
			t.Fatal(err)
		}
		publicIDs[id] = pid
	}
	_ = rows.Close()

	if err := applyOne(ctx, sqlDB, usersIsServiceMigration); err != nil {
		t.Fatalf("apply 080: %v", err)
	}

	rows, err = sqlDB.QueryContext(ctx, `SELECT id, is_service, typeof(is_service), public_id, system_role FROM users ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var id, isService int64
		var kind, pid, role string
		if err := rows.Scan(&id, &isService, &kind, &pid, &role); err != nil {
			t.Fatal(err)
		}
		if isService != 0 || kind != "integer" {
			t.Fatalf("user %d: is_service=%d (%s), want integer 0", id, isService, kind)
		}
		if pid != publicIDs[id] {
			t.Fatalf("user %d: public_id changed from %q to %q", id, publicIDs[id], pid)
		}
		n++
	}
	if n != 4 {
		t.Fatalf("got %d users after migration, want 4", n)
	}
	if _, err := sqlDB.ExecContext(ctx, `UPDATE users SET name = 'Renamed', system_role = 'admin' WHERE id = 4`); err != nil {
		t.Fatalf("legacy human on reserved domain must stay writable: %v", err)
	}

	var notNull int
	var dflt sql.NullString
	if err := sqlDB.QueryRowContext(ctx, `SELECT "notnull", dflt_value FROM pragma_table_info('users') WHERE name = 'is_service'`).Scan(&notNull, &dflt); err != nil {
		t.Fatal(err)
	}
	if notNull != 1 || !strings.EqualFold(dflt.String, "FALSE") {
		t.Fatalf("is_service notnull=%d default=%q, want NOT NULL DEFAULT FALSE", notNull, dflt.String)
	}
	// Existing insert sites name no is_service column and must keep creating humans.
	if _, err := sqlDB.ExecContext(ctx, `INSERT INTO users(id, email, created_at, name, system_role) VALUES (9, 'new@example.com', ?, 'n', 'user')`, now); err != nil {
		t.Fatal(err)
	}
	var isService int
	if err := sqlDB.QueryRowContext(ctx, `SELECT is_service FROM users WHERE id = 9`).Scan(&isService); err != nil || isService != 0 {
		t.Fatalf("new row is_service=%d err=%v, want 0", isService, err)
	}
}

// api_tokens.is_service keeps its meaning (a human-owned automation token) across the migration.
func TestMigration080LeavesAPITokenServiceFlagAlone(t *testing.T) {
	ctx := context.Background()
	sqlDB := openRawTestDB(t)
	applyMigrationsBefore(t, sqlDB, usersIsServiceMigration)
	if _, err := sqlDB.ExecContext(ctx, `INSERT INTO users(id, email, created_at, name, system_role) VALUES (1, 'a@example.com', 1, 'a', 'user')`); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.ExecContext(ctx, `INSERT INTO api_tokens(id, user_id, token_hash, created_at, is_service) VALUES (1, 1, 'h', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if err := applyOne(ctx, sqlDB, usersIsServiceMigration); err != nil {
		t.Fatal(err)
	}
	var tokenFlag, userFlag int
	if err := sqlDB.QueryRowContext(ctx, `SELECT t.is_service, u.is_service FROM api_tokens t JOIN users u ON u.id = t.user_id`).Scan(&tokenFlag, &userFlag); err != nil {
		t.Fatal(err)
	}
	if tokenFlag != 1 || userFlag != 0 {
		t.Fatalf("token flag=%d user flag=%d, want a human owning a service token", tokenFlag, userFlag)
	}
}

func TestMigration080CheckRejectsInvalidServiceIdentities(t *testing.T) {
	sqlDB := openMigratedDB(t)
	insertServiceRow(t, sqlDB, 1, validServiceEmail)

	invalidInserts := map[string]string{
		"owner role":      `INSERT INTO users(email, name, created_at, system_role, is_service) VALUES ('svc-a@service.invalid', 's', 1, 'owner', 1)`,
		"admin role":      `INSERT INTO users(email, name, created_at, system_role, is_service) VALUES ('svc-a@service.invalid', 's', 1, 'admin', 1)`,
		"bootstrap":       `INSERT INTO users(email, name, created_at, system_role, is_bootstrap, is_service) VALUES ('svc-a@service.invalid', 's', 1, 'user', 1, 1)`,
		"password":        `INSERT INTO users(email, name, created_at, system_role, password_hash, is_service) VALUES ('svc-a@service.invalid', 's', 1, 'user', 'x', 1)`,
		"empty password":  `INSERT INTO users(email, name, created_at, system_role, password_hash, is_service) VALUES ('svc-a@service.invalid', 's', 1, 'user', '', 1)`,
		"2fa enabled":     `INSERT INTO users(email, name, created_at, system_role, two_factor_enabled, is_service) VALUES ('svc-a@service.invalid', 's', 1, 'user', 1, 1)`,
		"2fa secret":      `INSERT INTO users(email, name, created_at, system_role, two_factor_secret_enc, is_service) VALUES ('svc-a@service.invalid', 's', 1, 'user', 'x', 1)`,
		"real email":      `INSERT INTO users(email, name, created_at, system_role, is_service) VALUES ('real@example.com', 's', 1, 'user', 1)`,
		"no svc prefix":   `INSERT INTO users(email, name, created_at, system_role, is_service) VALUES ('bot@service.invalid', 's', 1, 'user', 1)`,
		"uppercase email": `INSERT INTO users(email, name, created_at, system_role, is_service) VALUES ('SVC-A@SERVICE.INVALID', 's', 1, 'user', 1)`,
		"is_service 2":    `INSERT INTO users(email, name, created_at, system_role, is_service) VALUES ('svc-a@service.invalid', 's', 1, 'user', 2)`,
		"is_service text": `INSERT INTO users(email, name, created_at, system_role, is_service) VALUES ('svc-a@service.invalid', 's', 1, 'user', 'yes')`,
	}
	for name, stmt := range invalidInserts {
		if _, err := sqlDB.Exec(stmt); err == nil || !strings.Contains(err.Error(), "users_service_identity") {
			t.Errorf("%s: err = %v, want users_service_identity CHECK failure", name, err)
		}
	}
	if _, err := sqlDB.Exec(`INSERT INTO users(email, name, created_at, system_role, is_service) VALUES ('svc-a@service.invalid', 's', 1, 'user', NULL)`); err == nil {
		t.Error("NULL is_service must be rejected")
	}

	invalidUpdates := map[string]string{
		"promote to owner": `UPDATE users SET system_role = 'owner' WHERE id = 1`,
		"promote to admin": `UPDATE users SET system_role = 'admin' WHERE id = 1`,
		"set bootstrap":    `UPDATE users SET is_bootstrap = 1 WHERE id = 1`,
		"set password":     `UPDATE users SET password_hash = '$2a$10$abcdefghijklmnopqrstuv' WHERE id = 1`,
		"enable 2fa":       `UPDATE users SET two_factor_enabled = 1 WHERE id = 1`,
		"set 2fa secret":   `UPDATE users SET two_factor_secret_enc = 'x' WHERE id = 1`,
		"real email":       `UPDATE users SET email = 'real@example.com' WHERE id = 1`,
	}
	for name, stmt := range invalidUpdates {
		if _, err := sqlDB.Exec(stmt); err == nil || !strings.Contains(err.Error(), "users_service_identity") {
			t.Errorf("%s: err = %v, want users_service_identity CHECK failure", name, err)
		}
	}

	// Unrelated profile writes, and the public_id insert trigger, must still work.
	if _, err := sqlDB.Exec(`UPDATE users SET name = 'renamed' WHERE id = 1`); err != nil {
		t.Fatalf("renaming a service account: %v", err)
	}
	var pid string
	if err := sqlDB.QueryRow(`SELECT public_id FROM users WHERE id = 1`).Scan(&pid); err != nil || !uuidV4.MatchString(pid) {
		t.Fatalf("service account public_id = %q (%v), want a UUIDv4", pid, err)
	}
	if _, err := sqlDB.Exec(`UPDATE users SET public_id = '0f8e2c4a-1b3d-4e5f-8a9b-0c1d2e3f4a5b' WHERE id = 1`); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("changing a service account's public_id: err = %v, want immutability error", err)
	}
}

func TestMigration080AccountTypeIsImmutable(t *testing.T) {
	sqlDB := openMigratedDB(t)
	insertServiceRow(t, sqlDB, 1, validServiceEmail)
	if _, err := sqlDB.Exec(`INSERT INTO users(id, email, name, created_at, system_role) VALUES (2, 'human@example.com', 'h', 1, 'user')`); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`UPDATE users SET is_service = 0 WHERE id = 1`,
		`UPDATE users SET is_service = 1 WHERE id = 2`,
		// A human already shaped exactly like a service account still cannot convert.
		`UPDATE users SET is_service = 1, email = 'svc-b@service.invalid', password_hash = NULL WHERE id = 2`,
	} {
		if _, err := sqlDB.Exec(stmt); err == nil || !strings.Contains(err.Error(), "immutable") {
			t.Errorf("%s: err = %v, want immutability error", stmt, err)
		}
	}
	if _, err := sqlDB.Exec(`UPDATE users SET is_service = 1 WHERE id = 1`); err != nil {
		t.Errorf("a no-op is_service write must be allowed: %v", err)
	}
}

// credentialInsert returns the INSERT for one credential row of each table. first_password_grants
// needs a parent session, and the OAuth and mobile tables need their parent client or flow.
func credentialInsert(table string, userID int64) (string, []any) {
	const hash = "hash-for-trigger-test"
	switch table {
	case "sessions":
		return `INSERT INTO sessions(user_id, token_hash, created_at, expires_at, last_seen_at) VALUES (?, ?, 1, 2, 1)`, []any{userID, hash}
	case "user_oidc_identities":
		return `INSERT INTO user_oidc_identities(user_id, issuer, subject, email_at_login, created_at) VALUES (?, 'iss', 'sub', 'e', 1)`, []any{userID}
	case "login_2fa_pending":
		return `INSERT INTO login_2fa_pending(user_id, token_hash, created_at, expires_at, attempt_count) VALUES (?, ?, 1, 2, 0)`, []any{userID, hash}
	case "two_factor_enrollments":
		return `INSERT INTO two_factor_enrollments(user_id, token_hash, secret_enc, created_at, expires_at, attempt_count) VALUES (?, ?, 's', 1, 2, 0)`, []any{userID, hash}
	case "user_recovery_codes":
		return `INSERT INTO user_recovery_codes(user_id, code_hash, created_at) VALUES (?, ?, 1)`, []any{userID, hash}
	case "first_password_grants":
		return `INSERT INTO first_password_grants(token_hash, user_id, session_token_hash, created_at, expires_at) VALUES (?, ?, 'parent-session', 1, 2)`, []any{hash, userID}
	case "oauth_auth_codes":
		return `INSERT INTO oauth_auth_codes(code_hash, client_id, user_id, redirect_uri, code_challenge, code_challenge_method, resource, created_at, expires_at) VALUES (?, 'client', ?, 'r', 'c', 'S256', 'res', 1, 2)`, []any{hash, userID}
	case "oauth_access_tokens":
		return `INSERT INTO oauth_access_tokens(token_hash, client_id, user_id, resource, created_at, expires_at) VALUES (?, 'client', ?, 'res', 1, 2)`, []any{hash, userID}
	case "oauth_refresh_tokens":
		return `INSERT INTO oauth_refresh_tokens(token_hash, client_id, user_id, resource, created_at, expires_at) VALUES (?, 'client', ?, 'res', 1, 2)`, []any{hash, userID}
	case "mobile_oidc_handoff_grants":
		return `INSERT INTO mobile_oidc_handoff_grants(code_hash, user_id, flow_state_hash, handoff_challenge, return_to, created_at, expires_at) VALUES (?, ?, 'flow', 'c', 'r', 1, 2)`, []any{hash, userID}
	}
	panic("unknown credential table " + table)
}

func TestMigration080CredentialTablesRejectServiceAccounts(t *testing.T) {
	sqlDB := openMigratedDB(t)
	insertServiceRow(t, sqlDB, 1, validServiceEmail)
	if _, err := sqlDB.Exec(`INSERT INTO users(id, email, name, created_at, system_role) VALUES (2, 'human@example.com', 'h', 1, 'user')`); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`INSERT INTO sessions(user_id, token_hash, created_at, expires_at, last_seen_at) VALUES (2, 'parent-session', 1, 2, 1)`,
		`INSERT INTO oauth_clients(id, client_name, redirect_uri, created_at) VALUES ('client', 'c', 'r', 1)`,
		`INSERT INTO mobile_oidc_flows(state_hash, handoff_challenge, challenge_method, return_to, created_at, expires_at) VALUES ('flow', 'c', 'S256', 'r', 1, 2)`,
	} {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}

	for _, table := range interactiveCredentialTables {
		t.Run(table, func(t *testing.T) {
			stmt, args := credentialInsert(table, 1)
			if _, err := sqlDB.Exec(stmt, args...); err == nil || !strings.Contains(err.Error(), "service identity:") {
				t.Fatalf("insert for service account: err = %v, want trigger rejection", err)
			}
			// The same row is accepted for a human.
			stmt, args = credentialInsert(table, 2)
			if _, err := sqlDB.Exec(stmt, args...); err != nil {
				t.Fatalf("insert for human must work: %v", err)
			}
			var n int
			if err := sqlDB.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE user_id = 1`, table)).Scan(&n); err != nil || n != 0 {
				t.Fatalf("%d %s rows reference the service account (err=%v)", n, table, err)
			}
		})
	}

	// api_tokens is the machine channel and must stay open at the schema level.
	if _, err := sqlDB.Exec(`INSERT INTO api_tokens(user_id, token_hash, created_at) VALUES (1, 'machine', 1)`); err != nil {
		t.Fatalf("api token for a service account must be representable: %v", err)
	}
}

// recover-owner does not migrate, and CheckRecoverySchema accepts anything from migration 049 on, so
// host-side recovery has to keep working on a database that has not reached migration 080.
func TestMigration080RecoverOwnerStillWorksOnOlderSchema(t *testing.T) {
	ctx := context.Background()
	sqlDB := openRawTestDB(t)
	applyMigrationsBefore(t, sqlDB, usersIsServiceMigration)
	if _, err := sqlDB.ExecContext(ctx, `INSERT INTO users(id, email, name, password_hash, is_bootstrap, system_role, created_at) VALUES (1, 'owner@example.com', 'O', 'old', 1, 'owner', 1), (2, 'user@example.com', 'U', 'old', 0, 'user', 1)`); err != nil {
		t.Fatal(err)
	}
	if err := CheckRecoverySchema(ctx, sqlDB); err != nil {
		t.Fatalf("CheckRecoverySchema: %v", err)
	}

	st := store.New(sqlDB, nil)
	if err := st.RecoverOwnerPassword(ctx, "owner@example.com", "NewPassword123!"); err != nil {
		t.Fatalf("RecoverOwnerPassword on pre-080 schema: %v", err)
	}
	var hash string
	if err := sqlDB.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id = 1`).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("NewPassword123!")) != nil {
		t.Fatal("recovered password does not verify")
	}
	if err := st.RecoverOwnerPassword(ctx, "user@example.com", "NewPassword123!"); err == nil {
		t.Fatal("recovering a non-owner must still fail")
	}
	if columnExists(t, sqlDB, "users", "is_service") {
		t.Fatal("recovery must not migrate the schema")
	}
}
