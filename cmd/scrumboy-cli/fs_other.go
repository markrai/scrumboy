//go:build !windows

package main

import (
	"errors"
	"os"
	"syscall"
)

// tryLockFile takes an exclusive flock(2) on f without blocking; false means
// another process holds it. The kernel releases it when the holder exits.
func tryLockFile(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return false, nil
	}
	return err == nil, err
}

func unlockFile(f *os.File) { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }

// restrictToOwner makes a file that is about to hold credentials owner-only.
func restrictToOwner(f *os.File) error { return f.Chmod(0o600) }

// renameRetryable reports whether a failed config replacement is worth
// retrying; only Windows refuses to replace a file another process has open.
func renameRetryable(error) bool { return false }
