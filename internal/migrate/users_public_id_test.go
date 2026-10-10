package migrate

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"
)

const usersPublicIDMigration = "076_add_users_public_id.sql"

var uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestMigration076BackfillsExistingUsersWithDistinctUUIDs(t *testing.T) {
	ctx := context.Background()
	sqlDB := openRawTestDB(t)
	applyMigrationsBefore(t, sqlDB, usersPublicIDMigration)

	now := time.Now().UTC().UnixMilli()
	for i, email := range []string{"a@example.com", "b@example.com", "c@example.com"} {
		if _, err := sqlDB.ExecContext(ctx, `INSERT INTO users(id, email, created_at, name, system_role) VALUES (?, ?, ?, ?, 'user')`, i+1, email, now, email); err != nil {
			t.Fatalf("insert pre-076 user: %v", err)
		}
	}
	if columnExists(t, sqlDB, "users", "public_id") {
		t.Fatal("public_id exists before migration 076")
	}

	if err := applyOne(ctx, sqlDB, usersPublicIDMigration); err != nil {
		t.Fatalf("apply 076: %v", err)
	}

	seen := map[string]bool{}
	rows, err := sqlDB.QueryContext(ctx, `SELECT public_id FROM users ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		if !uuidV4.MatchString(id) {
			t.Fatalf("backfilled public_id %q is not a UUIDv4", id)
		}
		if seen[id] {
			t.Fatalf("duplicate public_id %q", id)
		}
		seen[id] = true
		n++
	}
	if n != 3 {
		t.Fatalf("backfilled %d users, want 3", n)
	}
	if !indexExists(t, sqlDB, "idx_users_public_id") {
		t.Fatal("unique index idx_users_public_id missing")
	}
}

func TestMigration076NewUsersGetAnIDAndItIsImmutable(t *testing.T) {
	ctx := context.Background()
	sqlDB := openMigratedDB(t)
	now := time.Now().UTC().UnixMilli()

	insert := func(id int64, email string) string {
		t.Helper()
		// Deliberately the same column list the real insert sites use: no public_id.
		if _, err := sqlDB.ExecContext(ctx, `INSERT INTO users(id, email, created_at, name, system_role) VALUES (?, ?, ?, ?, 'user')`, id, email, now, email); err != nil {
			t.Fatalf("insert user: %v", err)
		}
		var pid string
		if err := sqlDB.QueryRowContext(ctx, `SELECT public_id FROM users WHERE id = ?`, id).Scan(&pid); err != nil {
			t.Fatal(err)
		}
		if !uuidV4.MatchString(pid) {
			t.Fatalf("new user's public_id %q is not a UUIDv4", pid)
		}
		return pid
	}
	first := insert(1, "a@example.com")
	second := insert(2, "b@example.com")
	if first == second {
		t.Fatal("two users share a public_id")
	}

	// The scenario the column exists for: delete a user and let SQLite reuse the integer id.
	if _, err := sqlDB.ExecContext(ctx, `DELETE FROM users WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	reused := insert(2, "c@example.com")
	if reused == second {
		t.Fatal("a new user that reused users.id 2 inherited the deleted user's public_id")
	}

	// Immutable: any change to a set public_id aborts; a no-op write is allowed.
	if _, err := sqlDB.ExecContext(ctx, `UPDATE users SET public_id = 'x' WHERE id = 1`); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("changing public_id: err = %v, want an immutability error", err)
	}
	if _, err := sqlDB.ExecContext(ctx, `UPDATE users SET public_id = NULL WHERE id = 1`); err == nil {
		t.Fatal("clearing public_id must be rejected")
	}
	if _, err := sqlDB.ExecContext(ctx, `UPDATE users SET public_id = public_id, name = 'renamed' WHERE id = 1`); err != nil {
		t.Fatalf("a no-op public_id write must be allowed: %v", err)
	}
	// Two users can never share one.
	if _, err := sqlDB.ExecContext(ctx, `INSERT INTO users(id, email, created_at, name, system_role, public_id) VALUES (9, 'd@example.com', ?, 'd', 'user', ?)`, now, first); err == nil {
		t.Fatal("duplicate public_id insert must be rejected by the unique index")
	}
}
