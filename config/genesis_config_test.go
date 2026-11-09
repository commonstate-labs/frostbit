package config

import (
	bridgeasset "dex/frost/asset"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDefaultGenesisKeepsCanonicalBuiltInAssets(t *testing.T) {
	genesis := DefaultGenesisConfig()
	if len(genesis.Tokens) != 16 {
		t.Fatalf("default token count=%d want 16", len(genesis.Tokens))
	}
	byAddress := make(map[string]GenesisToken, len(genesis.Tokens))
	for _, token := range genesis.Tokens {
		byAddress[token.Address] = token
	}
	if token := byAddress["FB"]; token.Type != "SYSTEM" {
		t.Fatalf("unexpected FB token: %+v", token)
	}
	for _, native := range []struct{ chain, contract string }{
		{chain: "btc"},
		{chain: "eth"},
		{chain: "bnb"},
		{chain: "trx"},
		{chain: "eth", contract: "0xdAC17F958D2ee523a2206206994597C13D831ec7"},
	} {
		identity, err := bridgeasset.Resolve(native.chain, native.contract)
		if err != nil {
			t.Fatal(err)
		}
		token, exists := byAddress[identity.ID]
		if !exists || token.NativeChain != identity.Chain || token.NativeContract != identity.Contract {
			t.Fatalf("missing canonical built-in asset %+v", identity)
		}
	}
}

func TestCheckedInGenesisUsesCanonicalBuiltInAssets(t *testing.T) {
	want := DefaultGenesisConfig().Tokens
	for _, path := range []string{
		"genesis.json",
		filepath.Join("env", "dev", "genesis.json"),
		filepath.Join("env", "local20", "genesis.json"),
		filepath.Join("env", "test", "genesis.json"),
		filepath.Join("env", "prod", "genesis.json"),
	} {
		genesis, err := LoadGenesisConfig(path)
		if err != nil {
			t.Fatalf("load %s: %v", path, err)
		}
		if len(genesis.Tokens) != len(want) {
			t.Fatalf("%s token count=%d want %d", path, len(genesis.Tokens), len(want))
		}
		if !reflect.DeepEqual(genesis.Tokens, want) {
			t.Fatalf("%s token registry differs from DefaultGenesisConfig", path)
		}
		if err := ValidateGenesisConfig(genesis); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
}

func TestLoadGenesisConfigRejectsNonCanonicalBridgedAssetID(t *testing.T) {
	genesis := DefaultGenesisConfig()
	genesis.Tokens[1].Address = "BTC"
	encoded, err := json.Marshal(genesis)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "genesis.json")
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	_, err = LoadGenesisConfig(path)
	if err == nil {
		t.Fatal("non-canonical bridged genesis AssetID was accepted")
	}
}
