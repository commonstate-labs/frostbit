//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestLoadRuntimeSecretsFromWindowsLocalSecretDir(t *testing.T) {
	dir, address := createWindowsSecretFixture(t, testPrivateKeyForIndex(7))
	opts := runtimeOptions{KeystorePasswordFD: -1,
		PrivateKeyFD:               -1,
		ArbitragePrivateKeyFD:      -1,
		FrostWrapKeyFD:             -1,
		WindowsLocalSecretDir:      dir,
		AllowWindowsLocalSecretDir: true,
		ExpectedAddress:            address,
	}
	if err := loadRuntimeSecrets(&opts); err != nil {
		t.Fatalf("load Windows local secret: %v", err)
	}
	identity, err := deriveLocalPeerIdentity(opts.KeyManager)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Address != address {
		t.Fatalf("loaded address = %s, want %s", identity.Address, address)
	}
}

func TestLoadRuntimeSecretsFromIndependentWindowsArbitrageSecretDir(t *testing.T) {
	nodeDir, _ := createWindowsSecretFixture(t, testPrivateKeyForIndex(17))
	arbitrageKey := testPrivateKeyForIndex(18)
	arbitrageDir, arbitrageAddress := createWindowsSecretFixture(t, arbitrageKey)
	opts := runtimeOptions{KeystorePasswordFD: -1,
		PrivateKeyFD:               -1,
		ArbitragePrivateKeyFD:      -1,
		FrostWrapKeyFD:             -1,
		WindowsLocalSecretDir:      nodeDir,
		WindowsArbitrageSecretDir:  arbitrageDir,
		AllowWindowsLocalSecretDir: true,
		ExpectedArbitrageAddress:   arbitrageAddress,
	}
	if err := loadRuntimeSecrets(&opts); err != nil {
		t.Fatalf("load independent Windows arbitrage secret: %v", err)
	}
	identity, err := deriveLocalPeerIdentity(opts.ArbitrageKeyManager)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Address != arbitrageAddress {
		t.Fatalf("loaded arbitrage address = %s, want %s", identity.Address, arbitrageAddress)
	}
}

func TestLoadRuntimeSecretsWindowsRequiresExplicitOptIn(t *testing.T) {
	dir, _ := createWindowsSecretFixture(t, testPrivateKeyForIndex(8))
	opts := runtimeOptions{KeystorePasswordFD: -1, PrivateKeyFD: -1, ArbitragePrivateKeyFD: -1, FrostWrapKeyFD: -1, WindowsLocalSecretDir: dir}
	err := loadRuntimeSecrets(&opts)
	if err == nil || !strings.Contains(err.Error(), "explicit --allow-windows-local-secret-dir") {
		t.Fatalf("opt-in error = %v", err)
	}
}

func TestLoadRuntimeSecretsWindowsRejectsOutsideLocal(t *testing.T) {
	dir := t.TempDir()
	opts := runtimeOptions{KeystorePasswordFD: -1,
		PrivateKeyFD:               -1,
		ArbitragePrivateKeyFD:      -1,
		FrostWrapKeyFD:             -1,
		WindowsLocalSecretDir:      dir,
		AllowWindowsLocalSecretDir: true,
	}
	err := loadRuntimeSecrets(&opts)
	if err == nil || !strings.Contains(err.Error(), "strict child") {
		t.Fatalf("outside .local error = %v", err)
	}
}

