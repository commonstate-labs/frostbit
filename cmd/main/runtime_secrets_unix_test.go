//go:build !windows

package main

import (
	"bytes"
	"encoding/hex"
	"golang.org/x/sys/unix"
	"os"
	"strings"
	"testing"
)

func TestLoadRuntimeSecretsRequiresPrivateKeyFD(t *testing.T) {
	opts := runtimeOptions{KeystorePasswordFD: -1, PrivateKeyFD: -1, ArbitragePrivateKeyFD: -1, FrostWrapKeyFD: -1}
	if err := loadRuntimeSecrets(&opts); err == nil || !strings.Contains(err.Error(), "--private-key-fd or --keystore is required") {
		t.Fatalf("missing private-key FD error = %v", err)
	}
}

func TestLoadRuntimeSecretsRejectsWindowsLocalSecretDir(t *testing.T) {

	opts := runtimeOptions{KeystorePasswordFD: -1,
		PrivateKeyFD:               -1,
		ArbitragePrivateKeyFD:      -1,
		FrostWrapKeyFD:             -1,
		WindowsLocalSecretDir:      ".local/debug-node/secrets",
		AllowWindowsLocalSecretDir: true,
	}
	if err := loadRuntimeSecrets(&opts); err == nil || !strings.Contains(err.Error(), "supported on Windows only") {
		t.Fatalf("Windows local secret source error = %v", err)
	}
}

func TestLoadRuntimeSecretsFromInheritedFDs(t *testing.T) {
	privateReader, privateWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	wrapReader, wrapWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	arbitrageReader, arbitrageWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	wrapKey := bytes.Repeat([]byte{0x42}, frostWrapKeyBytes)
	if _, err := privateWriter.WriteString(testPrivateKeyForIndex(1) + "\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := arbitrageWriter.WriteString(testPrivateKeyForIndex(2) + "\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := wrapWriter.WriteString(hex.EncodeToString(wrapKey) + "\n"); err != nil {
		t.Fatal(err)
	}
	_ = privateWriter.Close()
	_ = arbitrageWriter.Close()
	_ = wrapWriter.Close()

	                                                                     
	                                                                
	duplicate := func(file *os.File) int {
		t.Helper()
		fd, err := unix.Dup(int(file.Fd()))
		file.Close()
		if err != nil {
			t.Fatal(err)
		}
		return fd
	}
	privateFD, arbitrageFD, wrapFD := duplicate(privateReader), duplicate(arbitrageReader), duplicate(wrapReader)

	opts := runtimeOptions{KeystorePasswordFD: -1,
		PrivateKeyFD:          privateFD,
		ArbitragePrivateKeyFD: arbitrageFD,
		FrostWrapKeyFD:        wrapFD,
	}
	if err := loadRuntimeSecrets(&opts); err != nil {
		t.Fatalf("load runtime secrets: %v", err)
	}
	if opts.KeyManager == nil || opts.KeyManager.GetAddress() != testRuntimeKey(t, testPrivateKeyForIndex(1)).GetAddress() {
		t.Fatal("wrong miner identity")
	}
	if opts.ArbitrageKeyManager == nil || opts.ArbitrageKeyManager.GetAddress() != testRuntimeKey(t, testPrivateKeyForIndex(2)).GetAddress() {
		t.Fatal("wrong arbitrage identity")
	}
	if !bytes.Equal(opts.FrostWrapKey, wrapKey) {
		t.Fatal("unexpected wrap key")
	}
	wipeBytes(opts.FrostWrapKey)
}
