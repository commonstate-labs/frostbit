package config

import (
	"encoding/json"
	"testing"
)

func TestFreshFrostGenesisStakePolicy(t *testing.T) {
	newInputs := func() (FrostConfig, *PeerManifest, *GenesisConfig) {
		frost := DefaultFrostConfig()
		frost.Enabled = true
		peers := &PeerManifest{ChainID: 51025, GenesisValidators: []GenesisValidatorConfig{
			{Address: "miner-a", IsMiner: true}, {Address: "miner-b", IsMiner: true},
		}}
		genesis := &GenesisConfig{ChainID: 51025, Alloc: map[string]GenesisAlloc{
			"default": {Balances: map[string]GenesisBalance{"FB": {MinerLockedBalance: "3200000000"}}},
		}}
		return frost, peers, genesis
	}
	for _, tc := range []struct {
		name      string
		edit      func(*FrostConfig, *PeerManifest, *GenesisConfig)
		wantError bool
	}{
		{name: "default fallback"},
		{name: "equal numeric values and different spendable balances", edit: func(_ *FrostConfig, _ *PeerManifest, g *GenesisConfig) {
			g.Alloc["miner-a"] = GenesisAlloc{Balances: map[string]GenesisBalance{"FB": {Balance: "123456789", MinerLockedBalance: "+03200000000"}}}
		}},
		{name: "unequal stake", wantError: true, edit: func(_ *FrostConfig, _ *PeerManifest, g *GenesisConfig) {
			g.Alloc["miner-b"] = GenesisAlloc{Balances: map[string]GenesisBalance{"FB": {MinerLockedBalance: "3200000001"}}}
		}},
		{name: "address allocation cannot inherit individual default fields", wantError: true, edit: func(_ *FrostConfig, _ *PeerManifest, g *GenesisConfig) {
			g.Alloc["miner-a"] = GenesisAlloc{Balances: map[string]GenesisBalance{"FB": {Balance: "999"}}}
		}},
		{name: "wrong chain", wantError: true, edit: func(_ *FrostConfig, p *PeerManifest, _ *GenesisConfig) { p.ChainID++ }},
		{name: "empty chain", wantError: true, edit: func(_ *FrostConfig, p *PeerManifest, _ *GenesisConfig) { p.ChainID = 0 }},
		{name: "duplicate identity", wantError: true, edit: func(_ *FrostConfig, p *PeerManifest, _ *GenesisConfig) { p.GenesisValidators[1].Address = "miner-a" }},
		{name: "missing miners", wantError: true, edit: func(_ *FrostConfig, p *PeerManifest, _ *GenesisConfig) { p.GenesisValidators = nil }},
		{name: "nonminer does not rank", edit: func(_ *FrostConfig, p *PeerManifest, _ *GenesisConfig) { p.GenesisValidators[1].IsMiner = false }},
		{name: "disabled does not impose genesis policy", edit: func(f *FrostConfig, _ *PeerManifest, g *GenesisConfig) { f.Enabled = false; g.Alloc = nil }},
		{name: "explicit reuse verified separately", edit: func(f *FrostConfig, _ *PeerManifest, g *GenesisConfig) {
			f.GenesisKeyReuse = json.RawMessage(`{"proof":"validated elsewhere"}`)
			g.Alloc = nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frost, peers, genesis := newInputs()
			if tc.edit != nil {
				tc.edit(&frost, peers, genesis)
			}
			if err := ValidateFreshFrostGenesisStakePolicy(frost, peers, genesis); (err != nil) != tc.wantError {
				t.Fatalf("error=%v, wantError=%v", err, tc.wantError)
			}
		})
	}
	for _, raw := range []string{"", "-1", "3199999999", "18446744073709551616", "3.2e9", "abc"} {
		t.Run("invalid/"+raw, func(t *testing.T) {
			frost, peers, genesis := newInputs()
			genesis.Alloc["default"] = GenesisAlloc{Balances: map[string]GenesisBalance{"FB": {MinerLockedBalance: raw}}}
			if err := ValidateFreshFrostGenesisStakePolicy(frost, peers, genesis); err == nil {
				t.Fatalf("accepted invalid stake %q", raw)
			}
		})
	}
	frost, peers, genesis := newInputs()
	if err := ValidateFreshFrostGenesisStakePolicy(frost, nil, genesis); err == nil {
		t.Fatal("accepted missing peers")
	}
	if err := ValidateFreshFrostGenesisStakePolicy(frost, peers, nil); err == nil {
		t.Fatal("accepted missing genesis")
	}
}
