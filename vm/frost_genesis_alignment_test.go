package vm

import (
	"bytes"
	"dex/config"
	"dex/frost/ceremony"
	"dex/keys"
	"dex/pb"
	"dex/protocol"
	"slices"
	"testing"

	"google.golang.org/protobuf/proto"
)

const freshGenesisAlignmentActivationHeight = uint64(1)
const freshGenesisAlignmentFirstReview = freshGenesisAlignmentActivationHeight + protocol.VaultCommitteeTermBlocks

func freshGenesisAlignmentFixture(t *testing.T) (*Executor, *MockStateView, *pb.FrostActiveMiners, map[string]*pb.FrostVaultState) {
	t.Helper()
	cfg := config.DefaultFrostConfig()
	cfg.Vault.DefaultK = 4
	for name, c := range cfg.Chains {
		c.VaultsPerChain = 1
		cfg.Chains[name] = c
	}
	roster := canonicalGenesisAddressesForTest(t, 5)
	snapshot := testRankedFrostSnapshot(roster, 1)
	for i := range snapshot.MaturedStakes {
		snapshot.MaturedStakes[i] = protocol.MinMinerStakeRaw
		snapshot.ActiveSinceHeights[i] = 0
	}
	sv := NewMockStateView()
	original := make(map[string]*pb.FrostVaultState)
	for _, name := range ceremony.GenesisCeremonyChainOrder() {
		selected, err := ceremony.InitialCommitteeIndicesForChain(name, []uint64{0, 1, 2, 3, 4}, roster, cfg.Vault.DefaultK)
		if err != nil {
			t.Fatal(err)
		}
		members := make([]string, len(selected))
		for i, index := range selected {
			members[i] = roster[index]
		}
		policy := testVaultCommitteePolicyConfig(name, 4)
		policy.CommitteeScaleMinerMultiplier = 100
		policy.SignAlgo = pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128
		if name == "btc" {
			policy.SignAlgo = pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340
		}
		vault := &pb.FrostVaultState{Chain: name, KeyEpoch: 1, GroupPubkey: []byte{1}, SignAlgo: policy.SignAlgo,
			Status: VaultStatusActive, CommitteeMembers: members, CommitteeThreshold: 3,
			ActiveSinceHeight: freshGenesisAlignmentActivationHeight, NextCommitteeReviewHeight: freshGenesisAlignmentFirstReview}
		frostAutoPolicyStoreProto(t, sv, keys.KeyFrostVaultConfig(name, 0), policy)
		frostAutoPolicyStoreProto(t, sv, keys.KeyFrostVaultState(name, 0), vault)
		original[name] = proto.Clone(vault).(*pb.FrostVaultState)
	}
	return &Executor{DB: &initializedDkgDBManager{}}, sv, snapshot, original
}

func requireFreshGenesisServingGeneration(t *testing.T, sv StateView, original map[string]*pb.FrostVaultState, nextReview uint64) {
	t.Helper()
	for chain, before := range original {
		expected := proto.Clone(before).(*pb.FrostVaultState)
		expected.NextCommitteeReviewHeight = nextReview
		actual := frostAutoPolicyLoadVault(t, sv, chain, 0)
		if !proto.Equal(actual, expected) {
			t.Fatalf("%s serving generation changed: got=%+v want=%+v", chain, actual, expected)
		}
		frostAutoPolicyRequireNoTransition(t, sv, keys.KeyFrostVaultTransition(chain, 0, 2))
	}
}

                                                                             
                                                                          
func TestFreshGenesisChainHashMembershipIsFixedUntilTermBoundary(t *testing.T) {
	executor, sv, snapshot, original := freshGenesisAlignmentFixture(t)
	term := protocol.VaultCommitteeTermBlocks
	nextReview := freshGenesisAlignmentFirstReview
	for _, height := range []uint64{1, 200, freshGenesisAlignmentFirstReview - 1} {
		executor.processFrostAutoTransitions(&pb.Block{Header: &pb.BlockHeader{Height: height}}, snapshot, sv)
		if height >= nextReview {
			nextReview = height + term
		}
		requireFreshGenesisServingGeneration(t, sv, original, nextReview)
	}
}

