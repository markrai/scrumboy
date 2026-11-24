//go:build windows

package githubcredentials

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func assertCredentialACL(t *testing.T, path string, directory bool) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("credential DACL is not protected: %s", sd)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if dacl == nil {
		t.Fatal("credential path has a NULL DACL")
	}
	user, system, admins, err := credentialSIDs()
	if err != nil {
		t.Fatal(err)
	}
	allowed := []*windows.SID{user, system, admins}
	seen := make([]bool, len(allowed))
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			t.Fatal(err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			t.Fatalf("unexpected ACE type %d: %s", ace.Header.AceType, sd)
		}
		if ace.Header.AceFlags&windows.INHERITED_ACE != 0 {
			t.Fatalf("inherited ACE remained: %s", sd)
		}
		if !directory && ace.Header.AceFlags != 0 {
			t.Fatalf("file ACE has inheritance flags %#x: %s", ace.Header.AceFlags, sd)
		}
		if ace.Mask != credentialAllAccess {
			t.Fatalf("ACE mask %#x: %s", ace.Mask, sd)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		matched := -1
		for n, candidate := range allowed {
			if sid.Equals(candidate) {
				matched = n
				break
			}
		}
		if matched < 0 || seen[matched] {
			t.Fatalf("unexpected or duplicate trustee %s: %s", sid, sd)
		}
		seen[matched] = true
	}
	for _, ok := range seen {
		if !ok {
			t.Fatalf("missing required trustee: %s", sd)
		}
	}
}

func TestCredentialStoreProtectsWindowsACLs(t *testing.T) {
	user, _, _, err := credentialSIDs()
	if err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(dataDir, 0700); err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString(
		"D:P(A;OICI;FA;;;" + user.String() + ")(A;OICI;FR;;;AU)",
	)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(
		dataDir,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	store, err := New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(testCredentials()); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dataDir, directoryName)
	assertCredentialACL(t, root, true)
	assertCredentialACL(t, filepath.Join(root, credentialsFileName), false)
	if _, found, err := store.Load(); err != nil || !found {
		t.Fatalf("protected bundle did not reload: found=%v err=%v", found, err)
	}
}

func TestCredentialStoreRejectsInsecureWindowsACL(t *testing.T) {
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, directoryName)
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	bundle := testCredentials()
	data := []byte(`{"version":1,"appId":` + "73482" + `,"slug":"` + bundle.Slug + `","privateKey":` + """ + strings.ReplaceAll(string(bundle.PrivateKey), "\n", "\\n") + """ + `,"webhookSecret":"` + bundle.WebhookSecret + `"}`)
	if err := os.WriteFile(filepath.Join(root, credentialsFileName), data, 0600); err != nil {
		t.Fatal(err)
	}
	user, _, _, err := credentialSIDs()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString(
		"D:P(A;OICI;FA;;;" + user.String() + ")(A;OICI;FR;;;AU)",
	)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(
		root,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	store, err := New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Load(); !errors.Is(err, ErrInsecureStorage) {
		t.Fatalf("insecure Windows ACL accepted: %v", err)
	}
}
