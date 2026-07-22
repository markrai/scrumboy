package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scrumboy/internal/config"
	"scrumboy/internal/db"
	"scrumboy/internal/migrate"
	"scrumboy/internal/store"
)

func recoveryTestConfig(path string) config.Config {
	return config.Config{DBPath: path, SQLiteBusyTimeout: 1000, SQLiteJournalMode: "WAL", SQLiteSynchronous: "FULL"}
}

func prepareRecoveryDB(t *testing.T, path string, owner bool) (string, int64) {
	t.Helper()
	database, err := db.Open(path, db.Options{BusyTimeout: 1000, JournalMode: "WAL", Synchronous: "FULL"})
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate.Apply(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	st := store.New(database, nil)
	u, err := st.BootstrapUser(context.Background(), "owner@example.com", "OldPassword123!", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	if !owner {
		if _, err := database.Exec(`UPDATE users SET system_role='user' WHERE id=?`, u.ID); err != nil {
			t.Fatal(err)
		}
	}
	token, _, err := st.CreateSession(context.Background(), u.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`DROP TABLE first_password_grants`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`DELETE FROM schema_migrations WHERE version='056_add_first_password_grants.sql'`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return token, u.ID
}

func TestRecoverOwnerCommandWithoutMigration056(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	oldSession, ownerID := prepareRecoveryDB(t, path, true)
	cfg := recoveryTestConfig(path)
	cfg.OIDCIssuerCanonical = "https://idp.example"
	cfg.OIDCClientID = "client"
	cfg.OIDCClientSecret = "secret"
	cfg.OIDCRedirectURL = "https://scrumboy.example/api/auth/oidc/callback"
	cfg.OIDCLocalAuthDisabled = true
	var output bytes.Buffer
	if err := runRecoverOwner(cfg, []string{"--email", "owner@example.com", "--password-stdin"}, strings.NewReader("RecoveredPassword123!\n"), &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "sessions") || !strings.Contains(output.String(), "local authentication is disabled") {
		t.Fatalf("missing audit-safe recovery output: %s", output.String())
	}
	database, err := db.Open(path, db.Options{BusyTimeout: 1000, JournalMode: "WAL", Synchronous: "FULL"})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	st := store.New(database, nil)
	if _, err := st.AuthenticateUser(context.Background(), "owner@example.com", "RecoveredPassword123!"); err != nil {
		t.Fatalf("recovered local login failed: %v", err)
	}
	if _, err := st.GetUserBySessionToken(context.Background(), oldSession); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("old owner session survived recovery: %v", err)
	}
	var pending int
	if err := database.QueryRow(`SELECT COUNT(*) FROM login_2fa_pending WHERE user_id=?`, ownerID).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("pending challenges not revoked: count=%d err=%v", pending, err)
	}
}

func TestRecoverOwnerCommandEstablishesPasswordForOIDCOnlyOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	prepareRecoveryDB(t, path, true)
	database, err := db.Open(path, db.Options{BusyTimeout: 1000, JournalMode: "WAL", Synchronous: "FULL"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE users SET password_hash=NULL WHERE email='owner@example.com'`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO user_oidc_identities(user_id,issuer,subject,email_at_login,created_at) SELECT id,'https://idp.example','owner-sub',email,0 FROM users WHERE email='owner@example.com'`); err != nil {
		t.Fatal(err)
	}
	_ = database.Close()
	if err := runRecoverOwner(recoveryTestConfig(path), []string{"--email", "owner@example.com", "--password-stdin"}, strings.NewReader("RecoveredPassword123!\n"), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	database, err = db.Open(path, db.Options{BusyTimeout: 1000, JournalMode: "WAL", Synchronous: "FULL"})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	st := store.New(database, &store.StoreOptions{ConfiguredOIDCIssuer: "https://idp.example"})
	u, err := st.AuthenticateUser(context.Background(), "owner@example.com", "RecoveredPassword123!")
	if err != nil {
		t.Fatal(err)
	}
	if !u.HasLocalPassword || !u.OIDCLinked {
		t.Fatalf("recovery did not produce dual-auth owner: %+v", u)
	}
}

func TestRecoverOwnerCommandRejectsUnsafeTargetsAndArgvPassword(t *testing.T) {
	if err := runRecoverOwner(recoveryTestConfig("unused"), []string{"--email", "owner@example.com", "--password", "secret"}, strings.NewReader(""), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "command-line") {
		t.Fatalf("argv password was not rejected safely: %v", err)
	}
	path := filepath.Join(t.TempDir(), "app.db")
	prepareRecoveryDB(t, path, false)
	err := runRecoverOwner(recoveryTestConfig(path), []string{"--email", "owner@example.com", "--password-stdin"}, strings.NewReader("RecoveredPassword123!\n"), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "not an owner") {
		t.Fatalf("non-owner recovery was not rejected: %v", err)
	}
	err = runRecoverOwner(recoveryTestConfig(path), []string{"--email", "missing@example.com", "--password-stdin"}, strings.NewReader("RecoveredPassword123!\n"), &bytes.Buffer{})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown target error=%v", err)
	}
}

func TestRecoverOwnerCommandRejectsIncompatibleSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	prepareRecoveryDB(t, path, true)
	database, err := db.Open(path, db.Options{BusyTimeout: 1000, JournalMode: "WAL", Synchronous: "FULL"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES ('999_future.sql', 0)`); err != nil {
		t.Fatal(err)
	}
	_ = database.Close()
	err = runRecoverOwner(recoveryTestConfig(path), []string{"--email", "owner@example.com", "--password-stdin"}, strings.NewReader("RecoveredPassword123!\n"), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "unknown to this binary") {
		t.Fatalf("newer schema was not rejected: %v", err)
	}
}

func TestRecoverOwnerCommandRejectsTooOldAndMissingAuthSchema(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate string
		want   string
	}{
		{name: "too old", mutate: `DELETE FROM schema_migrations WHERE version='049_add_oidc_identities.sql'`, want: "too old"},
		{name: "missing table", mutate: `DROP TABLE login_2fa_pending`, want: "login_2fa_pending"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "app.db")
			prepareRecoveryDB(t, path, true)
			database, err := db.Open(path, db.Options{BusyTimeout: 1000, JournalMode: "WAL", Synchronous: "FULL"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(tc.mutate); err != nil {
				t.Fatal(err)
			}
			_ = database.Close()
			err = runRecoverOwner(recoveryTestConfig(path), []string{"--email", "owner@example.com", "--password-stdin"}, strings.NewReader("RecoveredPassword123!\n"), &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("schema error=%v want %q", err, tc.want)
			}
		})
	}
}

func TestRecoverOwnerCommandRejectsWeakPasswordAndImplicitPipe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	prepareRecoveryDB(t, path, true)
	err := runRecoverOwner(recoveryTestConfig(path), []string{"--email", "owner@example.com", "--password-stdin"}, strings.NewReader("weak\n"), &bytes.Buffer{})
	if err == nil {
		t.Fatal("weak recovery password was accepted")
	}
	err = runRecoverOwner(recoveryTestConfig(path), []string{"--email", "owner@example.com"}, strings.NewReader("RecoveredPassword123!\n"), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "--password-stdin") {
		t.Fatalf("implicit non-terminal stdin was accepted: %v", err)
	}
}

func TestRecoverOwnerCommandFailsClearlyWhenDatabaseIsLocked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	prepareRecoveryDB(t, path, true)
	locker, err := db.Open(path, db.Options{BusyTimeout: 1000, JournalMode: "WAL", Synchronous: "FULL"})
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Close()
	conn, err := locker.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(context.Background(), `ROLLBACK`)

	cfg := recoveryTestConfig(path)
	cfg.SQLiteBusyTimeout = 50
	err = runRecoverOwner(cfg, []string{"--email", "owner@example.com", "--password-stdin"}, strings.NewReader("RecoveredPassword123!\n"), &bytes.Buffer{})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "locked") || !strings.Contains(err.Error(), "stop the active Scrumboy service") {
		t.Fatalf("locked database did not produce actionable failure: %v", err)
	}
}
