package main

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func passwordFD(t *testing.T, password string) (string, func()) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte(password + "\n")); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	return strconv.FormatUint(uint64(reader.Fd()), 10), func() { reader.Close() }
}

func TestCLIImportInspectAndAddress(t *testing.T) {
	dir := t.TempDir()
	source, out := filepath.Join(dir, "original.hex"), filepath.Join(dir, "identity.json")
	key := make([]byte, 32)
	key[31] = 7
	before := []byte(hex.EncodeToString(key) + "\r\n")
	if err := os.WriteFile(source, before, 0o600); err != nil {
		t.Fatal(err)
	}
	const password = " test CLI passphrase 中文 "
	fd, closeFD := passwordFD(t, password)
	var stdout, stderr bytes.Buffer
	err := run([]string{"import", "--private-key-file", source, "--out", out, "--password-fd", fd}, &stdout, &stderr)
	closeFD()
	if err != nil {
		t.Fatal(err)
	}
	address, err := dexAddress(key)
	if err != nil || !strings.HasPrefix(address, "fbc1q") {
		t.Fatalf("DEX address derivation failed: %v", err)
	}
	if !strings.Contains(stdout.String(), address) || strings.Contains(stdout.String(), password) || bytes.Contains(stdout.Bytes(), bytes.TrimSpace(before)) {
		t.Fatal("import output is missing public identity or exposes a secret")
	}
	after, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("import modified plaintext source: %v", err)
	}
	stdout.Reset()
	if err := run([]string{"inspect", "--keystore", out}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "N=262144 r=8 p=1") || !strings.Contains(stdout.String(), "not a DEX address") {
		t.Fatalf("inspect failed to distinguish metadata: %s", &stdout)
	}
	stdout.Reset()
	fd, closeFD = passwordFD(t, password)
	err = run([]string{"address", "--keystore", out, "--password-fd", fd}, &stdout, &stderr)
	closeFD()
	if err != nil || strings.TrimSpace(stdout.String()) != address {
		t.Fatalf("verified address mismatch: %v", err)
	}
	stdout.Reset()
	fd, closeFD = passwordFD(t, "wrong passphrase")
	err = run([]string{"address", "--keystore", out, "--password-fd", fd}, &stdout, &stderr)
	closeFD()
	if err == nil || stdout.Len() != 0 {
		t.Fatal("wrong password produced a verified address")
	}
}

func TestCLIRejectsPasswordValuesAndInvalidArguments(t *testing.T) {
	for _, args := range [][]string{
		nil, {"export"}, {"import"}, {"inspect"}, {"address"},
		{"import", "--private-key-file", "unused", "--out", "unused", "--password", "secret"},
		{"address", "--keystore", "unused", "--password-env", "PASSWORD"},
		{"address", "--keystore", "unused", "--password-fd", "0"},
		{"import", "--private-key-file", "unused", "--out", "unused", "--password-fd", "2"},
		{"inspect", "--keystore", "unused", "extra"},
	} {
		var stdout, stderr bytes.Buffer
		if err := run(args, &stdout, &stderr); err == nil {
			t.Fatalf("invalid arguments accepted: %v", args)
		}
		if stdout.Len() != 0 {
			t.Fatal("invalid command produced successful output")
		}
	}
}
