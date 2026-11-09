//go:build linux

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"dex/nodekey"
	"golang.org/x/sys/unix"
)

func TestRuntimeKeystoreImportPasswordFDIsConsumedOnce(t *testing.T) {
	opts, _ := importTestOptions(t)
	password := []byte("fixture-one-use-fd-password-123")
	defer wipeBytes(password)
	fd := passphraseTestFD(t, password)
	opts.KeystorePasswordFD = fd
	ops := defaultRuntimeKeystoreOperations()
	reads := 0
	originalRead := ops.readPassphraseFD
	ops.readPassphraseFD = func(got int) ([]byte, error) { reads++; return originalRead(got) }
	ops.readPassphraseTTY = func(string) ([]byte, error) { t.Fatal("explicit FD unexpectedly used terminal"); return nil, nil }
	identity, err := loadRuntimeKeystoreIdentity(&opts, ops)
	if err != nil || identity == nil {
		t.Fatalf("FD import: %v", err)
	}
	if reads != 1 {
		t.Fatalf("password FD read %d times", reads)
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err != unix.EBADF {
		t.Fatal("password descriptor remained open")
	}
	if identity.GetAddress() != opts.ExpectedAddress {
		t.Fatal("FD import changed identity")
	}
}

func TestRuntimeKeystoreImportWithoutTerminalFailsBeforeCreate(t *testing.T) {
	if os.Getenv("DEX_TEST_KEYSTORE_IMPORT_NO_TTY") == "1" {
		opts, _ := importTestOptions(t)
		err := loadRuntimeSecrets(&opts)
		if err == nil || !strings.Contains(err.Error(), "terminal") {
			t.Fatalf("noninteractive import: %v", err)
		}
		if opts.KeyManager != nil {
			t.Fatal("failed import published a runtime identity")
		}
		if _, err := os.Stat(opts.KeystorePath); !os.IsNotExist(err) {
			t.Fatal("noninteractive import created ciphertext")
		}
		if _, err := os.Stat(opts.KeystoreImportFrom); err != nil {
			t.Fatal("failed import removed source")
		}
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestRuntimeKeystoreImportWithoutTerminalFailsBeforeCreate$")
	cmd.Env = append(os.Environ(), "DEX_TEST_KEYSTORE_IMPORT_NO_TTY=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("noninteractive import child: %v %s", err, out)
	}
}

func TestRuntimeKeystoreCleanupPreparationAndRetry(t *testing.T) {
	opts, original := importTestOptions(t)
	opts.DeleteKeystoreImportSource = true
	const password = "fixture-opt-in-cleanup-password"
	ops, counts := importTestOperations(t, password, password)
	if identity, err := loadRuntimeKeystoreIdentity(&opts, ops); err != nil || identity == nil {
		t.Fatalf("prepare cleanup on first import: %v", err)
	}
	if opts.keystoreImportCleanup == nil {
		t.Fatal("verified import did not prepare cleanup")
	}
	t.Cleanup(opts.keystoreImportCleanup.close)
	if counts.source != 1 || counts.encrypt != 1 || counts.decrypt != 1 {
		t.Fatalf("first import operations = %+v", counts)
	}
	assertImportBuffersCleared(t, counts)
	                                                                             
	                                                                   
	opts.keystoreImportCleanup.close()
	opts.keystoreImportCleanup = nil
	for _, tc := range []struct {
		name         string
		password     string
		source       string
		wantCleanup  bool
		wantSourceIO int
		wantErr      bool
	}{
		{"matching source", password, opts.KeystoreImportFrom, true, 1, false},
		{"missing source", password, opts.KeystoreImportFrom + ".missing", false, 1, false},
		{"wrong password", "wrong-fixture-password", opts.KeystoreImportFrom, false, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			retry := opts
			retry.KeystoreImportFrom = tc.source
			ops, counts := importTestOperations(t, tc.password)
			identity, err := loadRuntimeKeystoreIdentity(&retry, ops)
			defer retry.keystoreImportCleanup.close()
			if (err != nil) != tc.wantErr || (identity == nil) != tc.wantErr {
				t.Fatalf("unexpected retry unlock result: %v", err)
			}
			if (retry.keystoreImportCleanup != nil) != tc.wantCleanup {
				t.Fatal("unexpected retry cleanup state")
			}
			if counts.source != tc.wantSourceIO || counts.encrypt != 0 || counts.decrypt != 1 {
				t.Fatalf("retry operations = %+v", counts)
			}
			assertImportBuffersCleared(t, counts)
		})
	}
	got, err := os.ReadFile(opts.KeystoreImportFrom)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatal("preparing or discarding cleanup changed the source")
	}
}

func TestRuntimeKeystoreCleanupKeepsMismatchedSource(t *testing.T) {
	opts, _ := importTestOptions(t)
	opts.DeleteKeystoreImportSource = true
	const password = "fixture-mismatched-source-password"
	key := make([]byte, 32)
	key[31] = 1
	defer wipeBytes(key)
	opts.ExpectedAddress = testRuntimeKey(t, "01").GetAddress()
	if err := nodekey.EncryptFile(opts.KeystorePath, key, []byte(password)); err != nil {
		t.Fatal(err)
	}
	ops, counts := importTestOperations(t, password)
	identity, err := loadRuntimeKeystoreIdentity(&opts, ops)
	defer opts.keystoreImportCleanup.close()
	if err != nil || identity == nil || opts.keystoreImportCleanup != nil {
		t.Fatalf("valid keystore should unlock without arming a mismatched source: %v", err)
	}
	if counts.source != 1 || counts.encrypt != 0 || counts.decrypt != 1 {
		t.Fatalf("mismatched source operations = %+v", counts)
	}
	if _, err := os.Stat(opts.KeystoreImportFrom); err != nil {
		t.Fatal("mismatched import source disappeared")
	}
	assertImportBuffersCleared(t, counts)
}

func TestRuntimeKeystoreCleanupDiscardsReceiptsOnLaterSecretFailure(t *testing.T) {
	opts, _ := importTestOptions(t)
	opts.DeleteKeystoreImportSource = true
	password := []byte("fixture-later-secret-failure")
	defer wipeBytes(password)
	opts.KeystorePasswordFD = passphraseTestFD(t, password)
	opts.FrostWrapKeyFD = passphraseTestFD(t, []byte("invalid-wrap-key"))
	if err := loadRuntimeSecrets(&opts); err == nil || !strings.Contains(err.Error(), "FROST wrap key") {
		t.Fatalf("invalid wrap key did not stop startup: %v", err)
	}
	if opts.keystoreImportCleanup != nil || opts.KeyManager != nil {
		t.Fatal("failed startup retained an armed cleanup or published a runtime identity")
	}
	if _, err := os.Stat(opts.KeystoreImportFrom); err != nil {
		t.Fatal("later startup failure removed the import source")
	}
	if _, err := os.Stat(opts.KeystorePath); err != nil {
		t.Fatal("fixture did not reach successful keystore creation")
	}
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Logf("descriptor inspection unavailable: %v", err)
		return
	}
	dir := filepath.Dir(opts.KeystorePath)
	for _, entry := range entries {
		path, err := os.Readlink(filepath.Join("/proc/self/fd", entry.Name()))
		if err == nil && (path == dir || strings.HasPrefix(path, dir+string(os.PathSeparator))) {
			t.Fatalf("failed startup leaked an import descriptor for %s", path)
		}
	}
}
