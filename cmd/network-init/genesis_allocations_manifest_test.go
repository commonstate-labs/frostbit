package main

import (
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"dex/config"
)

const testOrdinaryGenesisAddress = "fbc1qw508d6qejxtdg4y5r3zarvary0c5xw7kng2fs8"

func TestGenerateNetworkMergesStrictPublicGenesisAllocations(t *testing.T) {
	tokens := config.DefaultGenesisConfig().Tokens
	allocation := quarterSupplyAllocation(t, tokens)
	output := filepath.Join(t.TempDir(), "network")
	if err := generateNetworkTopologyWithProfileAndAllocations(
		output, 5, 8, 51021, "127.0.0.1", 6000, "",
		runtimeProfilePhysical20FrostStress,
		map[string]config.GenesisAlloc{testOrdinaryGenesisAddress: allocation},
		testNetworkGuardian()); err != nil {
		t.Fatalf("generate network with public allocations: %v", err)
	}
	genesis, err := config.LoadGenesisConfig(filepath.Join(output, "genesis.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := genesis.Alloc[testOrdinaryGenesisAddress]; !reflect.DeepEqual(got, allocation) {
		t.Fatalf("ordinary allocation mismatch\ngot:  %#v\nwant: %#v", got, allocation)
	}
	if err := validatePublicGenesisAllocationsMatch(output, map[string]config.GenesisAlloc{testOrdinaryGenesisAddress: allocation}, true); err != nil {
		t.Fatalf("exact public allocation match failed: %v", err)
	}
	tampered := cloneGenesisAllocation(allocation)
	tampered.Balances["UNREGISTERED"] = config.GenesisBalance{Balance: "1"}
	if err := validatePublicGenesisAllocationsMatch(output, map[string]config.GenesisAlloc{testOrdinaryGenesisAddress: tampered}, true); err == nil {
		t.Fatal("mismatching public allocation manifest was accepted")
	}
}

func TestGenericProfileAcceptsOnlyExplicitOrdinaryAllocations(t *testing.T) {
	output := filepath.Join(t.TempDir(), "network")
	allocation := config.GenesisAlloc{Balances: map[string]config.GenesisBalance{
		"FB": {Balance: "1000000000000"},
	}}
	if err := generateNetworkTopologyWithProfileAndAllocations(
		output, 2, 2, 51022, "127.0.0.1", 6000, "", runtimeProfileGeneric,
		map[string]config.GenesisAlloc{testOrdinaryGenesisAddress: allocation},
		testNetworkGuardian()); err != nil {
		t.Fatalf("generic explicit ordinary allocation: %v", err)
	}
	genesis, err := config.LoadGenesisConfig(filepath.Join(output, "genesis.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := genesis.Alloc[testOrdinaryGenesisAddress]; !exists {
		t.Fatal("explicit generic ordinary allocation was omitted")
	}

	withoutOptIn := filepath.Join(t.TempDir(), "network")
	if err := generateNetworkTopologyWithProfile(withoutOptIn, 2, 2, 51023, "127.0.0.1", 6000, "", runtimeProfileGeneric, testNetworkGuardian()); err != nil {
		t.Fatal(err)
	}
	genesis, err = config.LoadGenesisConfig(filepath.Join(withoutOptIn, "genesis.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(genesis.Alloc) != 2 {
		t.Fatalf("generic profile generated implicit ordinary accounts: alloc=%d", len(genesis.Alloc))
	}
}

func TestLoadPublicGenesisAllocationsFailsClosed(t *testing.T) {
	allocation := config.GenesisAlloc{Balances: map[string]config.GenesisBalance{"FB": {Balance: "1"}}}
	document := publicGenesisAllocationsDocument{
		SchemaVersion: publicGenesisAllocationsSchema,
		Kind:          publicGenesisAllocationsKind,
		Notice:        "PUBLIC GENESIS ALLOCATIONS — NO PRIVATE KEYS",
		Alloc:         map[string]config.GenesisAlloc{testOrdinaryGenesisAddress: allocation},
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "genesis-allocations.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadPublicGenesisAllocations(path)
	if err != nil {
		t.Fatalf("load valid public allocations: %v", err)
	}
	if !reflect.DeepEqual(loaded, document.Alloc) {
		t.Fatalf("loaded allocations mismatch: %#v", loaded)
	}

	withSecret := strings.Replace(string(encoded), `"alloc":`, `"private_key":"forbidden","alloc":`, 1)
	if err := os.WriteFile(path, []byte(withSecret), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPublicGenesisAllocations(path); err == nil {
		t.Fatal("private_key field was accepted in public allocations")
	}
}

func TestOrdinaryGenesisAllocationValidationRejectsMinerStateAndMalformedAmounts(t *testing.T) {
	tokens := config.DefaultGenesisConfig().Tokens
	valid := quarterSupplyAllocation(t, tokens)
	tests := []struct {
		name   string
		mutate func(config.GenesisAlloc) config.GenesisAlloc
	}{
		{"miner locked", func(a config.GenesisAlloc) config.GenesisAlloc {
			b := a.Balances["FB"]
			b.MinerLockedBalance = "1"
			a.Balances["FB"] = b
			return a
		}},
		{"leading zero", func(a config.GenesisAlloc) config.GenesisAlloc {
			a.Balances["FB"] = config.GenesisBalance{Balance: "01"}
			return a
		}},
		{"missing asset", func(a config.GenesisAlloc) config.GenesisAlloc {
			delete(a.Balances, "FB")
			return a
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			allocation := test.mutate(cloneGenesisAllocation(valid))
			err := validateOrdinaryGenesisAllocations(map[string]config.GenesisAlloc{testOrdinaryGenesisAddress: allocation}, tokens, nil, true)
			if err == nil {
				t.Fatal("invalid ordinary allocation was accepted")
			}
		})
	}
}

func TestValidateExistingNetworkRejectsOrdinaryAllocationForLateJoinIdentity(t *testing.T) {
	output := filepath.Join(t.TempDir(), "network")
	const (
		validators = 5
		identities = 6
	)
	if err := generateNetworkTopologyWithProfile(output, validators, identities, 51024, "127.0.0.1", 6000, "", runtimeProfilePhysical20FrostStress, testNetworkGuardian()); err != nil {
		t.Fatal(err)
	}
	identityDocument, err := loadIdentityDocument(filepath.Join(output, "identities.json"))
	if err != nil {
		t.Fatal(err)
	}
	lateJoinAddress := identityDocument.Nodes[validators].Address
	genesisPath := filepath.Join(output, "genesis.json")
	genesis, err := config.LoadGenesisConfig(genesisPath)
	if err != nil {
		t.Fatal(err)
	}
	genesis.Alloc[lateJoinAddress] = quarterSupplyAllocation(t, genesis.Tokens)
	encoded, err := json.MarshalIndent(genesis, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(genesisPath, append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	err = validateExistingNetworkTopologyWithProfile(output, validators, identities, 51024, runtimeProfilePhysical20FrostStress)
	if err == nil || !strings.Contains(err.Error(), "duplicates node identity") {
		t.Fatalf("late-join identity allocation error=%v", err)
	}
}

func quarterSupplyAllocation(t *testing.T, tokens []config.GenesisToken) config.GenesisAlloc {
	t.Helper()
	balances := make(map[string]config.GenesisBalance, len(tokens))
	for _, token := range tokens {
		supply, ok := new(big.Int).SetString(token.TotalSupply, 10)
		if !ok {
			t.Fatalf("invalid token supply for %s", token.Address)
		}
		balances[token.Address] = config.GenesisBalance{Balance: new(big.Int).Quo(supply, big.NewInt(4)).String()}
	}
	return config.GenesisAlloc{Balances: balances}
}
