//go:build linux

package nodekey

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

                                                                                     
func openSafe(path string, flags int, perm os.FileMode) (*os.File, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	dir, err := openKeyParent(abs, nil)
	if err != nil {
		return nil, err
	}
	defer unix.Close(dir)
	fd, err := unix.Openat(dir, filepath.Base(abs), flags|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, uint32(perm.Perm()))
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), abs), nil
}

                                                                              
                                                                                 
func openKeyParent(abs string, validateDirectory func(int) error) (int, error) {
	if abs == "/" {
		return -1, errors.New("key path must name a file")
	}
	const flags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC | unix.O_NOFOLLOW
	dir, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	for part := range strings.SplitSeq(strings.TrimPrefix(filepath.Dir(abs), "/"), "/") {
		if part == "" {
			continue
		}
		if validateDirectory != nil {
			if err := validateDirectory(dir); err != nil {
				unix.Close(dir)
				return -1, err
			}
		}
		next, err := unix.Openat(dir, part, flags, 0)
		unix.Close(dir)
		if err != nil {
			return -1, err
		}
		dir = next
	}
	if validateDirectory != nil {
		if err := validateDirectory(dir); err != nil {
			unix.Close(dir)
			return -1, err
		}
	}
	return dir, nil
}

func checkFilePermissions(mode os.FileMode, private bool) error {
	if mode.Perm()&0o022 != 0 {
		return errors.New("key file must not be writable by group or others")
	}
	if private && mode.Perm()&0o077 != 0 {
		return errors.New("plaintext import key file must be private (0600 or stricter)")
	}
	return nil
}
