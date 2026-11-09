//go:build !linux && !windows

package nodekey

import (
	"errors"
	"os"
)

func openSafe(path string, flags int, perm os.FileMode) (*os.File, error) {
	info, err := os.Lstat(path)
	if err == nil && !info.Mode().IsRegular() {
		return nil, errors.New("key file must be regular, not a symlink")
	}
	return os.OpenFile(path, flags, perm)
}

func checkFilePermissions(mode os.FileMode, private bool) error {
	if mode.Perm()&0o022 != 0 || private && mode.Perm()&0o077 != 0 {
		return errors.New("unsafe key file permissions")
	}
	return nil
}
