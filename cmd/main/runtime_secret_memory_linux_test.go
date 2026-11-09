//go:build linux

package main

import (
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"testing"
)

func TestRuntimeSecretMemoryHardening(t *testing.T) {
	if os.Getenv("DEX_TEST_MEMORY_HARDEN_CHILD") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestRuntimeSecretMemoryHardening$")
		cmd.Env = append(os.Environ(), "DEX_TEST_MEMORY_HARDEN_CHILD=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("hardening subprocess: %v %s", err, out)
		}
		return
	}
	if err := hardenRuntimeSecretMemory(); err != nil {
		t.Fatal(err)
	}
	var limit unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_CORE, &limit); err != nil {
		t.Fatal(err)
	}
	if limit.Cur != 0 || limit.Max != 0 {
		t.Fatal("core limit remains enabled")
	}
	dumpable, err := unix.PrctlRetInt(unix.PR_GET_DUMPABLE, 0, 0, 0, 0)
	if err != nil || dumpable != 0 {
		t.Fatalf("dumpable=%d err=%v", dumpable, err)
	}
}
