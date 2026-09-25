package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"scrumboy/internal/db"
	"scrumboy/internal/migrate"
)

func TestCreateUserAPITokenAndGetUserByAPIToken(t *testing.T) {
	dir := t.TempDir()
	sqlDB, err := db.Open(filepath.Join(dir, "app.db"), db.Options{
		BusyTimeout: 5000,
		JournalMode: "WAL",
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
	_, plain, _, err := st.CreateUserAPIToken(ctx, u.ID, &name, false)
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

func TestDeleteUserDoesNotProceedAfterRequesterDemotedBeforeMutation(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "authorization-race.db")
	primaryDB, err := db.Open(databasePath, db.Options{
		BusyTimeout: 5000,
		JournalMode: "WAL",
		Synchronous: "FULL",
	})
	if err != nil {
		t.Fatalf("open primary db: %v", err)
	}
	defer func() { _ = primaryDB.Close() }()
	if err := migrate.Apply(context.Background(), primaryDB); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st := New(primaryDB, nil)
	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "race-owner@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	target, err := st.CreateUser(ctx, "race-target@example.com", "password123", "Target")
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	serviceTokenID, _, _, err := st.CreateUserAPIToken(ctx, target.ID, stringPtr("race-service"), true)
	if err != nil {
		t.Fatalf("create service token: %v", err)
	}

	concurrentDB, err := db.Open(databasePath, db.Options{
		BusyTimeout: 5000,
		JournalMode: "WAL",
		Synchronous: "FULL",
	})
	if err != nil {
		t.Fatalf("open concurrent db: %v", err)
	}
	defer func() { _ = concurrentDB.Close() }()
	demotionTx, err := concurrentDB.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		t.Fatalf("begin demotion: %v", err)
	}
	if _, err := demotionTx.ExecContext(ctx, `UPDATE users SET system_role = 'user' WHERE id = ?`, owner.ID); err != nil {
		_ = demotionTx.Rollback()
		t.Fatalf("stage demotion: %v", err)
	}

	deleteResult := make(chan error, 1)
	go func() {
		deleteResult <- st.DeleteUser(ctx, owner.ID, target.ID)
	}()
	var (
		deleteErr      error
		deleteReturned bool
	)
	select {
	case deleteErr = <-deleteResult:
		deleteReturned = true
	case <-time.After(200 * time.Millisecond):
	}
	if err := demotionTx.Commit(); err != nil {
		t.Fatalf("commit demotion: %v", err)
	}
	if !deleteReturned {
		deleteErr = <-deleteResult
	}
	if deleteErr == nil {
		t.Fatal("DeleteUser succeeded using owner authorization read before a concurrent demotion")
	}
	if _, err := st.GetUser(ctx, target.ID); err != nil {
		t.Fatalf("target was deleted after requester lost owner role: %v", err)
	}
	assertAPITokenRow(t, st.db, serviceTokenID, target.ID, true, false)
	if n := countArchivedServiceAPITokens(t, st.db); n != 0 {
		t.Fatalf("archive rows after rejected deletion = %d, want 0", n)
	}
}
