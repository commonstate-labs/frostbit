package config_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"testing"

	"dex/config"
	"dex/protocol"
	"dex/utils"
	"github.com/btcsuite/btcd/btcec/v2"
)

func formalGenesisStakeInput(t *testing.T) (config.FrostConfig, *config.PeerManifest, *config.GenesisConfig) {
	t.Helper()
	frost := config.DefaultFrostConfig()
	frost.Enabled = true
	peers := &config.PeerManifest{ChainID: 51025}
	for i := byte(23); i < 28; i++ {
		key, _ := btcec.PrivKeyFromBytes([]byte{i})
		address, err := utils.AddressFromSecp256k1PubKey(key.PubKey().SerializeCompressed())
		if err != nil {
			t.Fatal(err)
		}
		peers.GenesisValidators = append(peers.GenesisValidators, config.GenesisValidatorConfig{Address: address, IsMiner: true})
	}
	genesis := &config.GenesisConfig{ChainID: 51025, Alloc: map[string]config.GenesisAlloc{
		"default": {Balances: map[string]config.GenesisBalance{"FB": {MinerLockedBalance: strconv.FormatUint(protocol.MinMinerStakeRaw, 10)}}},
	}}
	return frost, peers, genesis
}

func TestFormalGenesisAlignmentStakePreflight(t *testing.T) {
	for _, tc := range []struct {
		name     string
		edit     func(*config.FrostConfig, **config.PeerManifest, **config.GenesisConfig)
		accepted bool
	}{
		{"equal-default", func(*config.FrostConfig, **config.PeerManifest, **config.GenesisConfig) {}, true},
		{"equal-explicit", func(_ *config.FrostConfig, p **config.PeerManifest, g **config.GenesisConfig) {
			(*g).Alloc[(*p).GenesisValidators[0].Address] = (*g).Alloc["default"]
		}, true},
		{"unequal-explicit-priority", func(_ *config.FrostConfig, p **config.PeerManifest, g **config.GenesisConfig) {
			(*g).Alloc[(*p).GenesisValidators[0].Address] = config.GenesisAlloc{Balances: map[string]config.GenesisBalance{"FB": {MinerLockedBalance: strconv.FormatUint(protocol.MinMinerStakeRaw+1, 10)}}}
		}, false},
		{"explicit-missing-fb-does-not-inherit", func(_ *config.FrostConfig, p **config.PeerManifest, g **config.GenesisConfig) {
			(*g).Alloc[(*p).GenesisValidators[0].Address] = config.GenesisAlloc{Balances: map[string]config.GenesisBalance{}}
		}, false},
		{"no-default", func(_ *config.FrostConfig, _ **config.PeerManifest, g **config.GenesisConfig) {
			delete((*g).Alloc, "default")
		}, false},
		{"nil-peers", func(_ *config.FrostConfig, p **config.PeerManifest, _ **config.GenesisConfig) { *p = nil }, false},
		{"nil-genesis", func(_ *config.FrostConfig, _ **config.PeerManifest, g **config.GenesisConfig) { *g = nil }, false},
		{"chain-mismatch", func(_ *config.FrostConfig, _ **config.PeerManifest, g **config.GenesisConfig) { (*g).ChainID++ }, false},
		{"duplicate-miner", func(_ *config.FrostConfig, p **config.PeerManifest, _ **config.GenesisConfig) {
			(*p).GenesisValidators = append((*p).GenesisValidators, (*p).GenesisValidators[0])
		}, false},
		{"non-miner-does-not-rank", func(_ *config.FrostConfig, p **config.PeerManifest, _ **config.GenesisConfig) {
			(*p).GenesisValidators = append((*p).GenesisValidators, config.GenesisValidatorConfig{Address: "observer", IsMiner: false})
		}, true},
		{"disabled-scope", func(f *config.FrostConfig, p **config.PeerManifest, g **config.GenesisConfig) {
			f.Enabled = false
			*p = nil
			*g = nil
		}, true},
		{"retained-scope", func(f *config.FrostConfig, p **config.PeerManifest, g **config.GenesisConfig) {
			f.GenesisKeyReuse = json.RawMessage(`{}`)
			*p = nil
			*g = nil
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frost, peers, genesis := formalGenesisStakeInput(t)
			tc.edit(&frost, &peers, &genesis)
			before, _ := json.Marshal([]any{frost, peers, genesis})
			err := config.ValidateFreshFrostGenesisStakePolicy(frost, peers, genesis)
			if (err == nil) != tc.accepted {
				t.Fatalf("accepted=%v want=%v error=%v", err == nil, tc.accepted, err)
			}
			after, _ := json.Marshal([]any{frost, peers, genesis})
			if !reflect.DeepEqual(before, after) {
				t.Fatal("pure preflight mutated signed public input")
			}
		})
	}
	for _, amount := range []string{"", "-1", "not-a-number", "18446744073709551616", "0", strconv.FormatUint(protocol.MinMinerStakeRaw-1, 10)} {
		t.Run(fmt.Sprintf("invalid-%s", amount), func(t *testing.T) {
			frost, peers, genesis := formalGenesisStakeInput(t)
			genesis.Alloc["default"] = config.GenesisAlloc{Balances: map[string]config.GenesisBalance{"FB": {MinerLockedBalance: amount}}}
			if err := config.ValidateFreshFrostGenesisStakePolicy(frost, peers, genesis); err == nil {
				t.Fatal("invalid effective miner stake admitted")
			}
		})
	}
}
