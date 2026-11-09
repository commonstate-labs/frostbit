//go:build !windows

package main

import (
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func duplicateFDForTest(t *testing.T, file *os.File) int {
	t.Helper()
	duplicate, err := unix.Dup(int(file.Fd()))
	if err != nil {
		t.Fatalf("duplicate test descriptor: %v", err)
	}
	return duplicate
}

func closeFDForTest(t *testing.T, fd int) {
	t.Helper()
	if err := unix.Close(fd); err != nil {
		t.Fatalf("close test descriptor: %v", err)
	}
}
