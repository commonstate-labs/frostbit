package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"dex/config"
	"dex/formal/itftrace"
	"dex/frost/ceremony"
	"dex/keys"
	"dex/pb"
	"dex/protocol"
	"dex/utils"
	"dex/vm"

	"google.golang.org/protobuf/proto"
)

                                                                          
                                                                             
                                                                          
                                                                  
func TestFormalFreshGenesisConfiguredCommitteeITFDifferential(t *testing.T) {
	path := os.Getenv("DEX_FORMAL_ITF_FRESH_GENESIS_ASSIGNMENT")
	if path == "" {
		t.Skip("fresh-genesis assignment ITF path is set by formal/run-go-differential.ps1")
	}
	trace, err := itftrace.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(trace.States) != 7 {
		t.Fatalf("assignment ITF states=%d want=7", len(trace.States))
	}
	addresses := []string{"a", "b", "c", "d", "e"}
	topSnapshot := configuredBootstrapTop(addresses...)
	addresses = topSnapshot.Addresses
	chains := ceremony.GenesisCeremonyChainOrder()
	for ordinal := 0; ordinal < 6; ordinal++ {
		state := trace.States[ordinal+1]
		modelOrdinal, err := state.Int("vaultOrdinal")
		if err != nil || modelOrdinal != int64(ordinal) {
			t.Fatalf("model ordinal=%d at %d: %v", modelOrdinal, ordinal, err)
		}
		name := chains[ordinal]
		chainCfg := configuredBootstrapChain(name)
		cfg := testDualCustodyFrostConfig()
		cfg.Chains = map[string]config.ChainFrostConfig{name: chainCfg}
		vaultCfg := buildVaultConfigs(cfg, len(addresses))[name]
		committee := make([]string, 4)
		for slot := range committee {
			member, err := state.Int(fmt.Sprintf("slot%d", slot))
			if err != nil || member < 0 || member >= int64(len(addresses)) {
				t.Fatalf("model signer slot %d: %v", slot, err)
			}
			committee[slot] = addresses[member]
		}
		sv, _ := newMapBackedStateView()
		top, err := proto.Marshal(topSnapshot)
		if err != nil {
			t.Fatal(err)
		}
		sv.Set(keys.KeyFrostActiveMiners(), top)
		if err := applyChainFrostBootstrap(sv, name, chainCfg, vaultCfg, 1, 20, 5, 5, 5, [][]string{committee}); err != nil {
			t.Fatalf("Vault %s bootstrap: %v", name, err)
		}
		raw, exists, err := sv.Get(keys.KeyFrostVaultState(name, 0))
		if err != nil || !exists {
			t.Fatalf("Vault %s active state absent: %v", name, err)
		}
		var active pb.FrostVaultState
		if err := proto.Unmarshal(raw, &active); err != nil {
			t.Fatal(err)
		}
		if active.KeyEpoch != 1 || active.Status != vm.VaultStatusActive || active.CommitteeThreshold != 3 || len(active.CommitteeMembers) != 4 {
			t.Fatalf("Vault %s active authority differs from fresh genesis: %+v", name, &active)
		}
		if active.ActiveSinceHeight != 1 || active.NextCommitteeReviewHeight != 1+protocol.VaultCommitteeTermBlocks {
			t.Fatalf("actual configured bootstrap term starts at H=1: %+v", &active)
		}
		for slot, expected := range committee {
			if active.CommitteeMembers[slot] != expected {
				t.Fatalf("Vault %s slot %d = %s, want %s", name, slot, active.CommitteeMembers[slot], expected)
			}
		}
	}
}

                                                                          
                                                                               
                                                                                              
                                                                    
