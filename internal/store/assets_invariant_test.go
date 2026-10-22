package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/google/uuid"
)

// assertAssetInvariants checks the persistent asset invariants that must hold after every
// committed state: I2 (unreferenced_at NULL iff referenced), A5 (same-project references),
// and referential sanity of todo_assets.
func assertAssetInvariants(t *testing.T, db *sql.DB) {
	t.Helper()
	var n int
	if err := db.QueryRow(`
SELECT COUNT(*) FROM project_assets pa
WHERE (pa.unreferenced_at IS NULL AND NOT EXISTS (SELECT 1 FROM todo_assets ta WHERE ta.asset_id = pa.id))
   OR (pa.unreferenced_at IS NOT NULL AND EXISTS (SELECT 1 FROM todo_assets ta WHERE ta.asset_id = pa.id))`).Scan(&n); err != nil {
		t.Fatalf("invariant I2 query: %v", err)
	}
	if n != 0 {
		t.Fatalf("invariant I2 violated: %d assets with inconsistent unreferenced_at", n)
	}
	if err := db.QueryRow(`
SELECT COUNT(*) FROM todo_assets ta
JOIN todos t ON t.id = ta.todo_id
JOIN project_assets pa ON pa.id = ta.asset_id
WHERE t.project_id <> pa.project_id`).Scan(&n); err != nil {
		t.Fatalf("invariant A5 query: %v", err)
	}
	if n != 0 {
		t.Fatalf("invariant A5 violated: %d cross-project references", n)
	}
	if err := db.QueryRow(`
SELECT COUNT(*) FROM todo_assets ta
WHERE NOT EXISTS (SELECT 1 FROM todos t WHERE t.id = ta.todo_id)
   OR NOT EXISTS (SELECT 1 FROM project_assets pa WHERE pa.id = ta.asset_id)`).Scan(&n); err != nil {
		t.Fatalf("todo_assets FK query: %v", err)
	}
	if n != 0 {
		t.Fatalf("todo_assets has %d dangling rows", n)
	}
}

func newTestAssetUUID(t *testing.T) string {
	t.Helper()
	u, err := uuid.NewRandom()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}
	return u.String()
}

func randomSHA256Hex(t *testing.T) string {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return hex.EncodeToString(b)
}

// insertTestAsset inserts an asset row (no file) for store-level tests.
func insertTestAsset(t *testing.T, db *sql.DB, projectID int64) ProjectAsset {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	a, err := insertProjectAssetTx(t.Context(), tx, NewProjectAsset{
		UUID:      newTestAssetUUID(t),
		ProjectID: projectID,
		MimeType:  "image/png",
		SizeBytes: 123,
		Width:     10,
		Height:    20,
		SHA256:    randomSHA256Hex(t),
	}, 1_700_000_000_000)
	if err != nil {
		t.Fatalf("insertProjectAssetTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return a
}

func linkTestAsset(t *testing.T, db *sql.DB, todoID, assetID int64) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO todo_assets(todo_id, asset_id, created_at) VALUES (?, ?, ?)`, todoID, assetID, 1); err != nil {
		t.Fatalf("link asset: %v", err)
	}
}

func assetUnreferencedAt(t *testing.T, db *sql.DB, assetID int64) sql.NullInt64 {
	t.Helper()
	var v sql.NullInt64
	if err := db.QueryRow(`SELECT unreferenced_at FROM project_assets WHERE id = ?`, assetID).Scan(&v); err != nil {
		t.Fatalf("read unreferenced_at: %v", err)
	}
	return v
}

func countRows(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", fmt.Sprint(query), err)
	}
	return n
}
