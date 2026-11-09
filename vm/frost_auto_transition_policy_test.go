package vm

import (
	"dex/config"
	"dex/keys"
	"dex/liveness"
	"dex/pb"
	"dex/protocol"
	"fmt"
	"slices"
	"testing"

	"google.golang.org/protobuf/proto"
)

func TestProcessFrostAutoTransitionsExpansionWaitsForFixedTerm(t *testing.T) {
	current := frostAutoPolicyMembers("current", 4)
	target := append(append([]string(nil), current...), frostAutoPolicyMembers("new", 4)...)
	top := append(append([]string(nil), target...), frostAutoPolicyMembers("reserve", 8)...)
	executor, sv, cfg, vault, snapshot := seedFrostAutoTransitionPolicy(t, current, top, 16, 2, 100)
	key := keys.KeyFrostVaultTransition(cfg.Chain, 0, 2)
	for _, height := range []uint64{50, vault.NextCommitteeReviewHeight - 1} {
		executor.processFrostAutoTransitions(&pb.Block{Header: &pb.BlockHeader{Height: height}}, snapshot, sv)
		frostAutoPolicyRequireNoTransition(t, sv, key)
	}
	height := vault.NextCommitteeReviewHeight
	executor.processFrostAutoTransitions(&pb.Block{Header: &pb.BlockHeader{Height: height}}, snapshot, sv)
	transition := mustLoadTransition(t, sv, key)
	if transition.TriggerHeight != height+1 || transition.DkgN != 8 || transition.DkgThresholdT != 6 || !slices.Equal(transition.NewCommitteeMembers, target) {
		t.Fatalf("term expansion mismatch: %+v", transition)
	}
	if transition.DkgStatus != DKGStatusNotStarted || transition.DkgAttempt != 1 {
		t.Fatalf("invalid first attempt: %+v", transition)
	}
}

func TestProcessFrostAutoTransitionsReplacementStartsAtTermBoundary(t *testing.T) {
	current := frostAutoPolicyMembers("member", 20)
	top := append(append([]string(nil), current[4:]...), frostAutoPolicyMembers("replacement", 4)...)
	executor, sv, cfg, vault, snapshot := seedFrostAutoTransitionPolicy(t, current, top, 20, 1, 100)
	key := keys.KeyFrostVaultTransition(cfg.Chain, 0, 2)
	executor.processFrostAutoTransitions(&pb.Block{Header: &pb.BlockHeader{Height: vault.NextCommitteeReviewHeight - 1}}, snapshot, sv)
	frostAutoPolicyRequireNoTransition(t, sv, key)
	executor.processFrostAutoTransitions(&pb.Block{Header: &pb.BlockHeader{Height: vault.NextCommitteeReviewHeight}}, snapshot, sv)
	transition := mustLoadTransition(t, sv, key)
	if transition.TriggerHeight != vault.NextCommitteeReviewHeight+1 || transition.DkgN != 20 || transition.DkgThresholdT != 15 || !slices.Equal(transition.NewCommitteeMembers, top) {
		t.Fatalf("boundary replacement mismatch: %+v", transition)
	}
}

func TestProcessFrostAutoTransitionsSingleReplacementAtTermBoundary(t *testing.T) {
	current := frostAutoPolicyMembers("member", 20)
	top := append(append([]string(nil), current[1:]...), "replacement")
	executor, sv, cfg, vault, snapshot := seedFrostAutoTransitionPolicy(t, current, top, 20, 1, 100)
	vault.PendingCommitteeDigest = []byte("obsolete-debounce")
	vault.PendingCommitteeSinceHeight = 90
	frostAutoPolicyStoreProto(t, sv, keys.KeyFrostVaultState(cfg.Chain, 0), vault)
	executor.processFrostAutoTransitions(&pb.Block{Header: &pb.BlockHeader{Height: vault.NextCommitteeReviewHeight}}, snapshot, sv)
	transition := mustLoadTransition(t, sv, keys.KeyFrostVaultTransition(cfg.Chain, 0, 2))
	if !slices.Equal(transition.NewCommitteeMembers, top) {
		t.Fatalf("single replacement suppressed by old 20%% rule: %+v", transition)
	}
	stored := frostAutoPolicyLoadVault(t, sv, cfg.Chain, 0)
	if len(stored.PendingCommitteeDigest) != 0 || stored.PendingCommitteeSinceHeight != 0 {
		t.Fatalf("obsolete confirmation state retained: %+v", stored)
	}
}

