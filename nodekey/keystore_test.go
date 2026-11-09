package nodekey

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gethkeystore "github.com/ethereum/go-ethereum/accounts/keystore"
)

func dummyKey() []byte {
	key := make([]byte, 32)
	key[31] = 7
	return key
}

func fixture(t *testing.T) (string, []byte, []byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "identity.json")
	key, password := dummyKey(), []byte(" leading and trailing spaces ")
	if err := EncryptFile(path, key, password); err != nil {
		t.Fatal(err)
	}
	return path, key, password
}

func TestEncryptFileStandardRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.json")
	key, password := dummyKey(), []byte(" test-only standard scrypt passphrase ")
	keyBefore, passwordBefore := bytes.Clone(key), bytes.Clone(password)
	if err := EncryptFile(path, key, password); err != nil {
		t.Fatal(err)
	}
	metadata, err := InspectFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Version != 3 || metadata.ScryptN != gethkeystore.StandardScryptN || metadata.ScryptR != 8 || metadata.ScryptP != gethkeystore.StandardScryptP {
		t.Fatalf("unexpected encryption parameters: %+v", metadata)
	}
	got, err := ReadEncryptedFile(path, password)
	if err != nil {
		t.Fatal(err)
	}
	defer Wipe(got)
	if len(got) != 32 || !bytes.Equal(got, key) {
		t.Fatal("decryption did not preserve exact private scalar")
	}
	if !bytes.Equal(key, keyBefore) || !bytes.Equal(password, passwordBefore) {
		t.Fatal("encryption/decryption modified caller-owned input")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, password) || bytes.Contains(raw, []byte(hex.EncodeToString(key))) {
		t.Fatal("encrypted file contains plaintext secret")
	}
}

func TestNewPasswordMinimumAndReadCompatibility(t *testing.T) {
	for _, password := range [][]byte{nil, []byte("x"), bytes.Repeat([]byte("x"), MinNewPassphraseBytes-1)} {
		path := filepath.Join(t.TempDir(), "weak.json")
		if err := EncryptFile(path, dummyKey(), password); err == nil {
			t.Fatal("short new passphrase accepted")
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("invalid new passphrase created an output")
		}
	}
	                                                                                                     
	path := filepath.Join(t.TempDir(), "legacy-standard.json")
	if err := encryptFile(path, dummyKey(), []byte("x"), gethkeystore.StandardScryptN, gethkeystore.StandardScryptP); err != nil {
		t.Fatal(err)
	}
	got, err := ReadEncryptedFile(path, []byte("x"))
	if err != nil || !bytes.Equal(got, dummyKey()) {
		t.Fatalf("historical standard file cannot be unlocked: %v", err)
	}
	Wipe(got)
}

func TestEncryptFileNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "original.hex")
	before := []byte(hex.EncodeToString(dummyKey()) + "\n")
	if err := os.WriteFile(source, before, 0o600); err != nil {
		t.Fatal(err)
	}
	key, err := ReadPrivateKeyFile(source)
	if err != nil {
		t.Fatal(err)
	}
	defer Wipe(key)
	if err := encryptFile(source, key, []byte("test password"), 1<<12, 1); err == nil {
		t.Fatal("import overwrote the plaintext source")
	}
	after, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("original source changed: %v", err)
	}
	out := filepath.Join(dir, "new.json")
	if err := encryptFile(out, key, []byte("test password"), 1<<12, 1); err != nil {
		t.Fatal(err)
	}
	before, err = os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := encryptFile(out, key, []byte("another password"), 1<<12, 1); err == nil {
		t.Fatal("existing encrypted output was overwritten")
	}
	after, err = os.ReadFile(out)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("existing encrypted output changed: %v", err)
	}
}

func TestDecryptRejectsWrongPasswordTamperingAndAddress(t *testing.T) {
	path, _, password := fixture(t)
	if got, err := ReadEncryptedFile(path, []byte("wrong password")); err == nil || got != nil {
		t.Fatal("wrong password accepted")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var original encryptedFile
	if err := json.Unmarshal(raw, &original); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		edit func(*encryptedFile)
	}{
		{"MAC", func(f *encryptedFile) { f.Crypto.MAC = strings.Repeat("00", 32) }},
		{"ciphertext", func(f *encryptedFile) { f.Crypto.CipherText = strings.Repeat("00", 32) }},
		{"address", func(f *encryptedFile) { f.Address = strings.Repeat("00", 20) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := original
			tc.edit(&f)
			mutated, err := json.Marshal(f)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, mutated, 0o600); err != nil {
				t.Fatal(err)
			}
			if got, err := ReadEncryptedFile(path, password); err == nil || got != nil {
				t.Fatal("tampered keystore accepted")
			}
		})
	}
}

