package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"scrumboy/internal/crypto"
	"scrumboy/internal/db"
	"scrumboy/internal/migrate"
)

const startupEncryptionValidKey = "YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXoxMjM0NTY="
const startupEncryptionInvalidKey = "REPLACE_WITH_BASE64_32_BYTE_KEY"

func newStartupEncryptionTestDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	dir := t.TempDir()
	sqlDB, err := db.Open(filepath.Join(dir, "app.db"), db.Options{
		BusyTimeout: 5000,
		JournalMode: "WAL",
		Synchronous: "FULL",
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	if err := migrate.Apply(context.Background(), sqlDB); err != nil {
		_ = sqlDB.Close()
		t.Fatalf("migrate: %v", err)
	}

	return sqlDB, func() { _ = sqlDB.Close() }
}

func bootstrapStartupEncryptionUser(t *testing.T, sqlDB *sql.DB) User {
	t.Helper()

	u, err := New(sqlDB, nil).BootstrapUser(context.Background(), "startup-enc@example.com", "password123", "Startup Enc")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	return u
}

func TestResolveStartupEncryptionKey_InvalidNoUsersAllowed(t *testing.T) {
	sqlDB, cleanup := newStartupEncryptionTestDB(t)
	defer cleanup()

	res, err := ResolveStartupEncryptionKey(context.Background(), sqlDB, startupEncryptionInvalidKey)
	if err != nil {
		t.Fatalf("ResolveStartupEncryptionKey: %v", err)
	}
	if len(res.Key) != 0 {
		t.Fatalf("expected nil key, got %d bytes", len(res.Key))
	}
	if !res.InvalidIgnored {
		t.Fatal("expected InvalidIgnored=true")
	}
	if res.EncryptedAuthSecurityData {
		t.Fatal("expected no encrypted auth/security data")
	}
}

func TestResolveStartupEncryptionKey_InvalidNormalUserAllowed(t *testing.T) {
	sqlDB, cleanup := newStartupEncryptionTestDB(t)
	defer cleanup()
	bootstrapStartupEncryptionUser(t, sqlDB)

	res, err := ResolveStartupEncryptionKey(context.Background(), sqlDB, startupEncryptionInvalidKey)
	if err != nil {
		t.Fatalf("ResolveStartupEncryptionKey: %v", err)
	}
	if len(res.Key) != 0 {
		t.Fatalf("expected nil key, got %d bytes", len(res.Key))
	}
	if !res.InvalidIgnored {
		t.Fatal("expected InvalidIgnored=true")
	}
}

func TestResolveStartupEncryptionKey_InvalidWithTwoFactorEnabledFails(t *testing.T) {
	sqlDB, cleanup := newStartupEncryptionTestDB(t)
	defer cleanup()
	u := bootstrapStartupEncryptionUser(t, sqlDB)

	if _, err := sqlDB.Exec(`UPDATE users SET two_factor_enabled = 1, two_factor_secret_enc = '' WHERE id = ?`, u.ID); err != nil {
		t.Fatalf("seed 2fa flag: %v", err)
	}

	_, err := ResolveStartupEncryptionKey(context.Background(), sqlDB, startupEncryptionInvalidKey)
	if !errors.Is(err, ErrStartupEncryptionKeyInvalid) {
		t.Fatalf("expected ErrStartupEncryptionKeyInvalid, got %v", err)
	}
}

func TestResolveStartupEncryptionKey_InvalidWithUserSecretFails(t *testing.T) {
	sqlDB, cleanup := newStartupEncryptionTestDB(t)
	defer cleanup()
	u := bootstrapStartupEncryptionUser(t, sqlDB)

	if _, err := sqlDB.Exec(`UPDATE users SET two_factor_enabled = 0, two_factor_secret_enc = 'v1:ciphertext' WHERE id = ?`, u.ID); err != nil {
		t.Fatalf("seed 2fa secret: %v", err)
	}

	_, err := ResolveStartupEncryptionKey(context.Background(), sqlDB, startupEncryptionInvalidKey)
	if !errors.Is(err, ErrStartupEncryptionKeyInvalid) {
		t.Fatalf("expected ErrStartupEncryptionKeyInvalid, got %v", err)
	}
}

func TestResolveStartupEncryptionKey_InvalidWithEnrollmentSecretFails(t *testing.T) {
	sqlDB, cleanup := newStartupEncryptionTestDB(t)
	defer cleanup()
	u := bootstrapStartupEncryptionUser(t, sqlDB)

	if _, err := sqlDB.Exec(`
INSERT INTO two_factor_enrollments(user_id, token_hash, secret_enc, created_at, expires_at, attempt_count)
VALUES (?, 'token-hash', 'v1:ciphertext', 1, 2, 0)`, u.ID); err != nil {
		t.Fatalf("seed enrollment: %v", err)
	}

	_, err := ResolveStartupEncryptionKey(context.Background(), sqlDB, startupEncryptionInvalidKey)
	if !errors.Is(err, ErrStartupEncryptionKeyInvalid) {
		t.Fatalf("expected ErrStartupEncryptionKeyInvalid, got %v", err)
	}
}

func TestResolveStartupEncryptionKey_EmptyWithEncryptedStateFails(t *testing.T) {
	sqlDB, cleanup := newStartupEncryptionTestDB(t)
	defer cleanup()
	u := bootstrapStartupEncryptionUser(t, sqlDB)

	if _, err := sqlDB.Exec(`UPDATE users SET two_factor_secret_enc = 'v1:ciphertext' WHERE id = ?`, u.ID); err != nil {
		t.Fatalf("seed 2fa secret: %v", err)
	}

	_, err := ResolveStartupEncryptionKey(context.Background(), sqlDB, "")
	if !errors.Is(err, ErrStartupEncryptionKeyRequired) {
		t.Fatalf("expected ErrStartupEncryptionKeyRequired, got %v", err)
	}
}

func TestResolveStartupEncryptionKey_ValidNoEncryptedStateReturned(t *testing.T) {
	sqlDB, cleanup := newStartupEncryptionTestDB(t)
	defer cleanup()

	want, err := crypto.DecodeKey(startupEncryptionValidKey)
	if err != nil {
		t.Fatalf("decode valid key: %v", err)
	}
	res, err := ResolveStartupEncryptionKey(context.Background(), sqlDB, startupEncryptionValidKey)
	if err != nil {
		t.Fatalf("ResolveStartupEncryptionKey: %v", err)
	}
	if !bytes.Equal(res.Key, want) {
		t.Fatalf("decoded key mismatch")
	}
	if res.InvalidIgnored {
		t.Fatal("expected InvalidIgnored=false")
	}
}

func TestResolveStartupEncryptionKey_ValidWithEncryptedStateReturned(t *testing.T) {
	sqlDB, cleanup := newStartupEncryptionTestDB(t)
	defer cleanup()
	u := bootstrapStartupEncryptionUser(t, sqlDB)

	if _, err := sqlDB.Exec(`UPDATE users SET two_factor_secret_enc = 'v1:ciphertext' WHERE id = ?`, u.ID); err != nil {
		t.Fatalf("seed 2fa secret: %v", err)
	}

	want, err := crypto.DecodeKey(startupEncryptionValidKey)
	if err != nil {
		t.Fatalf("decode valid key: %v", err)
	}
	res, err := ResolveStartupEncryptionKey(context.Background(), sqlDB, startupEncryptionValidKey)
	if err != nil {
		t.Fatalf("ResolveStartupEncryptionKey: %v", err)
	}
	if !bytes.Equal(res.Key, want) {
		t.Fatalf("decoded key mismatch")
	}
	if !res.EncryptedAuthSecurityData {
		t.Fatal("expected encrypted auth/security data")
	}
}

func TestResolveStartupEncryptionKey_EmptyWithCalendarCiphertextFails(t *testing.T) {
	sqlDB, cleanup := newStartupEncryptionTestDB(t)
	defer cleanup()
	u := bootstrapStartupEncryptionUser(t, sqlDB)
	ownerCtx := WithUserID(context.Background(), u.ID)
	st := New(sqlDB, nil)
	project, err := st.CreateProject(ownerCtx, "Calendar Ciphertext")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := sqlDB.Exec(`
INSERT INTO calendar_sources(project_id, type, name, enabled, secret_enc, url_hash, created_at, updated_at)
VALUES (?, 'ics_feed', 'Family', 1, 'v1:ciphertext', 'hash-family', 1, 1)`, project.ID); err != nil {
		t.Fatalf("seed calendar ciphertext: %v", err)
	}

	_, err = ResolveStartupEncryptionKey(context.Background(), sqlDB, "")
	if !errors.Is(err, ErrStartupEncryptionKeyRequired) {
		t.Fatalf("expected ErrStartupEncryptionKeyRequired, got %v", err)
	}
}

func TestResolveStartupEncryptionKey_InvalidWithCalendarCiphertextFails(t *testing.T) {
	sqlDB, cleanup := newStartupEncryptionTestDB(t)
	defer cleanup()
	u := bootstrapStartupEncryptionUser(t, sqlDB)
	ownerCtx := WithUserID(context.Background(), u.ID)
	st := New(sqlDB, nil)
	project, err := st.CreateProject(ownerCtx, "Calendar Ciphertext Invalid")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := sqlDB.Exec(`
INSERT INTO calendar_sources(project_id, type, name, enabled, secret_enc, url_hash, created_at, updated_at)
VALUES (?, 'ics_feed', 'Family', 1, 'v1:ciphertext', 'hash-family', 1, 1)`, project.ID); err != nil {
		t.Fatalf("seed calendar ciphertext: %v", err)
	}

	_, err = ResolveStartupEncryptionKey(context.Background(), sqlDB, startupEncryptionInvalidKey)
	if !errors.Is(err, ErrStartupEncryptionKeyInvalid) {
		t.Fatalf("expected ErrStartupEncryptionKeyInvalid, got %v", err)
	}
}

func TestResolveStartupEncryptionKey_EmptyNormalUserAllowed(t *testing.T) {
	sqlDB, cleanup := newStartupEncryptionTestDB(t)
	defer cleanup()
	bootstrapStartupEncryptionUser(t, sqlDB)

	res, err := ResolveStartupEncryptionKey(context.Background(), sqlDB, "")
	if err != nil {
		t.Fatalf("ResolveStartupEncryptionKey: %v", err)
	}
	if len(res.Key) != 0 {
		t.Fatalf("expected nil key, got %d bytes", len(res.Key))
	}
	if res.InvalidIgnored {
		t.Fatal("expected InvalidIgnored=false")
	}
}
