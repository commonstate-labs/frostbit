package main

import (
	"bytes"
	"crypto/sha256"
	"dex/protocol"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"dex/config"
	"dex/db"
	"dex/frost/ceremony"
	"dex/frost/core/curve"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"dex/utils"
	"dex/vm"
	"github.com/btcsuite/btcd/btcec/v2"
	"google.golang.org/protobuf/proto"
)

func attachBootstrapMarkerProof(t *testing.T, cfg *config.FrostConfig) string {
	t.Helper()
	chainCfg := cfg.Chains["btc"]
	groups, err := config.DecodeInitialFrostGroupPubkeys("btc", chainCfg)
	if err != nil || len(groups) != 1 {
		t.Fatalf("decode test group key: %v", err)
	}
	digest := bytes.Repeat([]byte{0x42}, 32)
	configuredDigest, err := ceremony.ComputeConfiguredFrostConfigDigest(*cfg)
	if err != nil {
		t.Fatal(err)
	}
	statement := ceremony.GenesisDeploymentStatement{
		Version: ceremony.GenesisDeploymentVersion, CeremonyManifestDigest: digest,
		PlanDigest: digest, NetworkChainID: 1, PeerManifestDigest: digest, Epoch: 1,
		ConfiguredFrostDigest: configuredDigest, Participants: []string{"bootstrap-test"},
		Vaults: []ceremony.GenesisDeploymentVault{{
			Chain: "btc", VaultID: 0, Epoch: 1,
			SignAlgo:        pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
			CommitteeDigest: digest, RawGroupPubkey: groups[0], GuardianPubkey: func() []byte { k, _ := cfg.GuardianPublicKey("btc"); return k }(), Kind: ceremony.GenesisDeploymentKindDerived,
		}},
	}
	statementDigest, err := ceremony.GenesisDeploymentStatementDigest(&statement)
	if err != nil {
		t.Fatal(err)
	}
	proofJSON, err := json.Marshal(ceremony.SignedGenesisDeploymentManifest{
		Version: ceremony.GenesisDeploymentVersion, Statement: statement, StatementDigest: statementDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg.GenesisDeploymentManifest = proofJSON
	marker, err := configuredFrostGenesisMarkerValue(*cfg)
	if err != nil {
		t.Fatal(err)
	}
	return marker
}

func configuredBootstrapChain(name string) config.ChainFrostConfig {
	cfg := config.ChainFrostConfig{VaultsPerChain: 1, AuthorityProtocol: 3, DepositProtocol: 2, DepositDexChainID: 771}
	if name == "btc" {
		cfg.AuthorityProtocol, cfg.DepositProtocol, cfg.DepositDexChainID = 0, 0, 0
		cfg.SignAlgo = "SCHNORR_SECP256K1_BIP340"
		cfg.BTCNetwork = "mainnet"
		group := curve.NewSecp256k1Group()
		cfg.InitialGroupPubkeys = []string{hex.EncodeToString(group.SerializePoint(group.ScalarBaseMult(big.NewInt(1))))}
	} else {
		cfg.SignAlgo = "SCHNORR_ALT_BN128"
		cfg.VaultRefs = []string{"0x1111111111111111111111111111111111111111"}
		cfg.ChainID = 8453
		if name == "trx" {
			cfg.VaultRefs = []string{"TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"}
			cfg.ChainID = 728126428
		}
		group := curve.NewBN256Group()
		cfg.InitialGroupPubkeys = []string{hex.EncodeToString(group.SerializePoint(group.ScalarBaseMult(big.NewInt(1))))}
	}
	return cfg
}

func configuredBootstrapTop(addresses ...string) *pb.FrostActiveMiners {
	top := &pb.FrostActiveMiners{
		Addresses:     append([]string(nil), addresses...),
		Indices:       make([]uint64, len(addresses)),
		PublicKeys:    make([][]byte, len(addresses)),
		EligibleCount: uint32(len(addresses)),
	}
	for i := range addresses {
		top.Indices[i] = uint64(i)
		top.PublicKeys[i] = []byte{byte(i + 1)}
		if addresses[i] != "" && !utils.IsDexAddress(addresses[i]) {
			seed := sha256.Sum256([]byte("configured-bootstrap-test/" + addresses[i]))
			key, _ := btcec.PrivKeyFromBytes(seed[:])
			top.PublicKeys[i] = key.PubKey().SerializeCompressed()
			top.Addresses[i], _ = utils.AddressFromSecp256k1PubKey(top.PublicKeys[i])
			key.Zero()
		}
	}
	return top
}

func configuredBootstrapVaultPolicy(chain string, signAlgo pb.SignAlgo) *pb.FrostVaultConfig {
	out := &pb.FrostVaultConfig{
		Chain:          chain,
		GuardianPubkey: func() []byte { c := testDualCustodyFrostConfig(); k, _ := c.GuardianPublicKey(chain); return k }(), AuthorityProtocol: 3, DepositProtocol: 2, DepositDexChainId: 771,
		SignAlgo:                      signAlgo,
		VaultCount:                    1,
		CommitteeSize:                 4,
		ThresholdRatio:                0.75,
		VaultMaturityBlocks:           30,
		CommitteeTermBlocks:           protocol.VaultCommitteeTermBlocks,
		CommitteeScaleMinerMultiplier: 1,
		TransitionConfirmBlocks:       10,
		CommitteeMaxSize:              100,
		TransitionTriggerRatio:        0.2,
		DkgCommitWindowBlocks:         5,
		DkgSharingWindowBlocks:        5,
		DkgDisputeWindowBlocks:        5,
	}
	if chain == "btc" {
		out.AuthorityProtocol, out.DepositProtocol, out.DepositDexChainId = 0, 0, 0
	}
	return out
}

func TestApplyChainFrostBootstrapConfiguredSixChains(t *testing.T) {
	for _, name := range []string{"btc", "eth", "bnb", "base", "arb", "trx"} {
		t.Run(name, func(t *testing.T) {
			sv, _ := newMapBackedStateView()
			top := configuredBootstrapTop("a", "b", "c", "d")
			raw, _ := proto.Marshal(top)
			sv.Set(keys.KeyFrostActiveMiners(), raw)
			frostCfg := testDualCustodyFrostConfig()
			chainCfg := configuredBootstrapChain(name)
			frostCfg.Chains = map[string]config.ChainFrostConfig{name: chainCfg}
			vaultCfg := buildVaultConfigs(frostCfg, len(top.Indices))[name]
			if err := applyChainFrostBootstrap(sv, name, chainCfg, vaultCfg, 1, 20, 5, 5, 5); err != nil {
				t.Fatal(err)
			}
			raw, exists, err := sv.Get(keys.KeyFrostVaultState(name, 0))
			if err != nil || !exists {
				t.Fatalf("configured state missing: %v", err)
			}
			var state pb.FrostVaultState
			if err := proto.Unmarshal(raw, &state); err != nil {
				t.Fatal(err)
			}
			wantPubkey, _ := hex.DecodeString(chainCfg.InitialGroupPubkeys[0])
			if state.Status != vm.VaultStatusActive || state.KeyEpoch != 1 || !bytes.Equal(state.GroupPubkey, wantPubkey) || len(state.CommitteeMembers) != 4 {
				t.Fatalf("unexpected configured state: %v", &state)
			}
			if _, exists, err := sv.Get(keys.KeyFrostVaultTransition(name, 0, 1)); err != nil || exists {
				t.Fatalf("configured genesis created initial DKG: exists=%v err=%v", exists, err)
			}
		})
	}
}

func TestApplyChainFrostBootstrapRejectsPartialBeforeStateChanges(t *testing.T) {
	for _, name := range []string{"eth", "bnb", "base", "arb", "trx"} {
		t.Run(name, func(t *testing.T) {
			sv, _ := newMapBackedStateView()
			chainCfg := configuredBootstrapChain(name)
			chainCfg.InitialGroupPubkeys = nil
			frostCfg := testDualCustodyFrostConfig()
			frostCfg.Chains = map[string]config.ChainFrostConfig{name: chainCfg}
			vaultCfg := buildVaultConfigs(frostCfg, 4)[name]
			if err := applyChainFrostBootstrap(sv, name, chainCfg, vaultCfg, 1, 20, 5, 5, 5); err == nil {
				t.Fatal("partial configured genesis started online DKG")
			}
			if len(sv.Diff()) != 0 {
				t.Fatal("invalid configuration changed bootstrap state")
			}
		})
	}
}

func TestApplyFrostBootstrapReturnsChainFailureWithoutFlushingOtherChains(t *testing.T) {
	manager, err := db.NewManager(filepath.Join(t.TempDir(), "db"), logs.NewNodeLogger("configured-bootstrap-failure", 0))
	if err != nil {
		t.Fatal(err)
	}
	manager.InitWriteQueue(128, time.Hour)
	t.Cleanup(manager.Close)
	frostCfg := testDualCustodyFrostConfig()
	valid := configuredBootstrapChain("btc")
	invalid := configuredBootstrapChain("eth")
	invalid.InitialGroupPubkeys = nil
	frostCfg.Chains = map[string]config.ChainFrostConfig{"btc": valid, "eth": invalid}
	top := configuredBootstrapTop("a", "b", "c", "d")
	err = applyFrostBootstrap(manager, frostCfg, top, buildVaultConfigs(frostCfg, 4))
	if err == nil || !strings.Contains(err.Error(), "chain eth") {
		t.Fatalf("chain failure was not propagated: %v", err)
	}
	if err := manager.ForceFlush(); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{keys.KeyFrostActiveMiners(), keys.KeyFrostVaultConfig("btc", 0), keys.KeyFrostVaultState("btc", 0)} {
		data, err := manager.Get(key)
		if err != nil && !isNotFoundError(err) {
			t.Fatal(err)
		}
		if len(data) != 0 {
			t.Fatalf("failed multichain bootstrap persisted %s", key)
		}
	}
}

func TestApplyFrostBootstrapPersistsConfiguredGenesisMarkerIdempotently(t *testing.T) {
	manager, err := db.NewManager(filepath.Join(t.TempDir(), "db"), logs.NewNodeLogger("configured-bootstrap-marker", 0))
	if err != nil {
		t.Fatal(err)
	}
	manager.InitWriteQueue(128, time.Hour)
	t.Cleanup(manager.Close)
	frostCfg := testDualCustodyFrostConfig()
	frostCfg.Chains = map[string]config.ChainFrostConfig{"btc": configuredBootstrapChain("btc")}
	expectedMarker := attachBootstrapMarkerProof(t, &frostCfg)
	top := configuredBootstrapTop("a", "b", "c", "d")
	for run := 0; run < 2; run++ {
		if err := applyFrostBootstrap(manager, frostCfg, top, buildVaultConfigs(frostCfg, 4)); err != nil {
			t.Fatalf("configured bootstrap run %d failed: %v", run+1, err)
		}
		marker, err := manager.Get(keys.KeyFrostConfiguredGenesisMarker())
		if err != nil || string(marker) != expectedMarker {
			t.Fatalf("configured bootstrap marker missing after run %d: %q, %v", run+1, marker, err)
		}
	}
}

func TestConfiguredFreshGenesisAlignsFiveNodeCommitteesAtHeightZero(t *testing.T) {
	fixture := newGenesisImportFixtureWithFormat(t, 3, ceremony.PlaintextGenesisChainID, true)
	frostCfg := fixture.config.Frost
	proof, err := ceremony.StrictDecodeGenesisDeploymentManifest(frostCfg.GenesisDeploymentManifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := ceremony.ValidateGenesisDeploymentManifest(
		proof, frostCfg, fixture.manifest, nil, fixture.chainID, fixture.peerDigest,
		utils.VerifyECDSASignature,
	); err != nil {
		t.Fatalf("fresh five-node deployment proof did not verify: %v", err)
	}
	manager, err := db.NewManager(filepath.Join(t.TempDir(), "db"), logs.NewNodeLogger("configured-five-node-alignment", 0))
	if err != nil {
		t.Fatal(err)
	}
	manager.InitWriteQueue(128, time.Hour)
	t.Cleanup(manager.Close)
	validators := make([]ceremony.GenesisTrustValidator, 0, 5)
	for _, participant := range fixture.networkParticipants {
		validators = append(validators, ceremony.GenesisTrustValidator{
			Address: participant.ID, PublicKey: participant.PublicKey, IsMiner: true,
		})
	}
	addresses := make([]string, len(validators))
	top := configuredBootstrapTop(make([]string, len(validators))...)
	for i, validator := range validators {
		addresses[i] = validator.Address
		top.Addresses[i] = validator.Address
		top.PublicKeys[i] = validator.PublicKey
	}
	participants, trustDigests, err := ceremony.ExpectedGenesisDeploymentTrust(validators, frostCfg)
	if err != nil || len(participants) != len(fixture.manifest.Plan.Participants) || len(trustDigests) != 6 {
		t.Fatalf("independent deployment trust disagrees with five-node profile: participants=%d vaults=%d err=%v", len(participants), len(trustDigests), err)
	}
	if err := applyFrostBootstrap(manager, frostCfg, top, buildVaultConfigs(frostCfg, len(addresses))); err != nil {
		t.Fatal(err)
	}
	participation := make(map[string]int, len(addresses))
	for ordinal, chainName := range ceremony.GenesisCeremonyChainOrder() {
		raw, err := manager.Get(keys.KeyFrostVaultState(chainName, 0))
		if err != nil {
			t.Fatal(err)
		}
		var state pb.FrostVaultState
		if err := proto.Unmarshal(raw, &state); err != nil {
			t.Fatal(err)
		}
		if state.Status != "ACTIVE" || state.KeyEpoch != 1 || state.CommitteeThreshold != 3 || len(state.CommitteeMembers) != 4 {
			t.Fatalf("%s unexpected configured-genesis policy: %+v", chainName, &state)
		}
		if state.ActiveSinceHeight != 1 || state.NextCommitteeReviewHeight != 1+protocol.VaultCommitteeTermBlocks {
			t.Fatalf("%s configured genesis must initialize the complete fixed term: activeSince=%d nextReview=%d", chainName, state.ActiveSinceHeight, state.NextCommitteeReviewHeight)
		}
		for offset, member := range state.CommitteeMembers {
			want := fixture.manifest.Vaults[ordinal].Committee[offset]
			if member != want {
				t.Fatalf("%s committee[%d]=%s want %s", chainName, offset, member, want)
			}
			participation[member]++
		}
		actualDigest, err := ceremony.CommitteeDigest(chainName, 0, 1, state.SignAlgo, state.CommitteeMembers)
		if err != nil || !bytes.Equal(actualDigest, trustDigests[ordinal]) ||
			!bytes.Equal(actualDigest, proof.Statement.Vaults[ordinal].CommitteeDigest) ||
			!reflect.DeepEqual(state.CommitteeMembers, fixture.manifest.Vaults[ordinal].Committee) ||
			!bytes.Equal(state.GroupPubkey, fixture.manifest.Vaults[ordinal].RawGroupPubkey) {
			t.Fatalf("%s height-0 state disagrees with signed manifest and independently verified proof: %v", chainName, err)
		}
		transition, err := manager.Get(keys.KeyFrostVaultTransition(chainName, 0, 1))
		if err != nil && !isNotFoundError(err) {
			t.Fatal(err)
		}
		if len(transition) != 0 {
			t.Fatalf("%s configured genesis unexpectedly started DKG", chainName)
		}
	}
	for _, address := range addresses {
		want := 0
		for _, vault := range fixture.manifest.Vaults {
			for _, member := range vault.Committee {
				if member == address {
					want++
				}
			}
		}
		if participation[address] != want {
			t.Fatalf("%s participates in %d vaults, want %d", address, participation[address], want)
		}
	}
	                                                                           
	wrong := proto.Clone(fixture.reader.states[keys.KeyFrostVaultState("base", 0)]).(*pb.FrostVaultState)
	wrong.CommitteeMembers = append([]string(nil), fixture.manifest.Vaults[1].Committee...)
	wrong.CommitteeMembers[0], wrong.CommitteeMembers[1] = wrong.CommitteeMembers[1], wrong.CommitteeMembers[0]
	if reflect.DeepEqual(wrong.CommitteeMembers, fixture.manifest.Vaults[1].Committee) {
		t.Fatal("negative control did not change committee order")
	}
	fixture.reader.states[keys.KeyFrostVaultState("base", 0)] = wrong
	if err := importFrostGenesisSharesFromDirWithFormat(
		fixture.directory, fixture.nodeID, fixture.chainID, fixture.peerDigest,
		fixture.config, fixture.reader, fixture.store, fixture.store, nil, true,
	); err == nil {
		t.Fatal("genesis importer accepted the obsolete chain-rotated committee")
	}
}

func TestApplyFrostBootstrapRejectsConfiguredGenesisMarkerConflicts(t *testing.T) {
	newManager := func(t *testing.T, name string) *db.Manager {
		t.Helper()
		manager, err := db.NewManager(filepath.Join(t.TempDir(), "db"), logs.NewNodeLogger(name, 0))
		if err != nil {
			t.Fatal(err)
		}
		manager.InitWriteQueue(128, time.Hour)
		t.Cleanup(manager.Close)
		return manager
	}
	top := configuredBootstrapTop("a", "b", "c", "d")
	configured := testDualCustodyFrostConfig()
	configured.Chains = map[string]config.ChainFrostConfig{"btc": configuredBootstrapChain("btc")}
	attachBootstrapMarkerProof(t, &configured)

	t.Run("invalid marker", func(t *testing.T) {
		manager := newManager(t, "configured-bootstrap-invalid-marker")
		manager.EnqueueSet(keys.KeyFrostConfiguredGenesisMarker(), "attacker-controlled")
		if err := manager.ForceFlush(); err != nil {
			t.Fatal(err)
		}
		if err := applyFrostBootstrap(manager, configured, top, buildVaultConfigs(configured, 4)); err == nil {
			t.Fatal("invalid configured-genesis marker was accepted")
		}
		if _, err := manager.Get(keys.KeyFrostActiveMiners()); err == nil {
			t.Fatal("marker conflict partially persisted genesis state")
		}
	})

	t.Run("legacy downgrade", func(t *testing.T) {
		manager := newManager(t, "configured-bootstrap-legacy-downgrade")
		if err := applyFrostBootstrap(manager, configured, top, buildVaultConfigs(configured, 4)); err != nil {
			t.Fatal(err)
		}
		legacy := configured
		chainCfg := legacy.Chains["btc"]
		chainCfg.InitialGroupPubkeys = nil
		legacy.Chains = map[string]config.ChainFrostConfig{"btc": chainCfg}
		if err := applyFrostBootstrap(manager, legacy, top, buildVaultConfigs(legacy, 4)); err == nil {
			t.Fatal("persisted standalone-DKG marker was accepted with legacy public configuration")
		}
	})
}

func TestApplyFrostBootstrapLegacyModeDoesNotWriteConfiguredMarker(t *testing.T) {
	manager, err := db.NewManager(filepath.Join(t.TempDir(), "db"), logs.NewNodeLogger("legacy-bootstrap-marker", 0))
	if err != nil {
		t.Fatal(err)
	}
	manager.InitWriteQueue(128, time.Hour)
	t.Cleanup(manager.Close)
	frostCfg := testDualCustodyFrostConfig()
	chainCfg := configuredBootstrapChain("btc")
	chainCfg.InitialGroupPubkeys = nil
	frostCfg.Chains = map[string]config.ChainFrostConfig{"btc": chainCfg}
	top := configuredBootstrapTop("a", "b", "c", "d")
	if err := applyFrostBootstrap(manager, frostCfg, top, buildVaultConfigs(frostCfg, 4)); err != nil {
		t.Fatal(err)
	}
	if marker, err := manager.Get(keys.KeyFrostConfiguredGenesisMarker()); err == nil || len(marker) != 0 {
		t.Fatalf("legacy online-DKG bootstrap wrote configured-genesis marker: %q, %v", marker, err)
	}
}

func TestPersistedConfiguredGenesisMarkerRejectsRestartDowngrade(t *testing.T) {
	cfg := testDualCustodyFrostConfig()
	cfg.Enabled = true
	cfg.Chains = map[string]config.ChainFrostConfig{"btc": configuredBootstrapChain("btc")}
	marker := attachBootstrapMarkerProof(t, &cfg)
	reader := &genesisImportReader{values: map[string]string{keys.KeyFrostConfiguredGenesisMarker(): marker}}
	if present, err := validatePersistedFrostGenesisMarker(reader, cfg); err != nil || !present {
		t.Fatalf("matching configured restart rejected: present=%t err=%v", present, err)
	}
	disabled := cfg
	disabled.Enabled = false
	disabled.GenesisDeploymentManifest = nil
	if _, err := validatePersistedFrostGenesisMarker(reader, disabled); err == nil || !strings.Contains(err.Error(), "disabling") {
		t.Fatalf("configured database restarted with FROST disabled: %v", err)
	}
	legacy := cfg
	chainCfg := legacy.Chains["btc"]
	chainCfg.InitialGroupPubkeys = nil
	legacy.Chains = map[string]config.ChainFrostConfig{"btc": chainCfg}
	legacy.GenesisDeploymentManifest = nil
	if _, err := validatePersistedFrostGenesisMarker(reader, legacy); err == nil || !strings.Contains(err.Error(), "legacy") {
		t.Fatalf("configured database restarted in legacy mode: %v", err)
	}
}

func TestValidateFrostStartupMinerSetRejectsPartialConfiguredGenesis(t *testing.T) {
	cfg := testDualCustodyFrostConfig()
	chainCfg := configuredBootstrapChain("base")
	chainCfg.VaultRefs = nil
	cfg.Chains = map[string]config.ChainFrostConfig{"base": chainCfg}
	peers := []PeerInfo{{IsMiner: true}, {IsMiner: true}, {IsMiner: true}, {IsMiner: true}}
	if err := validateFrostStartupMinerSet(peers, cfg); err == nil || !strings.Contains(err.Error(), "vaultRefs count mismatch") {
		t.Fatalf("startup accepted a half-configured Base vault: %v", err)
	}
}

func TestValidateFrostStartupMinerSetRejectsCrossChainPartialCeremony(t *testing.T) {
	cfg := testDualCustodyFrostConfig()
	cfg.Chains = map[string]config.ChainFrostConfig{
		"btc": configuredBootstrapChain("btc"),
		"eth": {
			SignAlgo: "SCHNORR_ALT_BN128", FrostVariant: "frost-bn128",
			VaultsPerChain: 1, ChainID: 1,
		},
	}
	peers := []PeerInfo{{IsMiner: true}, {IsMiner: true}, {IsMiner: true}, {IsMiner: true}}
	err := validateFrostStartupMinerSet(peers, cfg)
	if err == nil || !strings.Contains(err.Error(), "all-or-none") || !strings.Contains(err.Error(), "eth") {
		t.Fatalf("startup accepted cross-chain partial ceremony results: %v", err)
	}
}