func TestKeystorePreflightBounds(t *testing.T) {
	path, _, password := fixture(t)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var original encryptedFile
	if err := json.Unmarshal(raw, &original); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		edit func(*encryptedFile)
	}{
		{"huge_N", func(f *encryptedFile) { f.Crypto.KDFParams.N = 1 << 30 }},
		{"weak_N", func(f *encryptedFile) { f.Crypto.KDFParams.N = 1 << 12 }},
		{"non_power_N", func(f *encryptedFile) { f.Crypto.KDFParams.N = 4097 }},
		{"negative_N", func(f *encryptedFile) { f.Crypto.KDFParams.N = -1 }},
		{"huge_r", func(f *encryptedFile) { f.Crypto.KDFParams.R = 1 << 28 }},
		{"huge_p", func(f *encryptedFile) { f.Crypto.KDFParams.P = 1 << 28 }},
		{"excess_work", func(f *encryptedFile) { f.Crypto.KDFParams.N = gethkeystore.StandardScryptN; f.Crypto.KDFParams.P = 2 }},
		{"huge_dklen", func(f *encryptedFile) { f.Crypto.KDFParams.DKLen = 1 << 30 }},
		{"unknown_KDF", func(f *encryptedFile) { f.Crypto.KDF = "pbkdf2" }},
		{"unknown_cipher", func(f *encryptedFile) { f.Crypto.Cipher = "aes-256-gcm" }},
		{"short_IV", func(f *encryptedFile) { f.Crypto.CipherParams.IV = "00" }},
		{"invalid_salt", func(f *encryptedFile) { f.Crypto.KDFParams.Salt = strings.Repeat("zz", 32) }},
		{"wrong_version", func(f *encryptedFile) { f.Version = 1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := original
			tc.edit(&f)
			mutated, err := json.Marshal(f)
			if err != nil {
				t.Fatal(err)
			}
			                                                                   
			if _, _, err := inspectJSON(mutated); err == nil {
				t.Fatal("unsafe parameters passed preflight")
			}
			if err := os.WriteFile(path, mutated, 0o600); err != nil {
				t.Fatal(err)
			}
			if got, err := ReadEncryptedFile(path, password); err == nil || got != nil {
				t.Fatal("unsafe keystore accepted")
			}
		})
	}
	for _, invalid := range [][]byte{nil, raw[:len(raw)/2], append(bytes.Clone(raw), raw...), bytes.Repeat([]byte("x"), MaxKeystoreBytes+1), []byte("null"), []byte("[]")} {
		if _, _, err := inspectJSON(invalid); err == nil {
			t.Fatal("malformed or oversized JSON passed preflight")
		}
	}
}

func TestPrivateKeyImportBoundaries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "original.hex")
	valid := hex.EncodeToString(dummyKey())
	for _, input := range []string{valid, "\t0x" + valid + "\r\n"} {
		if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := ReadPrivateKeyFile(path)
		if err != nil || !bytes.Equal(got, dummyKey()) {
			t.Fatalf("valid import rejected: %v", err)
		}
		Wipe(got)
	}
	for _, input := range []string{"", valid + " " + valid, strings.Repeat("0", 64), strings.Repeat("f", 64), strings.Repeat("z", 64), valid[:63], strings.Repeat("1", MaxPrivateKeyBytes+1)} {
		if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
			t.Fatal(err)
		}
		if got, err := ReadPrivateKeyFile(path); err == nil || got != nil {
			t.Fatal("invalid plaintext key accepted")
		}
	}
	for _, key := range [][]byte{nil, make([]byte, 31), make([]byte, 32), bytes.Repeat([]byte{0xff}, 32), make([]byte, 33)} {
		if err := encryptFile(filepath.Join(t.TempDir(), "bad.json"), key, []byte("test password"), 1<<12, 1); err == nil {
			t.Fatal("invalid scalar accepted for encryption")
		}
	}
}
