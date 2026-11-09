package main

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"dex/config"
	"dex/frost/chain/evm"
	"dex/frost/core/curve"
)

func testNetworkGuardian() config.GuardianConfig {
	btc := curve.NewSecp256k1Group()
	account := curve.NewBN256Group()
	b := btc.SerializePoint(btc.ScalarBaseMult(big.NewInt(31)))
	a, _ := evm.CanonicalBN128PublicKey(account.SerializePoint(account.ScalarBaseMult(big.NewInt(31))))
	return config.GuardianConfig{BTCPublicKey: hex.EncodeToString(b[1:]), AccountPublicKey: hex.EncodeToString(a)}
}

func TestNetworkGenerationRequiresExplicitGuardianBeforeWriting(t *testing.T) {
	output := filepath.Join(t.TempDir(), "new-network")
	err := generateNetworkTopologyWithProfile(output, 5, 5, 51025, "127.0.0.1", 6000, "", runtimeProfilePhysical20FrostStress)
	if err == nil || !strings.Contains(err.Error(), "--frost-config") {
		t.Fatalf("missing guardian error: %v", err)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("missing guardian created network output")
	}
	for _, mutate := range []func(*config.GuardianConfig){
		func(g *config.GuardianConfig) { g.BTCPublicKey = "" },
		func(g *config.GuardianConfig) { g.AccountPublicKey = strings.Repeat("00", 64) },
	} {
		guardian := testNetworkGuardian()
		mutate(&guardian)
		if err := requireGeneratorGuardian(runtimeProfilePhysical20FrostStress, []config.GuardianConfig{guardian}); err == nil {
			t.Fatal("invalid fixed public key accepted")
		}
	}
}

func TestLoadGeneratorGuardianPreservesExplicitPublicKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "frost.json")
	cfg := config.DefaultFrostConfig()
	cfg.Guardian = testNetworkGuardian()
	cfg.Guardian.Endpoint = "https://guardian.example"
	raw, _ := json.Marshal(cfg)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	guardian, err := loadGeneratorGuardian(path, runtimeProfilePhysical20FrostStress)
	if err != nil || guardian != cfg.Guardian {
		t.Fatalf("public guardian was not preserved: %v", err)
	}
	output := filepath.Join(t.TempDir(), "new-network")
	if err := generateNetworkTopologyWithProfile(output, 5, 5, 51025, "127.0.0.1", 6000, "", runtimeProfilePhysical20FrostStress, guardian); err != nil {
		t.Fatal(err)
	}
	generated, err := config.LoadFrostConfig(filepath.Join(output, "frost_default.json"))
	if err != nil || generated.Guardian != guardian {
		t.Fatalf("generated guardian differs: %v", err)
	}
}

func TestNetworkGuardianCLI(t *testing.T) {
	if os.Getenv("DEX_TEST_NETWORK_GUARDIAN_CLI") == "1" {
		for i, value := range os.Args {
			if value == "--" {
				os.Args = append([]string{"network-init"}, os.Args[i+1:]...)
				break
			}
		}
		flag.CommandLine = flag.NewFlagSet("network-init", flag.ExitOnError)
		main()
		os.Exit(0)
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "public-frost.json")
	cfg := config.DefaultFrostConfig()
	cfg.Guardian = testNetworkGuardian()
	raw, _ := json.Marshal(cfg)
	if err := os.WriteFile(source, raw, 0600); err != nil {
		t.Fatal(err)
	}
	for _, supplied := range []bool{false, true} {
		output := filepath.Join(dir, map[bool]string{false: "missing", true: "configured"}[supplied])
		args := []string{"-test.run=^TestNetworkGuardianCLI$", "--", "--output-dir", output, "--validators", "5", "--chain-id", "51025", "--runtime-profile", string(runtimeProfilePhysical20FrostStress)}
		if supplied {
			args = append(args, "--frost-config", source)
		}
		command := exec.Command(os.Args[0], args...)
		command.Env = append(os.Environ(), "DEX_TEST_NETWORK_GUARDIAN_CLI=1")
		text, err := command.CombinedOutput()
		if !supplied {
			if err == nil || !strings.Contains(string(text), "--frost-config") {
				t.Fatalf("missing public config: %v %s", err, text)
			}
			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatal("CLI generated network without guardian")
			}
			continue
		}
		if err != nil {
			t.Fatalf("configured CLI: %v %s", err, text)
		}
		actual, err := config.LoadFrostConfig(filepath.Join(output, "frost_default.json"))
		if err != nil || actual.Guardian != cfg.Guardian {
			t.Fatalf("CLI did not pin public guardian: %v", err)
		}
	}
}