func TestFormalFreshGenesisImportITFDifferential(t *testing.T) {
	cases := []struct {
		name string
		env  string
		kind string
	}{
		{"valid-import", "DEX_FORMAL_ITF_FRESH_GENESIS_IMPORT", "valid"},
		{"reject-sealed", "DEX_FORMAL_ITF_FRESH_GENESIS_REJECT_SEALED", "sealed"},
		{"reject-historical", "DEX_FORMAL_ITF_FRESH_GENESIS_REJECT_HISTORICAL", "historical"},
		{"reject-other-node", "DEX_FORMAL_ITF_FRESH_GENESIS_REJECT_OTHER_NODE", "other-node"},
		{"reject-wrong-point", "DEX_FORMAL_ITF_FRESH_GENESIS_REJECT_WRONG_POINT", "wrong-point"},
	}
	if os.Getenv(cases[0].env) == "" {
		t.Skip("fresh-genesis ITF paths are set by formal/run-go-differential.ps1")
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			trace, err := itftrace.Load(os.Getenv(tc.env))
			if err != nil {
				t.Fatal(err)
			}
			wantStates := 2
			if tc.kind == "valid" {
				wantStates = 3
			}
			if len(trace.States) != wantStates {
				t.Fatalf("ITF states=%d want=%d", len(trace.States), wantStates)
			}
			fixture := newGenesisImportFixtureWithFormat(t, 3, ceremony.PlaintextGenesisChainID, tc.kind != "sealed")
			switch tc.kind {
			case "historical":
				historical := newGenesisImportFixtureWithFormat(t, 3, ceremony.PlaintextGenesisChainID, true)
				writeGenesisImportJSON(t, filepath.Join(fixture.directory, genesisManifestName), historical.manifest)
			case "other-node":
				formalWriteOtherNodeShare(t, fixture)
			case "wrong-point":
				formalMutatePlaintextShareScalar(t, fixture)
			}

			started := false
			assertFormalFreshGenesisImportRelation(t, fixture, trace.States[0], started)
			for index := 1; index < len(trace.States); index++ {
				state := trace.States[index]
				switch state.Action {
				case "importCandidate", "differentialSealedRejectStep", "differentialHistoricalRejectStep",
					"differentialOtherNodeRejectStep", "differentialWrongPointRejectStep":
					err := importFrostGenesisSharesFromDirWithFormat(
						fixture.directory, fixture.nodeID, fixture.chainID, fixture.peerDigest,
						fixture.config, fixture.reader, fixture.store, fixture.store, nil, true,
					)
					if tc.kind == "valid" && err != nil {
						t.Fatalf("valid plaintext import failed: %v", err)
					}
					if tc.kind != "valid" && err == nil {
						t.Fatalf("invalid %s artifact was imported", tc.kind)
					}
				case "start":
					if tc.kind != "valid" {
						t.Fatal("rejected artifact reached runtime start")
					}
					if err := runGenesisPreflight(fixture, fixture.nodeID, fixture.store); err != nil {
						t.Fatalf("live restart preflight failed: %v", err)
					}
					started = true
				default:
					t.Fatalf("unexpected fresh-genesis ITF action %q", state.Action)
				}
				assertFormalFreshGenesisImportRelation(t, fixture, state, started)
			}
			if tc.kind == "valid" {
				                                                                     
				                                                                    
				restarted := &genesisImportStore{
					shares:   make(map[string][]byte, len(fixture.store.shares)),
					manifest: bytes.Clone(fixture.store.manifest),
				}
				for key, share := range fixture.store.shares {
					restarted.shares[key] = bytes.Clone(share)
				}
				if err := runGenesisPreflight(fixture, fixture.nodeID, restarted); err != nil {
					t.Fatalf("persisted fresh-genesis restart disagrees with signed manifest: %v", err)
				}
			}
		})
	}
	t.Run("foreign-chain-plaintext-mode", func(t *testing.T) {
		fixture := newGenesisImportFixtureWithFormat(t, 3, 771, true)
		if err := importFrostGenesisSharesFromDirWithFormat(
			fixture.directory, fixture.nodeID, fixture.chainID, fixture.peerDigest,
			fixture.config, fixture.reader, fixture.store, fixture.store, nil, true,
		); err == nil {
			t.Fatal("plaintext share mode escaped the 51025 development domain")
		}
		if fixture.store.saves != 0 || fixture.store.manifestWrites != 0 {
			t.Fatal("foreign-chain plaintext rejection wrote shares or receipt")
		}
	})
}

