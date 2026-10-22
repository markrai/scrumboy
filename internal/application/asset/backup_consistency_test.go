package asset

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"scrumboy/internal/db"
)

// copyTree copies the assets directory, as an operator's file copy would.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// missingFilesInBackup counts snapshot rows whose file is absent from the copied assets dir.
func missingFilesInBackup(t *testing.T, snapshotPath, assetsCopy string) int {
	t.Helper()
	snap, err := db.Open(snapshotPath, db.Options{BusyTimeout: 5000, JournalMode: "WAL", Synchronous: "FULL"})
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	rows, err := snap.Query(`SELECT uuid FROM project_assets`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	missing := 0
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(assetsCopy, id[:2], id+".png")); err != nil {
			missing++
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return missing
}

func runBackupScenario(t *testing.T, withHold bool) int {
	f := newJanitorFixture(t)
	j := f.janitor(true)
	for range 3 {
		f.addAsset(t, 1) // GC-eligible drafts
	}
	if withHold {
		release := f.holdRemovals(t)
		defer release()
	}
	backupDir := t.TempDir()
	snapshot := filepath.Join(backupDir, "app.db")
	if _, err := f.sqlDB.Exec(`VACUUM INTO ?`, snapshot); err != nil {
		t.Fatal(err)
	}
	// Deletions land between the DB snapshot and the file copy.
	if err := j.RunReconcile(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err := j.RunGC(f.ctx); err != nil {
		t.Fatal(err)
	}
	f.addAsset(t, 1) // concurrent upload after the snapshot: an extra file, harmless
	copyTree(t, f.blobs.Root(), filepath.Join(backupDir, "assets"))
	return missingFilesInBackup(t, snapshot, filepath.Join(backupDir, "assets"))
}

func TestOnlineBackupConsistencyUnderHold(t *testing.T) {
	if n := runBackupScenario(t, true); n != 0 {
		t.Fatalf("backup taken under the removal hold is missing %d files", n)
	}
	// Without the hold the same ordering loses files, which is what the hold prevents.
	if n := runBackupScenario(t, false); n == 0 {
		t.Fatal("expected the unheld backup to demonstrate missing files")
	}
}
