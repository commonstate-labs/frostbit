package config

import (
	bridgeasset "dex/frost/asset"
	"math/big"
	"testing"
)

func TestDefaultRentThresholdsUseEveryCanonicalGenesisAsset(t *testing.T) {
	assets := DefaultRentExemptAssets()
	if len(assets) != 15 {
		t.Fatalf("threshold count=%d, want 15", len(assets))
	}
	byID := make(map[string]string, len(assets))
	for _, asset := range assets {
		if _, duplicate := byID[asset.Token]; duplicate {
			t.Fatalf("duplicate rent asset %s", asset.Token)
		}
		value, ok := new(big.Int).SetString(asset.MinimumRaw, 10)
		if !ok || value.Sign() <= 0 {
			t.Fatalf("invalid threshold %+v", asset)
		}
		byID[asset.Token] = asset.MinimumRaw
	}
	for _, token := range DefaultGenesisConfig().Tokens {
		if token.Type == "BRIDGED" && byID[token.Address] == "" {
			t.Fatalf("genesis asset %s missing threshold", token.Address)
		}
	}
	for _, test := range []struct{ chain, contract, raw string }{
		{"btc", "", "13000"},
		{"eth", "", "4000000000000000"},
		{"base", "", "4000000000000000"},
		{"arb", "", "4000000000000000"},
		{"bnb", "", "14000000000000000"},
		{"trx", "", "30000000"},
		{"eth", "0xdAC17F958D2ee523a2206206994597C13D831ec7", "10000000"},
		{"eth", "0x6B175474E89094C44Da98b954EedeAC495271d0F", "10000000000000000000"},
		{"bnb", "0x55d398326f99059fF775485246999027B3197955", "10000000000000000000"},
	} {
		identity, err := bridgeasset.Resolve(test.chain, test.contract)
		if err != nil {
			t.Fatal(err)
		}
		if got := byID[identity.ID]; got != test.raw {
			t.Fatalf("%s/%s threshold=%s want %s", test.chain, test.contract, got, test.raw)
		}
	}
}
