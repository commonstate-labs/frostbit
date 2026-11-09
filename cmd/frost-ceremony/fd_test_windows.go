//go:build windows

package main

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

func duplicateFDForTest(t *testing.T, file *os.File) int {
	t.Helper()
	var duplicate windows.Handle
	process := windows.CurrentProcess()
	if err := windows.DuplicateHandle(
		process,
		windows.Handle(file.Fd()),
		process,
		&duplicate,
		0,
		false,
		windows.DUPLICATE_SAME_ACCESS,
	); err != nil {
		t.Fatalf("duplicate test handle: %v", err)
	}
	return int(duplicate)
}

func closeFDForTest(t *testing.T, fd int) {
	t.Helper()
	if err := windows.CloseHandle(windows.Handle(fd)); err != nil {
		t.Fatalf("close test handle: %v", err)
	}
}