func TestLoadRuntimeSecretsWindowsRejectsExtraEntry(t *testing.T) {
	dir, _ := createWindowsSecretFixture(t, testPrivateKeyForIndex(9))
	extra := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(extra, []byte("not secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(extra)
	opts := runtimeOptions{KeystorePasswordFD: -1,
		PrivateKeyFD:               -1,
		ArbitragePrivateKeyFD:      -1,
		FrostWrapKeyFD:             -1,
		WindowsLocalSecretDir:      dir,
		AllowWindowsLocalSecretDir: true,
	}
	err := loadRuntimeSecrets(&opts)
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("extra entry error = %v", err)
	}
}

func TestLoadRuntimeSecretsWindowsBindsFilenameAndExpectedAddress(t *testing.T) {
	dir, address := createWindowsSecretFixture(t, testPrivateKeyForIndex(10))
	wrongAddress := mustTestAddress(t, testPrivateKeyForIndex(11))
	oldPath := filepath.Join(dir, address+".key")
	wrongPath := filepath.Join(dir, wrongAddress+".key")
	if err := os.Rename(oldPath, wrongPath); err != nil {
		t.Fatal(err)
	}
	secureWindowsTestACL(t, wrongPath, false, false)
	opts := runtimeOptions{KeystorePasswordFD: -1,
		PrivateKeyFD:               -1,
		ArbitragePrivateKeyFD:      -1,
		FrostWrapKeyFD:             -1,
		WindowsLocalSecretDir:      dir,
		AllowWindowsLocalSecretDir: true,
	}
	err := loadRuntimeSecrets(&opts)
	if err == nil || !strings.Contains(err.Error(), "not filename address") {
		t.Fatalf("filename binding error = %v", err)
	}

	if err := os.Rename(wrongPath, oldPath); err != nil {
		t.Fatal(err)
	}
	secureWindowsTestACL(t, oldPath, false, false)
	opts.ExpectedAddress = wrongAddress
	err = loadRuntimeSecrets(&opts)
	if err == nil || !strings.Contains(err.Error(), "not --expected-address") {
		t.Fatalf("expected-address binding error = %v", err)
	}
}

func TestLoadRuntimeSecretsWindowsRejectsWideDACL(t *testing.T) {
	dir, address := createWindowsSecretFixture(t, testPrivateKeyForIndex(12))
	keyPath := filepath.Join(dir, address+".key")
	secureWindowsTestACL(t, keyPath, false, true)
	opts := runtimeOptions{KeystorePasswordFD: -1,
		PrivateKeyFD:               -1,
		ArbitragePrivateKeyFD:      -1,
		FrostWrapKeyFD:             -1,
		WindowsLocalSecretDir:      dir,
		AllowWindowsLocalSecretDir: true,
	}
	err := loadRuntimeSecrets(&opts)
	if err == nil || !strings.Contains(err.Error(), "untrusted SID") {
		t.Fatalf("wide DACL error = %v", err)
	}
}

func TestLoadRuntimeSecretsWindowsRejectsReparsePath(t *testing.T) {
	root, err := findWindowsRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	base, err := os.MkdirTemp(filepath.Join(root, ".local"), "secret-reparse-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(base)
	realDir := filepath.Join(base, "real")
	if err := os.Mkdir(realDir, 0o700); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(base, "link")
	if err := os.Symlink(realDir, linkDir); err != nil {
		output, junctionErr := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", linkDir, realDir).CombinedOutput()
		if junctionErr != nil {
			t.Skipf("Windows symlink and junction creation unavailable: symlink=%v junction=%v output=%s", err, junctionErr, strings.TrimSpace(string(output)))
		}
	}
	opts := runtimeOptions{KeystorePasswordFD: -1,
		PrivateKeyFD:               -1,
		ArbitragePrivateKeyFD:      -1,
		FrostWrapKeyFD:             -1,
		WindowsLocalSecretDir:      linkDir,
		AllowWindowsLocalSecretDir: true,
	}
	err = loadRuntimeSecrets(&opts)
	if err == nil || !strings.Contains(err.Error(), "reparse point") {
		t.Fatalf("reparse error = %v", err)
	}
}

func createWindowsSecretFixture(t *testing.T, privateKey string) (string, string) {
	t.Helper()
	root, err := findWindowsRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	localRoot := filepath.Join(root, ".local")
	if err := os.MkdirAll(localRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(localRoot, "secret-runtime-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	address := mustTestAddress(t, privateKey)
	keyPath := filepath.Join(dir, address+".key")
	if err := os.WriteFile(keyPath, []byte(privateKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	secureWindowsTestACL(t, dir, true, false)
	secureWindowsTestACL(t, keyPath, false, false)
	return dir, address
}

func mustTestAddress(t *testing.T, privateKey string) string {
	t.Helper()
	_, identity, err := deriveTestPeerIdentity(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return identity.Address
}

func secureWindowsTestACL(t *testing.T, path string, directory, includeWorld bool) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	inheritance := uint32(windows.NO_INHERITANCE)
	if directory {
		inheritance = windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT
	}
	entries := []windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.SET_ACCESS,
		Inheritance:       inheritance,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(user.User.Sid),
		},
	}}
	if includeWorld {
		world, err := windows.CreateWellKnownSid(windows.WinWorldSid)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, windows.EXPLICIT_ACCESS{
			AccessPermissions: windows.GENERIC_READ,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.NO_INHERITANCE,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_GROUP,
				TrusteeValue: windows.TrusteeValueFromSID(world),
			},
		})
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		acl,
		nil,
	); err != nil {
		t.Fatal(err)
	}
}
