package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"scrumboy/internal/crypto"
	"scrumboy/internal/db"
	"scrumboy/internal/migrate"
)

func newTestStoreWith2FA(t *testing.T) (*Store, func()) {
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
	key, err := crypto.DecodeKey("YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXoxMjM0NTY=")
	if err != nil {
		_ = sqlDB.Close()
		t.Fatalf("decode key: %v", err)
	}
	st := New(sqlDB, &StoreOptions{EncryptionKey: key})
	return st, func() { _ = sqlDB.Close() }
}

func TestCreateAndGetLogin2FAPending(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	ctx := context.Background()

	u, err := st.BootstrapUser(ctx, "u@x.com", "password123", "U")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}

	token, expiresAt, err := st.CreateLogin2FAPending(ctx, u.ID, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}
	if expiresAt.IsZero() {
		t.Fatal("expected non-zero expiresAt")
	}

	got, attemptCount, err := st.GetUserByLogin2FAPendingToken(ctx, token)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != u.ID {
		t.Fatalf("user id %d != %d", got.ID, u.ID)
	}
	if attemptCount != 0 {
		t.Fatalf("attempt count %d != 0", attemptCount)
	}
}

func TestIncrementAttemptRevokesAfterMax(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	ctx := context.Background()

	u, err := st.BootstrapUser(ctx, "u@x.com", "password123", "U")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	token, _, err := st.CreateLogin2FAPending(ctx, u.ID, 0)
	if err != nil {
		t.Fatalf("CreateLogin2FAPending: %v", err)
	}

	for i := 0; i < 5; i++ {
		incErr := st.IncrementLogin2FAPendingAttempt(ctx, token)
		if incErr != nil {
			t.Fatalf("increment %d: %v", i, incErr)
		}
	}
	err = st.IncrementLogin2FAPendingAttempt(ctx, token)
	if !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("expected ErrTooManyAttempts, got %v", err)
	}

	_, _, err = st.GetUserByLogin2FAPendingToken(ctx, token)
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound after revoke, got %v", err)
	}
}

func TestRecoveryCodesAndConsume(t *testing.T) {
	st, cleanup := newTestStoreWith2FA(t)
	defer cleanup()
	ctx := context.Background()

	u, err := st.BootstrapUser(ctx, "u@x.com", "password123", "U")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	codes := GenerateRecoveryCodes(3)
	if len(codes) != 3 {
		t.Fatalf("expected 3 codes, got %d", len(codes))
	}
	for _, c := range codes {
		if len(c) != 9 {
			t.Fatalf("expected xxxx-xxxx format (9 chars), got %q len=%d", c, len(c))
		}
	}

	if err := st.AddRecoveryCodes(ctx, u.ID, codes); err != nil {
		t.Fatalf("add: %v", err)
	}

	consumed, err := st.ConsumeRecoveryCode(ctx, u.ID, codes[0])
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if !consumed {
		t.Fatal("expected consumed=true")
	}

	consumed, _ = st.ConsumeRecoveryCode(ctx, u.ID, codes[0])
	if consumed {
		t.Fatal("expected consumed=false on reuse")
	}
}

func TestUserIsTwoFactorActive(t *testing.T) {
	u := User{TwoFactorEnabled: true}
	if !u.IsTwoFactorActive() {
		t.Fatal("expected true when enabled")
	}
	u.TwoFactorEnabled = false
	if u.IsTwoFactorActive() {
		t.Fatal("expected false when disabled")
	}
}