func TestProcessFrostAutoTransitionsPopulationDeclineNeverShrinksCommittee(t *testing.T) {
	current := frostAutoPolicyMembers("member", 8)
	executor, sv, cfg, _, snapshot := seedFrostAutoTransitionPolicy(
		t, current, current, 8, 2, 3,
	)

	target, err := targetVaultCommitteeSize(snapshot, cfg, len(current))
	if err != nil {
		t.Fatalf("derive target committee size: %v", err)
	}
	if target != len(current) {
		t.Fatalf("population decline target K=%d, want current K=%d", target, len(current))
	}

	executor.processFrostAutoTransitions(
		&pb.Block{Header: &pb.BlockHeader{Height: 100}}, snapshot, sv,
	)
	frostAutoPolicyRequireNoTransition(t, sv, keys.KeyFrostVaultTransition(cfg.Chain, 0, 2))

	stored := frostAutoPolicyLoadVault(t, sv, cfg.Chain, 0)
	if got := len(stored.CommitteeMembers); got != 8 {
		t.Fatalf("population decline changed current K=%d, want 8", got)
	}
	if stored.CommitteeThreshold != 6 {
		t.Fatalf("population decline changed threshold=%d, want 6", stored.CommitteeThreshold)
	}
}

func TestBuildAutoTransitionStateUsesExactThreeQuarterThreshold(t *testing.T) {
	for _, committeeSize := range []int{4, 8, 20, 32, 64, 100} {
		t.Run(fmt.Sprintf("K%d", committeeSize), func(t *testing.T) {
			committee := frostAutoPolicyMembers("member", committeeSize)
			cfg := testVaultCommitteePolicyConfig("eth", uint32(committeeSize))
			transition, err := buildAutoTransitionState(
				"eth", 0, 2, 1,
				pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128, 101,
				committee, committee, []byte{1},
				cfg,
			)
			if err != nil {
				t.Fatalf("build K=%d transition: %v", committeeSize, err)
			}
			if transition.DkgN != uint32(committeeSize) || transition.DkgThresholdT != uint32(committeeSize*3/4) {
				t.Fatalf("K=%d produced (n,t)=(%d,%d), want (%d,%d)",
					committeeSize, transition.DkgN, transition.DkgThresholdT, committeeSize, committeeSize*3/4)
			}
		})
	}
}

func TestFrostCandidateSnapshotValidation(t *testing.T) {
	valid := testRankedFrostSnapshot([]string{"a", "b"}, 10)
	if err := validateFrostCandidateSnapshot(valid); err != nil {
		t.Fatalf("valid snapshot rejected: %v", err)
	}
	emptyRuntime := testRankedFrostSnapshot(nil, 10)
	if err := validateFrostCandidateSnapshot(emptyRuntime); err != nil {
		t.Fatalf("valid empty runtime snapshot rejected: %v", err)
	}

	tests := map[string]*pb.FrostActiveMiners{
		"empty-genesis": {},
		"oversized": {
			Addresses: frostAutoPolicyMembers("miner", config.VaultCandidateSnapshotLimit+1),
		},
		"parallel-length": {
			Addresses: []string{"a", "b"}, Indices: []uint64{1},
		},
		"duplicate-address": {
			Addresses: []string{"a", "a"},
		},
		"duplicate-index": {
			Addresses: []string{"a", "b"}, Indices: []uint64{1, 1},
		},
		"eligible-count-smaller-than-prefix": {
			Addresses: []string{"a", "b"}, EligibleCount: 1,
		},
		"zero-matured-stake": {
			Addresses: []string{"a"}, MaturedStakes: []uint64{0},
		},
	}
	for name, snapshot := range tests {
		t.Run(name, func(t *testing.T) {
			if err := validateFrostCandidateSnapshot(snapshot); err == nil {
				t.Fatal("invalid snapshot was accepted")
			}
		})
	}
}

