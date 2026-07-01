package db

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testOptions() Options {
	return Options{BusyTimeout: 5000, JournalMode: "WAL", Synchronous: "FULL"}
}

func openTempDB(t *testing.T, opts Options) (*sql.DB, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "data", "app.db")
	sqlDB, err := Open(path, opts)
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return sqlDB, path
}

func pragmaInt(t *testing.T, sqlDB *sql.DB, name string) int {
	t.Helper()

	var got int
	if err := sqlDB.QueryRow("PRAGMA " + name).Scan(&got); err != nil {
		t.Fatalf("PRAGMA %s: %v", name, err)
	}
	return got
}

func pragmaString(t *testing.T, sqlDB *sql.DB, name string) string {
	t.Helper()

	var got string
	if err := sqlDB.QueryRow("PRAGMA " + name).Scan(&got); err != nil {
		t.Fatalf("PRAGMA %s: %v", name, err)
	}
	return got
}

func TestOpenCreatesParentDirectoryAndPings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "app.db")
	parent := filepath.Dir(path)
	if _, err := os.Stat(parent); !os.IsNotExist(err) {
		t.Fatalf("parent directory exists before Open or stat failed: %v", err)
	}

	sqlDB, err := Open(path, testOptions())
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	info, err := os.Stat(parent)
	if err != nil {
		t.Fatalf("stat parent directory: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("parent path %q is not a directory", parent)
	}
	if err := sqlDB.Ping(); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	var got int
	if err := sqlDB.QueryRow("SELECT 1").Scan(&got); err != nil {
		t.Fatalf("SELECT 1: %v", err)
	}
	if got != 1 {
		t.Fatalf("SELECT 1 = %d, want 1", got)
	}
}

func TestOpenAppliesSQLitePragmas(t *testing.T) {
	sqlDB, _ := openTempDB(t, testOptions())

	if got := pragmaInt(t, sqlDB, "busy_timeout"); got != 5000 {
		t.Fatalf("PRAGMA busy_timeout = %d, want 5000", got)
	}
	if got := strings.ToLower(pragmaString(t, sqlDB, "journal_mode")); got != "wal" {
		t.Fatalf("PRAGMA journal_mode = %q, want wal", got)
	}
	if got := pragmaInt(t, sqlDB, "synchronous"); got != 2 {
		t.Fatalf("PRAGMA synchronous = %d, want 2", got)
	}
	if got := pragmaInt(t, sqlDB, "foreign_keys"); got != 1 {
		t.Fatalf("PRAGMA foreign_keys = %d, want 1", got)
	}
}

func TestOpenConstrainsConnectionPool(t *testing.T) {
	sqlDB, _ := openTempDB(t, testOptions())

	if got := sqlDB.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("MaxOpenConnections = %d, want 1", got)
	}
}

func TestOpenRejectsZeroBusyTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "app.db")
	sqlDB, err := Open(path, Options{BusyTimeout: 0, JournalMode: "WAL", Synchronous: "FULL"})
	if sqlDB != nil {
		_ = sqlDB.Close()
		t.Fatalf("returned *sql.DB = %v, want nil", sqlDB)
	}
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "busy_timeout is 0") {
		t.Fatalf("error = %q, want busy_timeout is 0", err.Error())
	}
}
