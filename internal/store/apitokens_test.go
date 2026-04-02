package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"scrumboy/internal/db"
	"scrumboy/internal/migrate"
)

func TestCreateUserAPITokenAndGetUserByAPIToken(t *testing.T) {
	dir := t.TempDir()
	sqlDB, err := db.Open(filepath.Join(dir, "app.db"), db.Options{
		BusyTimeout:   5000,
		JournalMode:   "WAL",
		Synchronous: "FULL",
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()
	if err := migrate.Apply(context.Background(), sqlDB); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st := New(sqlDB, nil)
	ctx := context.Background()

	u, err := st.BootstrapUser(ctx, "tok@example.com", "password123", "T")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	name := "ci"
	_, plain, _, err := st.CreateUserAPIToken(ctx, u.ID, &name)
	if err != nil {
		t.Fatalf("create api token: %v", err)
	}
	if plain == "" || len(plain) < len(APITokenPrefix)+8 {
		t.Fatalf("unexpected plaintext token: %q", plain)
	}
	if plain[:len(APITokenPrefix)] != APITokenPrefix {
		t.Fatalf("expected %q prefix, got %q", APITokenPrefix, plain[:len(APITokenPrefix)])
	}

	got, err := st.GetUserByAPIToken(ctx, plain)
	if err != nil {
		t.Fatalf("get by api token: %v", err)
	}
	if got.ID != u.ID || got.Email != u.Email {
		t.Fatalf("user mismatch: got %+v want id=%d", got, u.ID)
	}

	if _, err := st.GetUserByAPIToken(ctx, plain+"x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong suffix: got %v want ErrNotFound", err)
	}
	if _, err := st.GetUserByAPIToken(ctx, "not-prefixed"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no prefix: got %v want ErrNotFound", err)
	}
}