func TestK100CandidateReserveSurvivesThreeFullFaultBudgets(t *testing.T) {
	addresses := frostAutoPolicyMembers("candidate", config.VaultCandidateSnapshotLimit)
	top := testRankedFrostSnapshot(addresses, 10)
	excluded := make(map[string]bool, 75)
	for i := 0; i < 75; i++ {
		excluded[addresses[i]] = true
	}
	committee, err := expectedTransitionCommitteeExcluding(top, &pb.FrostVaultConfig{CommitteeSize: 100}, excluded)
	if err != nil {
		t.Fatalf("select K100 after three 25-member fault budgets: %v", err)
	}
	if len(committee) != 100 || committee[0] != addresses[75] || committee[99] != addresses[174] {
		t.Fatalf("unexpected reserve committee: len=%d first=%q last=%q", len(committee), committee[0], committee[len(committee)-1])
	}
	transition, err := buildAutoTransitionState(
		"eth", 0, 2, 4, pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128, 10,
		addresses[:100], committee, []byte{1}, testVaultCommitteePolicyConfig("eth", 100),
	)
	if err != nil {
		t.Fatal(err)
	}
	if transition.DkgN != 100 || transition.DkgThresholdT != 75 {
		t.Fatalf("retry quorum drifted: n=%d t=%d", transition.DkgN, transition.DkgThresholdT)
	}
}

func TestCandidateShortageKeepsExpiredTermUntilCandidatesReturn(t *testing.T) {
	current := frostAutoPolicyMembers("member", 20)
	target := append(append([]string(nil), current[4:]...), frostAutoPolicyMembers("replacement", 4)...)
	executor, sv, cfg, vault, snapshot := seedFrostAutoTransitionPolicy(t, current, target, 20, 1, 3)
	key := keys.KeyFrostVaultTransition(cfg.Chain, 0, 2)
	height := vault.NextCommitteeReviewHeight
	shortage := testRankedFrostSnapshot(target[:19], height)
	executor.processFrostAutoTransitions(&pb.Block{Header: &pb.BlockHeader{Height: height}}, shortage, sv)
	frostAutoPolicyRequireNoTransition(t, sv, key)
	stored := frostAutoPolicyLoadVault(t, sv, cfg.Chain, 0)
	if stored.NextCommitteeReviewHeight != height || !slices.Equal(stored.CommitteeMembers, current) || stored.KeyEpoch != vault.KeyEpoch {
		t.Fatalf("shortage advanced term or changed serving key: %+v", stored)
	}
	executor.processFrostAutoTransitions(&pb.Block{Header: &pb.BlockHeader{Height: height + 1}}, snapshot, sv)
	if got := mustLoadTransition(t, sv, key); got.TriggerHeight != height+2 {
		t.Fatalf("restored candidates were delayed by obsolete confirmation: %+v", got)
	}
}

func TestProcessLivenessHooksUsesOnChainVaultMaturity(t *testing.T) {
	livenessCfg := liveness.DefaultConfig()
	livenessCfg.MinStake = 1
	tracker := liveness.NewTrackerWithVaultMaturity(livenessCfg, 1)
	executor := &Executor{LivenessTracker: tracker}
	sv := NewMockStateView()
	cfg := testVaultCommitteePolicyConfig("eth", 4)
	cfg.VaultMaturityBlocks = 5
	frostAutoPolicyStoreProto(t, sv, keys.KeyFrostVaultConfig("eth", 0), cfg)

	if _, _, err := executor.processLivenessHooks(&pb.Block{Header: &pb.BlockHeader{Height: 1}}, sv); err != nil {
		t.Fatalf("apply on-chain maturity: %v", err)
	}
	if _, err := tracker.RegisterMinerAtHeight(sv, "candidate", 100, 2); err != nil {
		t.Fatal(err)
	}
	snapshot := tracker.GetMinerSnapshot("candidate")
	if snapshot == nil || snapshot.PendingStake != 100 || snapshot.PendingStakeMatureHeight != 7 || snapshot.MaturedStake != 0 {
		t.Fatalf("on-chain maturity did not override local value: %+v", snapshot)
	}
}

func TestProcessLivenessHooksRejectsAmbiguousOnChainVaultMaturityBeforeMutation(t *testing.T) {
	for name, configs := range map[string][]*pb.FrostVaultConfig{
		"zero": {
			func() *pb.FrostVaultConfig {
				cfg := testVaultCommitteePolicyConfig("eth", 4)
				cfg.VaultMaturityBlocks = 0
				return cfg
			}(),
		},
		"mismatch": {
			testVaultCommitteePolicyConfig("eth", 4),
			func() *pb.FrostVaultConfig {
				cfg := testVaultCommitteePolicyConfig("btc", 4)
				cfg.VaultMaturityBlocks = 31
				return cfg
			}(),
		},
	} {
		t.Run(name, func(t *testing.T) {
			tracker := liveness.NewTrackerWithVaultMaturity(liveness.DefaultConfig(), 1)
			executor := &Executor{LivenessTracker: tracker}
			sv := NewMockStateView()
			for _, cfg := range configs {
				frostAutoPolicyStoreProto(t, sv, keys.KeyFrostVaultConfig(cfg.Chain, 0), cfg)
			}
			if _, _, err := executor.processLivenessHooks(&pb.Block{Header: &pb.BlockHeader{Height: 1}}, sv); err == nil {
				t.Fatal("ambiguous on-chain maturity was accepted")
			}
			if _, exists, err := sv.Get(keys.KeyLivenessGlobal()); err != nil || exists {
				t.Fatalf("liveness mutated before policy rejection: exists=%t err=%v", exists, err)
			}
		})
	}
}

