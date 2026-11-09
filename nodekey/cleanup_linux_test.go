//go:build linux

package nodekey

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxRemoveVerifiedImportSource(t *testing.T) {
	keystore, _, password := fixture(t)
	ciphertext, err := os.ReadFile(keystore)
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte(hex.EncodeToString(dummyKey()) + "\n")
	otherKey := dummyKey()
	otherKey[31]++
	otherPlain := []byte(hex.EncodeToString(otherKey) + "\n")
	for _, tc := range []struct {
		name  string
		alter func(t *testing.T, source, target string) []byte
	}{
		{name: "remove"},
		{name: "source replaced", alter: func(t *testing.T, source, _ string) []byte {
			if err := os.Rename(source, source+".original"); err != nil {
				t.Fatal(err)
			}
			writeCleanupTestFile(t, source, otherPlain)
			return otherPlain
		}},
		{name: "source overwritten", alter: func(t *testing.T, source, _ string) []byte {
			writeCleanupTestFile(t, source, otherPlain)
			return otherPlain
		}},
		{name: "ciphertext replaced", alter: func(t *testing.T, _, target string) []byte {
			if err := os.Rename(target, target+".original"); err != nil {
				t.Fatal(err)
			}
			writeCleanupTestFile(t, target, ciphertext)
			return plain
		}},
		{name: "ciphertext overwritten", alter: func(t *testing.T, _, target string) []byte {
			modified := append(bytes.Clone(ciphertext), '\n')
			writeCleanupTestFile(t, target, modified)
			return plain
		}},
		{name: "source directory unsafe", alter: func(t *testing.T, source, _ string) []byte {
			dir := filepath.Dir(source)
			if err := os.Chmod(dir, 0o777); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
			return plain
		}},
		{name: "ciphertext directory unsafe", alter: func(t *testing.T, _, target string) []byte {
			dir := filepath.Dir(target)
			if err := os.Chmod(dir, 0o777); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
			return plain
		}},
		{name: "canceled"},
		{name: "different keys"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sourcePath := filepath.Join(t.TempDir(), "source.hex")
			targetPath := filepath.Join(t.TempDir(), "identity.json")
			wantSource := plain
			if tc.name == "different keys" {
				wantSource = otherPlain
			}
			writeCleanupTestFile(t, sourcePath, wantSource)
			writeCleanupTestFile(t, targetPath, ciphertext)
			sourceKey, source, err := ReadPrivateKeyFileForCleanup(sourcePath)
			if err != nil {
				t.Fatal(err)
			}
			defer source.Close()
			Wipe(sourceKey)
			key, proof, err := ReadEncryptedFileForCleanup(targetPath, password)
			if err != nil {
				t.Fatal(err)
			}
			defer proof.Close()
			if !bytes.Equal(key, dummyKey()) {
				t.Fatal("verified keystore did not return the original key")
			}
			Wipe(key)
			if tc.alter != nil {
				wantSource = tc.alter(t, sourcePath, targetPath)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.name == "canceled" {
				cancel()
			}
			removed, err := RemoveVerifiedImportSource(ctx, source, proof)
			if tc.name == "remove" {
				if err != nil || !removed {
					t.Fatalf("cleanup did not remove the verified source: %v", err)
				}
				if _, err := os.Lstat(sourcePath); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("source still present after cleanup: %v", err)
				}
				if got, err := os.ReadFile(targetPath); err != nil || !bytes.Equal(got, ciphertext) {
					t.Fatalf("cleanup changed the verified keystore: %v", err)
				}
				return
			}
			if removed || err == nil {
				t.Fatal("cleanup accepted changed or canceled state")
			}
			if got, err := os.ReadFile(sourcePath); err != nil || !bytes.Equal(got, wantSource) {
				t.Fatalf("failed cleanup did not preserve the source entry: %v", err)
			}
		})
	}
}

func TestLinuxCleanupSourceRequiresPrivateFileAndTrustedPath(t *testing.T) {
	for _, testCase := range []string{"readable by others", "hardlink", "symlink", "writable parent"} {
		t.Run(testCase, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "source.hex")
			writeCleanupTestFile(t, path, []byte(hex.EncodeToString(dummyKey())))
			var err error
			switch testCase {
			case "readable by others":
				err = os.Chmod(path, 0o644)
			case "hardlink":
				err = os.Link(path, filepath.Join(dir, "copy.hex"))
			case "symlink":
				link := filepath.Join(dir, "link.hex")
				err = os.Symlink(path, link)
				path = link
			case "writable parent":
				err = os.Chmod(dir, 0o777)
				t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
			}
			if err != nil {
				t.Fatal(err)
			}
			key, proof, err := ReadPrivateKeyFileForCleanup(path)
			defer proof.Close()
			Wipe(key)
			if err == nil || key != nil || proof != nil {
				t.Fatal("unsafe source was accepted for deletion")
			}
		})
	}
}

func writeCleanupTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
