//go:build linux

package nodekey

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestLinuxPrivatePermissionsAndNoLinks(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "original.hex")
	if err := os.WriteFile(keyPath, []byte(hex.EncodeToString(dummyKey())), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(keyPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadPrivateKeyFile(keyPath); err == nil || got != nil {
		t.Fatal("world-readable plaintext import accepted")
	}
	if err := os.Chmod(keyPath, 0o400); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadPrivateKeyFile(keyPath); err != nil {
		t.Fatal(err)
	} else {
		Wipe(got)
	}
	link := filepath.Join(dir, "key-link")
	if err := os.Symlink(keyPath, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPrivateKeyFile(link); err == nil {
		t.Fatal("leaf symlink accepted")
	}
	parentLink := filepath.Join(t.TempDir(), "linked-dir")
	if err := os.Symlink(dir, parentLink); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPrivateKeyFile(filepath.Join(parentLink, "original.hex")); err == nil {
		t.Fatal("parent symlink accepted")
	}
	if err := encryptFile(filepath.Join(parentLink, "new.json"), dummyKey(), []byte("test password"), 1<<12, 1); err == nil {
		t.Fatal("output beneath a parent symlink accepted")
	}
	fifo := filepath.Join(dir, "fifo")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPrivateKeyFile(fifo); err == nil {
		t.Fatal("nonregular FIFO accepted")
	}
}

func TestLinuxEncryptedOutputPermission(t *testing.T) {
	path, _, password := fixture(t)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("new keystore mode %o, want 600", info.Mode().Perm())
	}
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadEncryptedFile(path, password); err == nil || got != nil {
		t.Fatal("group/world writable keystore accepted")
	}
}
