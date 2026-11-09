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
	"dex/vm"
)

func TestGenesisAllocationForRuntimeProfileKeepsLegacyProfilesMinimal(t *testing.T) {
	tokens := config.DefaultGenesisConfig().Tokens
	for _, profile := range []runtimeProfile{runtimeProfileGeneric, runtimeProfilePhysical20Stress} {
		t.Run(string(profile), func(t *testing.T) {
			allocation, err := genesisAllocationForRuntimeProfile(profile, tokens, 5)
			if err != nil {
				t.Fatal(err)
			}
			if len(allocation.Balances) != 1 {
				t.Fatalf("balance count=%d want=1: %#v", len(allocation.Balances), allocation.Balances)
			}
			fb := allocation.Balances["FB"]
			if fb.Balance != vm.MinMinerStake.String() || fb.MinerLockedBalance != vm.MinMinerStake.String() {
				t.Fatalf("legacy profile FB allocation changed: %#v", fb)
			}
		})
	}
}

func TestTailArbitrageFundingContainsEveryGenesisAsset(t *testing.T) {
	stressGenesis, err := defaultGenesisForRuntimeProfile(runtimeProfilePhysical20FrostStress)
	if err != nil {
		t.Fatal(err)
	}
	allocation, err := tailArbitrageAllocationForRuntimeProfile(runtimeProfilePhysical20FrostStress, stressGenesis.Tokens)
	if err != nil {
		t.Fatal(err)
	}

	if len(allocation.Balances) != len(stressGenesis.Tokens) {
		t.Fatalf("tail arbitrage genesis assets=%d want=%d", len(allocation.Balances), len(stressGenesis.Tokens))
	}
	for _, token := range stressGenesis.Tokens {
		supply, _ := new(big.Int).SetString(token.TotalSupply, 10)
		want := new(big.Int).Quo(supply, big.NewInt(tailArbitrageSupplyDivisor)).String()
		if got := allocation.Balances[token.Address].Balance; got != want {
			t.Fatalf("tail arbitrage %s balance=%s want=%s", token.Address, got, want)
		}
	}
}

