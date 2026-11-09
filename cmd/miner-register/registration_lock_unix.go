//go:build !windows

package main

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func tryExclusiveFileLock(file *os.File) (bool, error) {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return false, nil
	}
	return err == nil, err
}

func unlockExclusiveFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}

func syncParentDirectory(path string) error {
	dir, err := os.Open(filepath.Dir(filepath.Clean(path)))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
