//go:build linux

package main

import (
	"fmt"
	"golang.org/x/sys/unix"
)

                                                                              
                                                                           
func hardenRuntimeSecretMemory() error {
	if err := unix.Setrlimit(unix.RLIMIT_CORE, &unix.Rlimit{Cur: 0, Max: 0}); err != nil {
		return fmt.Errorf("disable core dumps: %w", err)
	}
	if err := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
		return fmt.Errorf("disable process dumps: %w", err)
	}
	return nil
}
