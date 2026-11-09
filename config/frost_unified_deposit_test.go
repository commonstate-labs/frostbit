package config

import (
	"encoding/json"
	"testing"
)

func TestUnifiedVaultDepositConfiguration(t *testing.T) {
	const vault = "0x1111111111111111111111111111111111111111"
	for _, name := range []string{"eth", "bnb", "base", "arb", "trx"} {
		if err := ValidateDepositConfig(name, 2, 51025, []string{vault}); err != nil {
			t.Fatal(name, err)
		}
		for _, tc := range []struct {
			version uint32
			domain  uint64
			refs    []string
		}{
			{2, 0, []string{vault}}, {2, 51025, nil},
			{2, 51025, []string{""}}, {2, 51025, []string{"0x0000000000000000000000000000000000000000"}},
			{3, 51025, []string{vault}}, {0, 51025, []string{vault}},
		} {
			if err := ValidateDepositConfig(name, tc.version, tc.domain, tc.refs); err == nil {
				t.Fatalf("%s accepted %+v", name, tc)
			}
		}
	}
	if err := ValidateDepositConfig("btc", 2, 51025, []string{vault}); err == nil {
		t.Fatal("BTC accepted unified Vault protocol")
	}
	if err := ValidateDepositConfig("unknown", 2, 51025, []string{vault}); err == nil {
		t.Fatal("unsupported chain accepted unified Vault protocol")
	}
	var cfg FrostConfig
	if err := json.Unmarshal([]byte(`{"chains":{"eth":{"authorityProtocol":3,"depositProtocol":2,"depositDexChainId":51025,"vaultRefs":["`+vault+`"]},"trx":{"authorityProtocol":3,"vaultRefs":["`+vault+`"]}}}`), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Chains["eth"].DepositProtocol != 2 || cfg.Chains["trx"].DepositProtocol != 0 {
		t.Fatal("lost explicit protocol")
	}
	if err := ValidateDeposits(cfg, 51025); err != nil {
		t.Fatal(err)
	}
	if err := ValidateDeposits(cfg, 51026); err == nil {
		t.Fatal("accepted foreign DEX domain")
	}
}
