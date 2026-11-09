//go:build linux

package main

import (
	"bytes"
	"dex/nodekey"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func passphraseTestFD(t *testing.T, password []byte) int {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(password); err != nil {
		t.Fatal(err)
	}
	w.Close()
	fd, err := unix.Dup(int(r.Fd()))
	r.Close()
	if err != nil {
		t.Fatal(err)
	}
	return fd
}
func TestRuntimeKeystoreInheritedFDUnlock(t *testing.T) {
	key := make([]byte, 32)
	key[31] = 7
	defer wipeBytes(key)
	password := []byte("fixture-only-passphrase-123")
	defer wipeBytes(password)
	path := filepath.Join(t.TempDir(), "miner.json")
	if err := nodekey.EncryptFile(path, key, password); err != nil {
		t.Fatal(err)
	}
	for _, success := range []bool{true, false} {
		input := password
		if !success {
			input = []byte("wrong-fixture-passphrase")
		}
		fd := passphraseTestFD(t, input)
		opts := emptySecretOptions()
		opts.KeystorePath = path
		opts.KeystorePasswordFD = fd
		err := loadRuntimeSecrets(&opts)
		if _, check := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); check != unix.EBADF {
			t.Fatal("password descriptor remained open")
		}
		if success {
			if err != nil {
				t.Fatal(err)
			}
			want := testRuntimeKey(t, "07")
			if opts.KeyManager == nil || opts.KeyManager.GetAddress() != want.GetAddress() || !bytes.Equal(opts.KeyManager.PublicKeyBytes(), want.PublicKeyBytes()) {
				t.Fatal("unlocked identity changed")
			}
			if opts.ArbitrageKeyManager != nil {
				t.Fatal("miner identity reused for arbitrage")
			}
		} else if err == nil || opts.KeyManager != nil {
			t.Fatal("wrong password admitted an identity")
		}
	}
}
func TestRuntimeKeystoreWithoutTerminalFails(t *testing.T) {
	if os.Getenv("DEX_TEST_KEYSTORE_NO_TTY") == "1" {
		opts := emptySecretOptions()
		opts.KeystorePath = filepath.Join(t.TempDir(), "not-opened-without-terminal.json")
		if err := os.WriteFile(opts.KeystorePath, []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := loadRuntimeSecrets(&opts); err == nil || !strings.Contains(err.Error(), "terminal") {
			t.Fatalf("no-terminal startup error=%v", err)
		}
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestRuntimeKeystoreWithoutTerminalFails$")
	cmd.Env = append(os.Environ(), "DEX_TEST_KEYSTORE_NO_TTY=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("noninteractive child: %v %s", err, out)
	}
}
