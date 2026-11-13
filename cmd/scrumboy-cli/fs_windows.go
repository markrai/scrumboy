//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// tryLockFile takes an exclusive LockFileEx lock on f without blocking; false
// means another process holds it. Windows releases it when the holder exits.
func tryLockFile(f *os.File) (bool, error) {
	err := windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, new(windows.Overlapped))
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return err == nil, err
}

func unlockFile(f *os.File) {
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, new(windows.Overlapped))
}

// restrictToOwner makes a file that is about to hold credentials readable only
// by the current user (plus SYSTEM and Administrators). Chmod only toggles the
// read-only attribute on Windows, so without this a config outside the user
// profile (--config, SCRUMBOY_CLI_CONFIG) inherits e.g. "Authenticated Users:
// Modify" from C:\ and any local account can read the token.
func restrictToOwner(f *os.File) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("protect config: %w", err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")(A;;FA;;;SY)(A;;FA;;;BA)")
	if err != nil {
		return fmt.Errorf("protect config: %w", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("protect config: %w", err)
	}
	if err := windows.SetNamedSecurityInfo(f.Name(), windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		return fmt.Errorf("protect config: %w", err)
	}
	return nil
}

// renameRetryable: Windows refuses to replace a file that another process has
// open (a concurrent scrumboy-cli reading the config, an antivirus or indexer
// scan) until that handle closes.
func renameRetryable(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}