func TestFreshGenesisSameMembersReorderedOnlyRenewTerm(t *testing.T) {
	for _, chain := range ceremony.GenesisCeremonyChainOrder() {
		t.Run(chain, func(t *testing.T) {
			executor, sv, initial, original := freshGenesisAlignmentFixture(t)
			before := original[chain]
			order := append([]string(nil), before.CommitteeMembers...)
			order[0], order[2] = order[2], order[0]
			for _, address := range initial.Addresses {
				if !slices.Contains(order, address) {
					order = append(order, address)
				}
			}
			snapshot := testRankedFrostSnapshot(order, freshGenesisAlignmentFirstReview-1)
			for i, address := range order {
				snapshot.Indices[i] = uint64(slices.Index(initial.Addresses, address))
				snapshot.ActiveSinceHeights[i] = 0
			}
			if err := validateFrostCandidateSnapshot(snapshot); err != nil {
				t.Fatal(err)
			}
			executor.processFrostAutoTransitions(&pb.Block{Header: &pb.BlockHeader{Height: freshGenesisAlignmentFirstReview}}, snapshot, sv)
			requireFreshGenesisServingGeneration(t, sv, map[string]*pb.FrostVaultState{chain: before}, freshGenesisAlignmentFirstReview+protocol.VaultCommitteeTermBlocks)
		})
	}
}

func TestFreshGenesisRealRankChangeWaitsUntilTermBoundary(t *testing.T) {
	executor, sv, initial, original := freshGenesisAlignmentFixture(t)
	target := testRankedFrostSnapshot([]string{initial.Addresses[4], initial.Addresses[0], initial.Addresses[1], initial.Addresses[2], initial.Addresses[3]}, 1)
	for i, index := range []uint64{4, 0, 1, 2, 3} {
		target.Indices[i] = index
		target.ActiveSinceHeights[i] = 0
	}
	if err := validateFrostCandidateSnapshot(target); err != nil {
		t.Fatal(err)
	}
	due := freshGenesisAlignmentFirstReview
	for _, height := range []uint64{1, 200, due - 1} {
		executor.processFrostAutoTransitions(&pb.Block{Header: &pb.BlockHeader{Height: height}}, target, sv)
		requireFreshGenesisServingGeneration(t, sv, original, due)
	}
	executor.processFrostAutoTransitions(&pb.Block{Header: &pb.BlockHeader{Height: due}}, target, sv)
	for chain, before := range original {
		if sameVaultCommitteeMembers(before.CommitteeMembers, target.Addresses[:4]) {
			requireFreshGenesisServingGeneration(t, sv, map[string]*pb.FrostVaultState{chain: before}, due+protocol.VaultCommitteeTermBlocks)
			continue
		}
		transition := mustLoadTransition(t, sv, keys.KeyFrostVaultTransition(chain, 0, 2))
		if transition.EpochId != 2 || transition.TriggerHeight != due+1 || transition.DkgN != 4 || transition.DkgThresholdT != 3 ||
			!slices.Equal(transition.OldCommitteeMembers, before.CommitteeMembers) || !slices.Equal(transition.NewCommitteeMembers, target.Addresses[:4]) {
			t.Fatalf("%s term transition lost frozen K/t/membership: %+v", chain, transition)
		}
		current := frostAutoPolicyLoadVault(t, sv, chain, 0)
		if current.KeyEpoch != 1 || current.ActiveSinceHeight != freshGenesisAlignmentActivationHeight || current.NextCommitteeReviewHeight != due ||
			!slices.Equal(current.CommitteeMembers, before.CommitteeMembers) || !bytes.Equal(current.GroupPubkey, before.GroupPubkey) || current.CommitteeThreshold != 3 {
			t.Fatalf("%s opened transition activated serving key prematurely: %+v", chain, current)
		}
	}
}
