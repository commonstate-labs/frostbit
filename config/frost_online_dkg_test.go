package config

import (
	"strings"
	"testing"
)

func TestValidateOnlineDKGVaultRefs(t *testing.T) {
	cfg := DefaultFrostConfig()
	base := cfg.Chains["base"]
	base.OnlineDKGVaultRefs = []string{"0x000000000000000000000000000000000000BA00"}
	cfg.Chains["base"] = base
	if err := ValidateOnlineDKGVaultRefs(cfg); err != nil {
		t.Fatalf("valid online-DKG ref rejected: %v", err)
	}

	btc := cfg.Chains["btc"]
	btc.OnlineDKGVaultRefs = []string{"0x000000000000000000000000000000000000B700"}
	cfg.Chains["btc"] = btc
	if err := ValidateOnlineDKGVaultRefs(cfg); err == nil || !strings.Contains(err.Error(), "cannot configure") {
		t.Fatalf("BTC online-DKG contract ref accepted: %v", err)
	}
}

func TestValidateOnlineDKGVaultRefsRejectsCountAndAddress(t *testing.T) {
	for _, tt := range []struct {
		name string
		refs []string
	}{
		{name: "count", refs: []string{"0x000000000000000000000000000000000000BA00", "0x000000000000000000000000000000000000BA01"}},
		{name: "zero", refs: []string{"0x0000000000000000000000000000000000000000"}},
		{name: "malformed", refs: []string{"0x123"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultFrostConfig()
			base := cfg.Chains["base"]
			base.OnlineDKGVaultRefs = tt.refs
			cfg.Chains["base"] = base
			if err := ValidateOnlineDKGVaultRefs(cfg); err == nil {
				t.Fatal("invalid online-DKG refs accepted")
			}
		})
	}
}
