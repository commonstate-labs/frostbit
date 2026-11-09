package main

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"testing"

	"dex/config"
	"dex/frost/ceremony"
	"dex/keys"
	"dex/pb"
	"google.golang.org/protobuf/proto"
)

func TestLegacyRetainedGenesisModeCannotInstallBatchVaultRuntime(t *testing.T) {
	raw, err := os.ReadFile("../../frost/ceremony/testdata/key-reuse-v1/reused.json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.FrostConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if marker, err := configuredFrostGenesisMarkerValue(cfg); err == nil || marker != "" {
		t.Fatalf("legacy single-withdraw Vault unexpectedly produced a batch-runtime marker: %q %v", marker, err)
	}
}

func TestRetainedKeyBootstrapPreservesSixOriginalCommitteesWithoutDKG(t *testing.T) {
	read := func(name string) []byte {
		b, err := os.ReadFile("../../frost/ceremony/testdata/key-reuse-v1/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	frost, err := ceremony.PrepareGenesisKeyReuse(read("source.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := ceremony.StrictDecodeManifest(read("manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var peers config.PeerManifest
	if err := json.Unmarshal(read("peers.json"), &peers); err != nil {
		t.Fatal(err)
	}
	var addresses []string
	for _, v := range peers.GenesisValidators {
		addresses = append(addresses, v.Address)
	}
	sort.Strings(addresses)
	top := configuredBootstrapTop(addresses...)
	installTestGuardian(&frost)
	configs := buildVaultConfigs(frost, len(addresses))
	for _, original := range manifest.Vaults {
		t.Run(original.Chain, func(t *testing.T) {
			sv, _ := newMapBackedStateView()
			raw, _ := proto.Marshal(top)
			sv.Set(keys.KeyFrostActiveMiners(), raw)
			indices, err := ceremony.InitialCommitteeIndices(frost, top.Indices, original.Chain, 0)
			if err != nil {
				t.Fatal(err)
			}
			var members []string
			for _, i := range indices {
				members = append(members, addresses[i])
			}
			for repeat := 0; repeat < 2; repeat++ {
				if err := applyChainFrostBootstrap(sv, original.Chain, frost.Chains[original.Chain], configs[original.Chain], 1, 20, 5, 5, 5, [][]string{members}); err != nil {
					t.Fatal(err)
				}
			}
			raw, exists, err := sv.Get(keys.KeyFrostVaultState(original.Chain, 0))
			if err != nil || !exists {
				t.Fatal("missing vault", err)
			}
			var state pb.FrostVaultState
			if err := proto.Unmarshal(raw, &state); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(state.GroupPubkey, original.RawGroupPubkey) || !reflect.DeepEqual(state.CommitteeMembers, original.Committee) || state.CommitteeThreshold != 3 || state.KeyEpoch != 1 || state.Status != "ACTIVE" {
				t.Fatal("original custody changed")
			}
			if _, exists, err := sv.Get(keys.KeyFrostVaultTransition(original.Chain, 0, 1)); err != nil || exists {
				t.Fatal("unexpected DKG", err)
			}
		})
	}
}
