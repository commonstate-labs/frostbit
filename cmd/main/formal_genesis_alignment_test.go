package main

import (
	"bytes"
	"testing"

	"dex/config"
	"dex/frost/ceremony"
	"dex/keys"
	"dex/pb"
	"dex/utils"
	"google.golang.org/protobuf/proto"
)

func TestFormalGenesisAlignmentUsedUnionControlsShareRequirement(t *testing.T) {
	f := newGenesisImportFixtureWithFormat(t, 3, ceremony.PlaintextGenesisChainID, true)
	used := make(map[string]bool)
	for _, participant := range f.manifest.Plan.Participants {
		used[participant.ID] = true
	}
	for _, identity := range f.networkParticipants {
		err := runGenesisPreflight(f, identity.ID, f.store)
		if used[identity.ID] && err == nil {
			t.Fatal("selected signer started without its signed genesis share")
		}
		if !used[identity.ID] && err != nil {
			t.Fatalf("unused network validator required a share: %v", err)
		}
		if !used[identity.ID] {
			if err := importFrostGenesisSharesFromDirWithFormat(f.directory, identity.ID, f.chainID, f.peerDigest, f.config, f.reader, f.store, f.store, nil, true); err == nil {
				t.Fatal("unused network validator imported another signer's bundle")
			}
		}
	}
	if f.store.saves != 0 || f.store.manifestWrites != 0 || len(f.store.shares) != 0 {
		t.Fatal("preflight or nonparticipant rejection persisted an artifact")
	}
}

func TestFormalGenesisAlignmentVerifiedCommitteeProjection(t *testing.T) {
	                                                                         
	                                                                           
	                                                                           
	const name = "eth"
	addresses := []string{"a", "b", "c", "d", "e"}
	top := configuredBootstrapTop(addresses...)
	verified := []string{top.Addresses[3], top.Addresses[2], top.Addresses[1], top.Addresses[0]}
	chainConfig := configuredBootstrapChain(name)
	frost := testDualCustodyFrostConfig()
	frost.Chains = map[string]config.ChainFrostConfig{name: chainConfig}
	cfg := buildVaultConfigs(frost, len(addresses))[name]
	sv, _ := newMapBackedStateView()
	raw, err := proto.Marshal(top)
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyFrostActiveMiners(), raw)
	if err := applyChainFrostBootstrap(sv, name, chainConfig, cfg, 1, 20, 5, 5, 5, [][]string{verified}); err != nil {
		t.Fatal(err)
	}
	raw, found, err := sv.Get(keys.KeyFrostVaultState(name, 0))
	if err != nil || !found {
		t.Fatal("missing active committee projection")
	}
	var state pb.FrostVaultState
	if err := proto.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	for i, member := range verified {
		if state.CommitteeMembers[i] != member {
			t.Fatal("authenticated committee order was replaced by a locally rederived order")
		}
	}
}

func TestFormalGenesisAlignmentFrozenRotatingProofCannotReinterpret(t *testing.T) {
	f := newGenesisImportFixtureWithAssignment(t, 3, ceremony.PlaintextGenesisChainID, true, true)
	proof, err := ceremony.StrictDecodeGenesisDeploymentManifest(f.config.Frost.GenesisDeploymentManifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := ceremony.ValidateManifest(f.manifest, utils.VerifyECDSASignature); err != nil {
		t.Fatal("historical negative control is not correctly signed")
	}
	if err := ceremony.ValidateGenesisDeploymentManifest(proof, f.config.Frost, f.manifest, nil, f.chainID, f.peerDigest, utils.VerifyECDSASignature); err != nil {
		t.Fatalf("historical frozen proof must verify under its actual five-party plan: %v", err)
	}
	validators := make([]ceremony.GenesisTrustValidator, 0, len(f.networkParticipants))
	for _, participant := range f.networkParticipants {
		validators = append(validators, ceremony.GenesisTrustValidator{Address: participant.ID, PublicKey: participant.PublicKey, IsMiner: true})
	}
	freshParticipants, freshDigests, err := ceremony.ExpectedGenesisDeploymentTrust(validators, f.config.Frost)
	if err != nil {
		t.Fatal(err)
	}
	if len(freshParticipants) < 4 || len(freshParticipants) > 5 || len(freshDigests) != len(proof.Statement.Vaults) {
		t.Fatal("fresh trust has an invalid union or vault set")
	}
	before := append([]byte(nil), f.config.Frost.GenesisDeploymentManifest...)
	peers := make([]PeerInfo, len(f.networkParticipants))
	for i, participant := range f.networkParticipants {
		peers[i] = PeerInfo{Address: participant.ID, SecpPublicKey: participant.PublicKey, IsMiner: true}
	}
	differs := false
	for i, digest := range freshDigests {
		differs = differs || !bytes.Equal(digest, proof.Statement.Vaults[i].CommitteeDigest)
	}
	if !differs {
		t.Fatal("historical fixture accidentally equals fresh per-chain assignment")
	}
	if _, err := validateFrostGenesisDeploymentProofMode(peers, f.config.Frost, f.chainID, f.peerDigest, false); err == nil {
		t.Fatal("old rotating committee was silently reinterpreted as fresh hash assignment")
	}
	if !bytes.Equal(before, f.config.Frost.GenesisDeploymentManifest) {
		t.Fatal("verification rewrote immutable old proof bytes")
	}
}
