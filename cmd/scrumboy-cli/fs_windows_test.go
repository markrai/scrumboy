//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

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
		got, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		sddl := got.String()
		if !strings.HasPrefix(sddl, "D:P") || strings.Contains(sddl, ";;;AU)") || strings.Contains(sddl, ";;;BU)") || strings.Contains(sddl, ";;;WD)") {
			t.Fatalf("config ACL is not owner-only: %s", sddl)
		}
		if !strings.Contains(sddl, ";;;"+me+")") {
			t.Fatalf("config ACL does not grant the current user: %s", sddl)
		}
	}
}
