//go:build !windows

package githubcredentials

import "os"

func protectCredentialPath(path string, directory bool) error {
	mode := os.FileMode(0600)
	if directory {
		mode = 0700
	}
	return os.Chmod(path, mode)
}

func validateCredentialPath(_ string, info os.FileInfo, _ bool) error {
	if info.Mode().Perm()&0077 != 0 {
		return ErrInsecureStorage
	}
	return nil
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