func TestGeneratePhysical20FrostStressFundsOnlyGenesisValidators(t *testing.T) {
	output := filepath.Join(t.TempDir(), "network")
	const (
		validatorCount = 5
		identityCount  = 8
		chainID        = uint64(51019)
	)
	if err := generateNetworkTopologyWithProfile(output, validatorCount, identityCount, chainID, "127.0.0.1", 6000, "", runtimeProfilePhysical20FrostStress, testNetworkGuardian()); err != nil {
		t.Fatalf("generate physical20 FROST stress network: %v", err)
	}

	genesis, err := config.LoadGenesisConfig(filepath.Join(output, "genesis.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := config.LoadPeerManifest(filepath.Join(output, "peers.json"))
	if err != nil {
		t.Fatal(err)
	}
	identities, err := loadIdentityDocument(filepath.Join(output, "identities.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := genesis.Alloc["default"]; exists {
		t.Fatal("generated FROST stress genesis contains forbidden default allocation")
	}
	if len(genesis.Alloc) != validatorCount {
		t.Fatalf("allocation count=%d want=%d", len(genesis.Alloc), validatorCount)
	}
	if len(manifest.GenesisValidators) != validatorCount {
		t.Fatalf("validator count=%d want=%d", len(manifest.GenesisValidators), validatorCount)
	}
	if len(genesis.Tokens) != physical20FrostStressTokenCount {
		t.Fatalf("token count=%d want=%d", len(genesis.Tokens), physical20FrostStressTokenCount)
	}

	wantBalances := expectedFrostStressBalances(t, genesis.Tokens, validatorCount)
	for _, validator := range manifest.GenesisValidators {
		allocation, exists := genesis.Alloc[validator.Address]
		if !exists {
			t.Fatalf("missing address-level allocation for %s", validator.Address)
		}
		if !reflect.DeepEqual(allocation.Balances, wantBalances) {
			t.Fatalf("validator %s balances do not match total_supply/(2*validators): %#v", validator.Address, allocation.Balances)
		}
	}
	for _, identity := range identities.Nodes[validatorCount:] {
		if _, exists := genesis.Alloc[identity.Address]; exists {
			t.Fatalf("late-join identity %s received a genesis allocation", identity.Address)
		}
	}
	if err := validateGenesisAllocationsWithinSupply(genesis); err != nil {
		t.Fatalf("generated allocations exceed supply: %v", err)
	}
	if err := validateExistingNetworkTopologyWithProfile(output, validatorCount, identityCount, chainID, runtimeProfilePhysical20FrostStress); err != nil {
		t.Fatalf("strict validation rejected generated network: %v", err)
	}
}

func TestGeneratePhysical20FrostStressFundsTailArbitrageAccounts(t *testing.T) {
	output := filepath.Join(t.TempDir(), "network")
	const (
		validatorCount = 5
		identityCount  = 20
		chainID        = uint64(51025)
	)
	ordinary := quarterSupplyAllocation(t, config.DefaultGenesisConfig().Tokens)
	if err := generateNetworkTopologyWithProfileAndAllocationsAndTailArbitrageFunding(
		output,
		validatorCount,
		identityCount,
		chainID,
		"127.0.0.1",
		6000,
		"",
		runtimeProfilePhysical20FrostStress,
		map[string]config.GenesisAlloc{testOrdinaryGenesisAddress: ordinary},
		true,
		testNetworkGuardian()); err != nil {
		t.Fatalf("generate physical20 FROST stress network with tail arbitrage funding: %v", err)
	}

	genesis, err := config.LoadGenesisConfig(filepath.Join(output, "genesis.json"))
	if err != nil {
		t.Fatal(err)
	}
	identities, err := loadIdentityDocument(filepath.Join(output, "identities.json"))
	if err != nil {
		t.Fatal(err)
	}
	wantTail, err := tailArbitrageAllocationForRuntimeProfile(runtimeProfilePhysical20FrostStress, genesis.Tokens)
	if err != nil {
		t.Fatal(err)
	}
	if len(genesis.Alloc) != 2*validatorCount+1 {
		t.Fatalf("allocation count=%d want=%d", len(genesis.Alloc), 2*validatorCount+1)
	}
	for _, identity := range identities.Nodes[validatorCount : identityCount-validatorCount] {
		if _, exists := genesis.Alloc[identity.Address]; exists {
			t.Fatalf("non-tail late-join identity %s received a genesis allocation", identity.Address)
		}
	}
	for _, identity := range identities.Nodes[identityCount-validatorCount:] {
		got, exists := genesis.Alloc[identity.Address]
		if !exists {
			t.Fatalf("tail arbitrage identity %s is missing its allocation", identity.Address)
		}
		if !reflect.DeepEqual(got, wantTail) {
			t.Fatalf("tail arbitrage allocation mismatch for %s\ngot:  %#v\nwant: %#v", identity.Address, got, wantTail)
		}
		for token, balance := range got.Balances {
			if balance.MinerLockedBalance != "" {
				t.Fatalf("tail arbitrage identity %s token %s miner_locked_balance=%q want omitted", identity.Address, token, balance.MinerLockedBalance)
			}
		}
	}
	if err := validateExistingNetworkTopologyWithProfileAndTailArbitrageFunding(output, validatorCount, identityCount, chainID, runtimeProfilePhysical20FrostStress, true); err != nil {
		t.Fatalf("strict tail arbitrage validation rejected generated network: %v", err)
	}
	if err := validatePublicGenesisAllocationsMatchWithTailArbitrageFunding(output, map[string]config.GenesisAlloc{testOrdinaryGenesisAddress: ordinary}, true, true); err != nil {
		t.Fatalf("ordinary allocation matching included protocol-managed tail arbitrage accounts: %v", err)
	}
	if err := validateExistingNetworkTopologyWithProfile(output, validatorCount, identityCount, chainID, runtimeProfilePhysical20FrostStress); err == nil {
		t.Fatal("funded tail arbitrage accounts were accepted without the explicit validation opt-in")
	}

	tailAddress := identities.Nodes[identityCount-validatorCount].Address
	tampered := genesis.Alloc[tailAddress]
	btc := tampered.Balances["BTC"]
	btc.Balance = "1"
	tampered.Balances["BTC"] = btc
	genesis.Alloc[tailAddress] = tampered
	encoded, err := json.MarshalIndent(genesis, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(output, "genesis.json"), append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateExistingNetworkTopologyWithProfileAndTailArbitrageFunding(output, validatorCount, identityCount, chainID, runtimeProfilePhysical20FrostStress, true); err == nil {
		t.Fatal("tampered tail arbitrage allocation was accepted")
	}
}

func TestTailArbitrageFundingRejectsUnsupportedProfileAndOverlappingIdentities(t *testing.T) {
	tests := []struct {
		name       string
		profile    runtimeProfile
		validators int
		identities int
		want       string
	}{
		{
			name:       "unsupported profile",
			profile:    runtimeProfilePhysical20Stress,
			validators: 5,
			identities: 20,
			want:       "requires --runtime-profile=physical20-frost-stress",
		},
		{
			name:       "overlaps validators",
			profile:    runtimeProfilePhysical20FrostStress,
			validators: 5,
			identities: 9,
			want:       "--identities >= 2*--validators",
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "network")
			err := generateNetworkTopologyWithProfileAndAllocationsAndTailArbitrageFunding(
				output,
				test.validators,
				test.identities,
				uint64(51026+index),
				"127.0.0.1",
				6000,
				"",
				test.profile,
				nil,
				true,
				testNetworkGuardian())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v want substring %q", err, test.want)
			}
			if _, statErr := os.Lstat(output); !os.IsNotExist(statErr) {
				t.Fatalf("invalid tail arbitrage request created output directory: %v", statErr)
			}
		})
	}
}

func TestPhysical20FrostStressGenesisValidationFailsClosed(t *testing.T) {
	valid, err := defaultGenesisForRuntimeProfile(runtimeProfilePhysical20FrostStress)
	if err != nil {
		t.Fatal(err)
	}
	validators := []config.GenesisValidatorConfig{{Address: "validator-a"}, {Address: "validator-b"}}
	valid.Alloc = make(map[string]config.GenesisAlloc, len(validators))
	for _, validator := range validators {
		allocation, err := genesisAllocationForRuntimeProfile(runtimeProfilePhysical20FrostStress, valid.Tokens, len(validators))
		if err != nil {
			t.Fatal(err)
		}
		valid.Alloc[validator.Address] = allocation
	}
	if err := validatePhysical20FrostStressGenesis(valid, validators); err != nil {
		t.Fatalf("valid funded genesis rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*config.GenesisConfig)
	}{
		{"default allocation", func(g *config.GenesisConfig) {
			g.Alloc["default"] = config.GenesisAlloc{Balances: map[string]config.GenesisBalance{}}
		}},
		{"missing validator asset", func(g *config.GenesisConfig) {
			a := g.Alloc[validators[0].Address]
			delete(a.Balances, "FB")
			g.Alloc[validators[0].Address] = a
		}},
		{"wrong available balance", func(g *config.GenesisConfig) {
			a := g.Alloc[validators[0].Address]
			balance := a.Balances["FB"]
			balance.Balance = "1"
			a.Balances["FB"] = balance
			g.Alloc[validators[0].Address] = a
		}},
		{"wrong miner stake", func(g *config.GenesisConfig) {
			a := g.Alloc[validators[0].Address]
			balance := a.Balances["FB"]
			balance.MinerLockedBalance = "0"
			a.Balances["FB"] = balance
			g.Alloc[validators[0].Address] = a
		}},
		{"changed token supply", func(g *config.GenesisConfig) {
			g.Tokens[0].TotalSupply = "1"
		}},
		{"unexpected bridged token", func(g *config.GenesisConfig) {
			g.Tokens = append(g.Tokens, config.GenesisToken{Address: "BRIDGED", TotalSupply: "1", Type: "BRIDGED"})
		}},
		{"unexpected validator allocation", func(g *config.GenesisConfig) {
			allocation, err := genesisAllocationForRuntimeProfile(runtimeProfilePhysical20FrostStress, g.Tokens, len(validators))
			if err != nil {
				t.Fatal(err)
			}
			g.Alloc["validator-c"] = allocation
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			genesis := cloneGenesisConfigForTest(t, valid)
			test.mutate(genesis)
			if err := validatePhysical20FrostStressGenesis(genesis, validators); err == nil {
				t.Fatal("invalid physical20 FROST stress genesis was accepted")
			}
		})
	}
}

func TestPhysical20FrostStressRejectsUnscaledGenesis(t *testing.T) {
	unscaled := config.DefaultGenesisConfig()
	scaled, err := defaultGenesisForRuntimeProfile(runtimeProfilePhysical20FrostStress)
	if err != nil {
		t.Fatal(err)
	}
	validators := []config.GenesisValidatorConfig{{Address: "validator-a"}, {Address: "validator-b"}}
	unscaled.Alloc = make(map[string]config.GenesisAlloc, len(validators))
	for _, validator := range validators {
		allocation, err := genesisAllocationForRuntimeProfile(runtimeProfilePhysical20FrostStress, scaled.Tokens, len(validators))
		if err != nil {
			t.Fatal(err)
		}
		unscaled.Alloc[validator.Address] = allocation
	}
	if err := validatePhysical20FrostStressGenesis(unscaled, validators); err == nil {
		t.Fatal("unscaled physical20 genesis was accepted")
	}
}

func TestPhysical20FrostStressRejectsUnderfundedDirectory(t *testing.T) {
	output := filepath.Join(t.TempDir(), "network")
	const (
		validatorCount = 5
		chainID        = uint64(51020)
	)
	if err := generateNetworkTopologyWithProfile(output, validatorCount, validatorCount, chainID, "127.0.0.1", 6000, "", runtimeProfilePhysical20FrostStress, testNetworkGuardian()); err != nil {
		t.Fatal(err)
	}
	genesisPath := filepath.Join(output, "genesis.json")
	genesis, err := config.LoadGenesisConfig(genesisPath)
	if err != nil {
		t.Fatal(err)
	}
	for address := range genesis.Alloc {
		allocation, err := genesisAllocationForRuntimeProfile(runtimeProfilePhysical20Stress, genesis.Tokens, validatorCount)
		if err != nil {
			t.Fatal(err)
		}
		genesis.Alloc[address] = allocation
	}
	encoded, err := json.MarshalIndent(genesis, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(genesisPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	err = validateExistingNetworkTopologyWithProfile(output, validatorCount, validatorCount, chainID, runtimeProfilePhysical20FrostStress)
	if err == nil {
		t.Fatal("underfunded FB allocation was accepted by the FROST stress profile")
	}
}

func TestValidateGenesisAllocationsWithinSupply(t *testing.T) {
	genesis := &config.GenesisConfig{
		Tokens: []config.GenesisToken{{Address: "ASSET", TotalSupply: "100"}},
		Alloc: map[string]config.GenesisAlloc{
			"validator-a": {Balances: map[string]config.GenesisBalance{"ASSET": {Balance: "60", MinerLockedBalance: "10"}}},
			"validator-b": {Balances: map[string]config.GenesisBalance{"ASSET": {Balance: "30"}}},
		},
	}
	if err := validateGenesisAllocationsWithinSupply(genesis); err != nil {
		t.Fatalf("exact supply allocation rejected: %v", err)
	}
	allocation := genesis.Alloc["validator-b"]
	allocation.Balances["ASSET"] = config.GenesisBalance{Balance: "31"}
	genesis.Alloc["validator-b"] = allocation
	if err := validateGenesisAllocationsWithinSupply(genesis); err == nil {
		t.Fatal("allocation exceeding total supply was accepted")
	}
}

func expectedFrostStressBalances(t *testing.T, tokens []config.GenesisToken, validatorCount int) map[string]config.GenesisBalance {
	t.Helper()
	denominator := big.NewInt(int64(2 * validatorCount))
	balances := make(map[string]config.GenesisBalance, len(tokens))
	for _, token := range tokens {
		supply, ok := new(big.Int).SetString(token.TotalSupply, 10)
		if !ok {
			t.Fatalf("invalid test token supply for %s", token.Address)
		}
		balances[token.Address] = config.GenesisBalance{Balance: new(big.Int).Quo(supply, denominator).String()}
	}
	fb := balances["FB"]
	fb.MinerLockedBalance = vm.MinMinerStake.String()
	balances["FB"] = fb
	return balances
}

func cloneGenesisConfigForTest(t *testing.T, input *config.GenesisConfig) *config.GenesisConfig {
	t.Helper()
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var clone config.GenesisConfig
	if err := json.Unmarshal(data, &clone); err != nil {
		t.Fatal(err)
	}
	return &clone
}
