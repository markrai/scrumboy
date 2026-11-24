package githubcredentials

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"scrumboy/internal/integration/githubmanifest"
)

func testCredentials() githubmanifest.Credentials {
	return githubmanifest.Credentials{
		AppID: 7139,
		Slug: "local-scrumboy-app",
		PrivateKey: []byte("private-test-pem-bytes"),
		WebhookSecret: strings.Repeat("w", 40),
	}
}

func TestCredentialStoreSaveLoadRestartAndNoOverwrite(t *testing.T) {
	dataDir := t.TempDir()
	store, err := New(dataDir)
	if err != nil { t.Fatal(err) }
	if _, exists, err := store.Load(); err != nil || exists {
		t.Fatalf("unexpected initial bundle: %v %v", exists, err)
	}
	original := testCredentials()
	if err := store.Save(original); err != nil { t.Fatal(err) }
	again, err := New(dataDir)
	if err != nil { t.Fatal(err) }
	loaded, exists, err := again.Load()
	if err != nil || !exists || loaded.AppID != original.AppID ||
		loaded.Slug != original.Slug || string(loaded.PrivateKey) != string(original.PrivateKey) ||
		loaded.WebhookSecret != original.WebhookSecret {
		t.Fatalf("restart recovery mismatch: exists=%v err=%v", exists, err)
	}
	other := testCredentials()
	other.AppID = 8000
	if err := again.Save(other); !errors.Is(err, ErrAlreadyConfigured) {
		t.Fatalf("unexpected overwrite result: %v", err)
	}
	bytes, err := os.ReadFile(filepath.Join(dataDir, directoryName, credentialsFileName))
	if err != nil { t.Fatal(err) }
	if strings.Contains(string(bytes), "8000") { t.Fatal("credential overwrite occurred") }
	if runtime.GOOS != "windows" {
		directory, err := os.Stat(filepath.Join(dataDir, directoryName))
		if err != nil || directory.Mode().Perm() != 0700 { t.Fatalf("directory permissions: %v", err) }
		file, err := os.Stat(filepath.Join(dataDir, directoryName, credentialsFileName))
		if err != nil || file.Mode().Perm() != 0600 { t.Fatalf("file permissions: %v", err) }
	}
}

func TestCredentialStoreRestoresProtectedBackupBundle(t *testing.T) {
	sourceDir := t.TempDir()
	source, err := New(sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	want := testCredentials()
	if err := source.Save(want); err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(filepath.Join(sourceDir, directoryName, credentialsFileName))
	if err != nil {
		t.Fatal(err)
	}

	restoreDir := t.TempDir()
	root := filepath.Join(restoreDir, directoryName)
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := protectCredentialPath(root, true); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(root, credentialsFileName)
	if err := os.WriteFile(name, backup, 0600); err != nil {
		t.Fatal(err)
	}
	if err := protectCredentialPath(name, false); err != nil {
		t.Fatal(err)
	}
	restored, err := New(restoreDir)
	if err != nil {
		t.Fatal(err)
	}
	got, found, err := restored.Load()
	if err != nil || !found {
		t.Fatalf("restored bundle unavailable: found=%v err=%v", found, err)
	}
	if got.AppID != want.AppID || got.Slug != want.Slug ||
		string(got.PrivateKey) != string(want.PrivateKey) || got.WebhookSecret != want.WebhookSecret {
		t.Fatal("restored credentials differ from backup")
	}
}

func TestCredentialStoreFailClosedOnInvalidOrInsecureBundle(t *testing.T) {
	dataDir := t.TempDir()
	store, err := New(dataDir)
	if err != nil { t.Fatal(err) }
	bad := testCredentials()
	bad.WebhookSecret = "short"
	if err := store.Save(bad); !errors.Is(err, ErrInvalidCredentials) { t.Fatalf("invalid secret accepted: %v", err) }
	if _, exists, err := store.Load(); err != nil || exists { t.Fatalf("invalid save left directory: %v %v", exists, err) }
	root := filepath.Join(dataDir, directoryName)
	if err := os.Mkdir(root, 0700); err != nil { t.Fatal(err) }
	if err := os.WriteFile(filepath.Join(root, credentialsFileName), []byte("{invalid"), 0600); err != nil { t.Fatal(err) }
	if _, _, err := store.Load(); !errors.Is(err, ErrInvalidCredentials) { t.Fatalf("corrupt bundle accepted: %v", err) }
	if runtime.GOOS != "windows" {
		if err := os.Chmod(root, 0755); err != nil { t.Fatal(err) }
		if _, _, err := store.Load(); !errors.Is(err, ErrInsecureStorage) { t.Fatalf("public directory accepted: %v", err) }
	}
}

func TestCredentialStoreRejectsSymlinkedCredentialDirectory(t *testing.T) {
	dataDir := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dataDir, directoryName)); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	store, err := New(dataDir)
	if err != nil { t.Fatal(err) }
	if _, _, err := store.Load(); !errors.Is(err, ErrInsecureStorage) {
		t.Fatalf("symlink accepted: %v", err)
	}
	if err := store.Save(testCredentials()); !errors.Is(err, ErrAlreadyConfigured) {
		t.Fatalf("overwrite of linked directory not blocked: %v", err)
	}
}

func TestCredentialStoreConcurrentSavesDoNotReplaceExistingBundle(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil { t.Fatal(err) }
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for id := int64(1); id <= 2; id++ {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			c := testCredentials()
			c.AppID = id
			results <- store.Save(c)
		}(id)
	}
	wg.Wait()
	close(results)
	success := 0
	conflicts := 0
	for err := range results {
		switch {
		case err == nil: success++
		case errors.Is(err, ErrAlreadyConfigured): conflicts++
		default: t.Fatalf("unexpected save: %v", err)
		}
	}
	if success != 1 || conflicts != 1 { t.Fatalf("success=%d conflicts=%d", success, conflicts) }
}