func seedFrostAutoTransitionPolicy(
	t *testing.T,
	currentCommittee, topAddresses []string,
	eligibleCount, multiplier uint32,
	confirmBlocks uint64,
) (*Executor, *MockStateView, *pb.FrostVaultConfig, *pb.FrostVaultState, *pb.FrostActiveMiners) {
	t.Helper()

	const chain = "eth"
	cfg := &pb.FrostVaultConfig{
		Chain:                         chain,
		SignAlgo:                      pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128,
		VaultCount:                    1,
		CommitteeSize:                 4,
		ThresholdRatio:                0.75,
		VaultMaturityBlocks:           30,
		CommitteeTermBlocks:           protocol.VaultCommitteeTermBlocks,
		CommitteeScaleMinerMultiplier: multiplier,
		TransitionConfirmBlocks:       confirmBlocks,
		CommitteeMaxSize:              100,
		TransitionTriggerRatio:        0.2,
		DkgCommitWindowBlocks:         10,
		DkgSharingWindowBlocks:        10,
		DkgDisputeWindowBlocks:        10,
	}
	vault := &pb.FrostVaultState{
		Chain:                     chain,
		VaultId:                   0,
		KeyEpoch:                  1,
		GroupPubkey:               []byte{1},
		SignAlgo:                  cfg.SignAlgo,
		Status:                    VaultStatusActive,
		CommitteeMembers:          append([]string(nil), currentCommittee...),
		CommitteeThreshold:        uint32(len(currentCommittee) * 3 / 4),
		ActiveSinceHeight:         1,
		NextCommitteeReviewHeight: 1 + protocol.VaultCommitteeTermBlocks,
	}
	if eligibleCount != uint32(len(topAddresses)) {
		t.Fatalf("test fixture must persist every untruncated eligible candidate: addresses=%d eligible=%d", len(topAddresses), eligibleCount)
	}
	top := testRankedFrostSnapshot(topAddresses, 99)

	sv := NewMockStateView()
	frostAutoPolicyStoreProto(t, sv, keys.KeyFrostVaultConfig(chain, 0), cfg)
	frostAutoPolicyStoreProto(t, sv, keys.KeyFrostVaultState(chain, 0), vault)
	frostAutoPolicyStoreProto(t, sv, keys.KeyFrostActiveMiners(), top)

	executor := &Executor{DB: &initializedDkgDBManager{}}
	return executor, sv, cfg, vault, top
}

func frostAutoPolicyMembers(prefix string, count int) []string {
	members := make([]string, count)
	for i := range members {
		members[i] = fmt.Sprintf("%s-%03d", prefix, i)
	}
	return members
}

func frostAutoPolicyStoreProto(t *testing.T, sv StateView, key string, value proto.Message) {
	t.Helper()
	data, err := MarshalProto(value)
	if err != nil {
		t.Fatalf("marshal %s: %v", key, err)
	}
	sv.Set(key, data)
}

func frostAutoPolicyLoadVault(t *testing.T, sv StateView, chain string, vaultID uint32) *pb.FrostVaultState {
	t.Helper()
	raw, exists, err := sv.Get(keys.KeyFrostVaultState(chain, vaultID))
	if err != nil {
		t.Fatalf("read vault state: %v", err)
	}
	if !exists || len(raw) == 0 {
		t.Fatal("vault state missing")
	}
	var vault pb.FrostVaultState
	if err := proto.Unmarshal(raw, &vault); err != nil {
		t.Fatalf("unmarshal vault state: %v", err)
	}
	return &vault
}

func frostAutoPolicyRequireNoTransition(t *testing.T, sv StateView, key string) {
	t.Helper()
	raw, exists, err := sv.Get(key)
	if err != nil {
		t.Fatalf("read transition: %v", err)
	}
	if exists || len(raw) != 0 {
		t.Fatalf("unexpected transition at %s", key)
	}
}
