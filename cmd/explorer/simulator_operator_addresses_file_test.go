package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dex/config"
	"dex/utils"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
)

func TestResolveSimulatorOperatorAddressesSelectsExactlyOneSource(t *testing.T) {
	address := simulatorOperatorFileTestAddress(t)
	if got, err := resolveSimulatorOperatorAddresses(address, ""); err != nil || got != address {
		t.Fatalf("raw addresses resolved to %q, %v", got, err)
	}

	path := writeSimulatorOperatorAddressesFile(t, "# local operator\n"+address+"\n")
	if _, err := resolveSimulatorOperatorAddresses(address, path); err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("combined address sources error = %v", err)
	}
	if got, err := resolveSimulatorOperatorAddresses("  ", path); err != nil || got != address {
		t.Fatalf("file addresses resolved to %q, %v", got, err)
	}
}

func TestValidateSimulatorOperatorServiceToken(t *testing.T) {
	address := simulatorOperatorFileTestAddress(t)
	if err := validateSimulatorOperatorServiceToken("", ""); err != nil {
		t.Fatalf("disabled controls rejected empty service token: %v", err)
	}
	if err := validateSimulatorOperatorServiceToken(address, ""); err == nil {
		t.Fatal("enabled controls accepted an empty service token")
	}
	if err := validateSimulatorOperatorServiceToken(address, strings.Repeat("x", config.ExplorerServiceTokenMinLen)); err != nil {
		t.Fatalf("enabled controls rejected a service token: %v", err)
	}
	if err := validateSimulatorOperatorServiceToken(address, "short"); err == nil {
		t.Fatal("enabled controls accepted a short service token")
	}
}

func TestLoadSimulatorOperatorAddressesFileAcceptsCommentsAndBlankLines(t *testing.T) {
	first := simulatorOperatorFileTestAddress(t)
	second := simulatorOperatorFileTestAddress(t)
	path := writeSimulatorOperatorAddressesFile(t, "  # generated for local testing\r\n\r\n  "+first+"  \r\n"+second+"\n")

	got, err := loadSimulatorOperatorAddressesFile(path)
	if err != nil {
		t.Fatalf("load operator addresses: %v", err)
	}
	if want := first + "," + second; got != want {
		t.Fatalf("operator addresses = %q, want %q", got, want)
	}
	allowed, err := parseSimulatorOperatorAddresses(got)
	if err != nil || len(allowed) != 2 {
		t.Fatalf("resolved addresses do not satisfy auth parser: count=%d err=%v", len(allowed), err)
	}
}

func TestLoadSimulatorOperatorAddressesFileRejectsUnsafeOrInvalidFiles(t *testing.T) {
	address := simulatorOperatorFileTestAddress(t)
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "empty", content: "\n# comment only\n", want: "contains no addresses"},
		{name: "invalid", content: "not-an-address\n", want: "line 1 is not a canonical"},
		{name: "duplicate", content: address + "\n" + address + "\n", want: "line 2 repeats address"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := writeSimulatorOperatorAddressesFile(t, test.content)
			if _, err := loadSimulatorOperatorAddressesFile(path); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("load error = %v, want substring %q", err, test.want)
			}
		})
	}

	t.Run("missing", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "missing.txt")
		if _, err := loadSimulatorOperatorAddressesFile(path); err == nil || !strings.Contains(err.Error(), "read simulator operator addresses file") {
			t.Fatalf("missing file error = %v", err)
		}
	})

	t.Run("directory", func(t *testing.T) {
		if _, err := loadSimulatorOperatorAddressesFile(t.TempDir()); err == nil || !strings.Contains(err.Error(), "regular, non-symlink") {
			t.Fatalf("directory error = %v", err)
		}
	})

	t.Run("symlink", func(t *testing.T) {
		directory := t.TempDir()
		target := filepath.Join(directory, "target.txt")
		if err := os.WriteFile(target, []byte(address+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(directory, "link.txt")
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("symlink creation is unavailable: %v", err)
		}
		if _, err := loadSimulatorOperatorAddressesFile(link); err == nil || !strings.Contains(err.Error(), "regular, non-symlink") {
			t.Fatalf("symlink error = %v", err)
		}
	})

	t.Run("oversized", func(t *testing.T) {
		content := strings.Repeat("#", int(simulatorOperatorAddressesFileMaxBytes)+1)
		path := writeSimulatorOperatorAddressesFile(t, content)
		if _, err := loadSimulatorOperatorAddressesFile(path); err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("oversized file error = %v", err)
		}
	})
}

func TestLoadSimulatorOperatorAddressesFileRejectsMoreThanLimit(t *testing.T) {
	addresses := make([]string, 0, simulatorOperatorMaxAddresses+1)
	for index := 0; index <= simulatorOperatorMaxAddresses; index++ {
		addresses = append(addresses, simulatorOperatorFileTestAddress(t))
	}
	path := writeSimulatorOperatorAddressesFile(t, strings.Join(addresses, "\n")+"\n")
	if _, err := loadSimulatorOperatorAddressesFile(path); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("exceeds %d", simulatorOperatorMaxAddresses)) {
		t.Fatalf("address limit error = %v", err)
	}
}

func simulatorOperatorFileTestAddress(t *testing.T) string {
	t.Helper()
	privateKey, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("generate private key: %v", err)
	}
	address, err := utils.DeriveDexAddress(privateKey)
	if err != nil {
		t.Fatalf("derive address: %v", err)
	}
	return address
}

func writeSimulatorOperatorAddressesFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "operator-addresses.txt")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write operator addresses fixture: %v", err)
	}
	return path
}
