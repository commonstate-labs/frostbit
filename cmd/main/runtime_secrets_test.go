package main

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
)

func TestReadBoundedSecret(t *testing.T) {
	got, err := readBoundedSecret(strings.NewReader("secret\n"), 8)
	if err != nil {
		t.Fatalf("read bounded secret: %v", err)
	}
	if string(got) != "secret\n" {
		t.Fatalf("unexpected secret payload %q", got)
	}

	if _, err := readBoundedSecret(strings.NewReader(""), 8); err == nil {
		t.Fatal("expected empty input to fail")
	}
	if _, err := readBoundedSecret(strings.NewReader("123456789"), 8); err == nil {
		t.Fatal("expected oversized input to fail")
	}
}

func TestDecodePrivateKeyToken(t *testing.T) {
	for _, raw := range []string{"01", "0x01\n", " 0001 "} {
		got, err := decodePrivateKeyToken([]byte(raw))
		if err != nil || len(got) != 32 || got[31] != 1 {
			t.Fatalf("decode key failed: %v", err)
		}
		wipeBytes(got)
	}
	for _, raw := range []string{"", "01 02", "z1", "1", strings.Repeat("01", 33)} {
		if _, err := decodePrivateKeyToken([]byte(raw)); err == nil {
			t.Fatal("invalid hex accepted")
		}
	}
}

func TestDecodeFrostWrapKey(t *testing.T) {
	want := bytes.Repeat([]byte{0x5a}, frostWrapKeyBytes)
	hexKey := hex.EncodeToString(want)
	for _, raw := range [][]byte{[]byte(hexKey), []byte("0x" + hexKey + "\n")} {
		got, err := decodeFrostWrapKey(raw)
		if err != nil {
			t.Fatalf("decode wrap key: %v", err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("decoded key mismatch")
		}
		wipeBytes(got)
	}

	for _, raw := range [][]byte{
		[]byte("00"),
		[]byte(strings.Repeat("z", frostWrapKeyBytes*2)),
		[]byte(hexKey + " " + hexKey),
	} {
		if _, err := decodeFrostWrapKey(raw); err == nil {
			t.Fatalf("expected invalid wrap key %q to fail", raw)
		}
	}
}

func TestWipeBytes(t *testing.T) {
	secret := []byte("sensitive")
	wipeBytes(secret)
	if !bytes.Equal(secret, make([]byte, len(secret))) {
		t.Fatal("secret buffer was not cleared")
	}
}

func TestLoadRuntimeSecretsRejectsPreloadedPrivateKey(t *testing.T) {
	opts := runtimeOptions{KeystorePasswordFD: -1, KeyManager: testRuntimeKey(t, "01"), PrivateKeyFD: 3, ArbitragePrivateKeyFD: -1, FrostWrapKeyFD: -1}
	if err := loadRuntimeSecrets(&opts); err == nil || !strings.Contains(err.Error(), "must only be loaded") {
		t.Fatalf("preloaded private key error = %v", err)
	}
}

func TestLoadRuntimeSecretsRejectsPreloadedArbitragePrivateKey(t *testing.T) {
	opts := runtimeOptions{KeystorePasswordFD: -1,
		PrivateKeyFD:          3,
		ArbitrageKeyManager:   testRuntimeKey(t, "01"),
		ArbitragePrivateKeyFD: 4,
		FrostWrapKeyFD:        -1,
	}
	if err := loadRuntimeSecrets(&opts); err == nil || !strings.Contains(err.Error(), "arbitrage private key must only be loaded") {
		t.Fatalf("preloaded arbitrage private key error = %v", err)
	}
}

func TestValidateDistinctSecretFDsRejectsArbitrageReuse(t *testing.T) {
	err := validateDistinctSecretFDs(
		runtimeSecretFD{"node", 3},
		runtimeSecretFD{"arbitrage", 4},
		runtimeSecretFD{"frost", 4},
	)
	if err == nil || !strings.Contains(err.Error(), "different inherited descriptors") {
		t.Fatalf("descriptor reuse error = %v", err)
	}
}

func TestValidateArbitrageRuntimeSecret(t *testing.T) {
	if err := validateArbitrageRuntimeSecret(true, nil, ""); err == nil || !strings.Contains(err.Error(), "no private key") {
		t.Fatalf("enabled without key error = %v", err)
	}
	if err := validateArbitrageRuntimeSecret(false, testRuntimeKey(t, "01"), ""); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("disabled with key error = %v", err)
	}
	if err := validateArbitrageRuntimeSecret(false, nil, "fbc1qexpected"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("disabled with expected address error = %v", err)
	}
	if err := validateArbitrageRuntimeSecret(true, testRuntimeKey(t, "01"), ""); err != nil {
		t.Fatalf("enabled with key rejected: %v", err)
	}
}