func assertFormalFreshGenesisImportRelation(t *testing.T, fixture *genesisImportFixture, state itftrace.State, started bool) {
	t.Helper()
	if domain, err := state.Int("externalDomain"); err != nil || domain != int64(fixture.chainID) {
		t.Fatalf("model/production DEX domain mismatch: %d %v", domain, err)
	}
	if epoch, err := state.Int("externalEpoch"); err != nil || epoch != 1 {
		t.Fatalf("model/production epoch mismatch: %d %v", epoch, err)
	}
	for _, field := range []string{"ceremony", "externalKey", "externalAddress"} {
		if tag, err := state.Tag(field); err != nil || tag != "Fresh" {
			t.Fatalf("model %s=%q err=%v", field, tag, err)
		}
	}
	if signed, err := state.Bool("manifestSigned"); err != nil || !signed {
		t.Fatalf("model signed manifest=%t err=%v", signed, err)
	}
	if verified, err := state.Bool("onlineVerified"); err != nil || !verified {
		t.Fatalf("model deployment proof cut point=%t err=%v", verified, err)
	}
	if published, err := state.Bool("proofPublished"); err != nil || !published {
		t.Fatalf("model deployment proof published=%t err=%v", published, err)
	}
	if approvals, err := state.SetTags("approvals"); err != nil || len(approvals) != 3 {
		t.Fatalf("model approval reduction has %d members: %v", len(approvals), err)
	}
	used := make(map[string]bool)
	for _, vault := range fixture.manifest.Plan.Vaults {
		if len(vault.Committee) != 4 {
			t.Fatal("fresh vault lost its four-member committee")
		}
		for _, id := range vault.Committee {
			used[id] = true
		}
	}
	if len(fixture.manifest.Plan.Participants) != len(used) || len(fixture.manifest.Attestations) != len(fixture.manifest.Plan.Participants) {
		t.Fatalf("production fixture lost full used-union DKG attestations: participants=%d attestations=%d",
			len(fixture.manifest.Plan.Participants), len(fixture.manifest.Attestations))
	}
	if _, err := ceremony.ManifestDigest(fixture.manifest, utils.VerifyECDSASignature); err != nil {
		t.Fatalf("production DKG manifest is invalid: %v", err)
	}
	imported, err := state.Bool("imported")
	if err != nil {
		t.Fatal(err)
	}
	wantLocalShares := 0
	for _, vault := range fixture.manifest.Vaults {
		if slices.Contains(vault.Committee, fixture.nodeID) {
			wantLocalShares++
		}
	}
	if wantLocalShares != len(fixture.manifest.Vaults) {
		t.Fatalf("shared TopK signer owns %d local Vaults, want all %d", wantLocalShares, len(fixture.manifest.Vaults))
	}
	actualImported := fixture.store.saves == wantLocalShares &&
		fixture.store.manifestWrites == 1 && len(fixture.store.shares) == wantLocalShares
	if imported != actualImported {
		t.Fatalf("model imported=%t actual shares=%d saves=%d receipt writes=%d",
			imported, len(fixture.store.shares), fixture.store.saves, fixture.store.manifestWrites)
	}
	if !imported && (fixture.store.saves != 0 || fixture.store.manifestWrites != 0 || len(fixture.store.shares) != 0) {
		t.Fatal("rejected import made partial durable writes")
	}
	if imported {
		for field, want := range map[string]string{
			"importedFormat": "PlainV1", "importedOrigin": "Fresh", "importedOwner": "Alice", "importedPoint": "Matches",
		} {
			if got, err := state.Tag(field); err != nil || got != want {
				t.Fatalf("model %s=%q want=%q err=%v", field, got, want, err)
			}
		}
		for _, vault := range fixture.manifest.Vaults {
			key := fmt.Sprintf("%s/%d/%d", vault.Chain, vault.VaultID, vault.Epoch)
			if slices.Contains(vault.Committee, fixture.nodeID) {
				if len(fixture.store.shares[key]) != 32 {
					t.Fatalf("persisted local share %s has invalid length", key)
				}
			} else if len(fixture.store.shares[key]) != 0 {
				t.Fatalf("node imported a share for nonmember Vault %s", key)
			}
		}
	}
	if ready, err := state.Bool("ready"); err != nil || ready != started {
		t.Fatalf("model ready=%t production start preflight=%t err=%v", ready, started, err)
	}
}

func formalWriteOtherNodeShare(t *testing.T, fixture *genesisImportFixture) {
	t.Helper()
	path := filepath.Join(fixture.directory, genesisShareFilename("btc", 0))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Version  uint32                 `json:"version"`
		Format   string                 `json:"format"`
		Metadata ceremony.ShareMetadata `json:"metadata"`
		Share    []byte                 `json:"share"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatal(err)
	}
	envelope.Metadata.NodeID = fixture.otherID
	mutated, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, mutated, 0o600); err != nil {
		t.Fatal(err)
	}
}

func formalMutatePlaintextShareScalar(t *testing.T, fixture *genesisImportFixture) {
	t.Helper()
	path := filepath.Join(fixture.directory, genesisShareFilename("btc", 0))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Version  uint32                 `json:"version"`
		Format   string                 `json:"format"`
		Metadata ceremony.ShareMetadata `json:"metadata"`
		Share    []byte                 `json:"share"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Share) != 32 {
		t.Fatal("fixture scalar is not 32 bytes")
	}
	envelope.Share[31] ^= 1                                                
	mutated, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, mutated, 0o600); err != nil {
		t.Fatal(err)
	}
}
