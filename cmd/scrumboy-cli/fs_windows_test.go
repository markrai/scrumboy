//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

const fileAllAccess windows.ACCESS_MASK = 0x1F01FF

// TestConfigACLIsOwnerOnlyOnWindows: Chmod(0600) does nothing for access on
// Windows. A config saved under a directory that grants other accounts access
// (like C:\, which gives Authenticated Users Modify) must not inherit it.
func TestConfigACLIsOwnerOnlyOnWindows(t *testing.T) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	me := user.User.Sid.String()
	dir := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Parent ACL children inherit: full control for us, read for every authenticated user.
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + me + ")(A;OICI;FR;;;AU)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, "config.json")
	for i := 0; i < 2; i++ { // first write, then replacement of an existing file
		if err := saveConfig(path, fileConfig{Server: "https://a", Token: "sb_x"}); err != nil {
			t.Fatal(err)
		}
		assertOwnerOnlyDACL(t, path, user.User.Sid)
	}
}

// assertOwnerOnlyDACL compares trustee SIDs rather than SDDL text: Windows
// prints well-known accounts as aliases (the built-in Administrator account
// as LA, for example), so the user's numeric SID need not appear in the SDDL.
func assertOwnerOnlyDACL(t *testing.T, path string, me *windows.SID) {
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
		t.Fatalf("config DACL is not protected from inheritance: %s", sd)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if dacl == nil {
		t.Fatalf("config has a NULL DACL, which grants everyone full access")
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatal(err)
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		name string
		sid  *windows.SID
	}{{"current user", me}, {"SYSTEM", system}, {"Administrators", admins}}
	seen := map[string]bool{}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			t.Fatal(err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			t.Fatalf("config ACE %d has type %d, want access-allowed: %s", i, ace.Header.AceType, sd)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		name := ""
		for _, w := range want {
			if sid.Equals(w.sid) {
				name = w.name
			}
		}
		switch {
		case name == "":
			t.Fatalf("config ACL grants %s, which is not the current user, SYSTEM or Administrators: %s", sid, sd)
		case seen[name]:
			t.Fatalf("config ACL lists %s more than once: %s", name, sd)
		case ace.Header.AceFlags&windows.INHERITED_ACE != 0:
			t.Fatalf("config ACE for %s is inherited: %s", name, sd)
		case ace.Mask != fileAllAccess:
			t.Fatalf("config ACE for %s has mask %#x, want full access %#x: %s", name, ace.Mask, fileAllAccess, sd)
		}
		seen[name] = true
	}
	for _, w := range want {
		if !seen[w.name] {
			t.Fatalf("config ACL does not grant %s (%s): %s", w.name, w.sid, sd)
		}
	}
}
