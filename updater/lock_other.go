//go:build !linux

package updater

import (
	"os"
	"os/exec"
)

func lockState(string) (func(), error)              { return nil, ErrUnsupported }
func syncDirectory(*os.File) error                  { return nil }
func configureCommand(*exec.Cmd)                    {}
func validateOwnedPaths(...string) error            { return ErrUnsupported }
func cacheOwnedByProcess(string) error              { return nil }
func platformExec(string, []string, []string) error { return ErrUnsupported }
func preserveSecretFDs([]int) (func(), error)       { return func() {}, nil }
