package certchain

import (
	"bytes"
	"fmt"
	"testing"

	"dex/interfaces"
	"dex/pb"
	"dex/utils"
)

func committeeTestSet(t *testing.T, count int) map[uint32]*MinerSnapshot {
	t.Helper()
	keys := makeMiners(t, count)
	set := make(map[uint32]*MinerSnapshot, count)
	for i := 0; i < count; i++ {
		pub, err := utils.SerializePublicKey(keys[i].pub)
		if err != nil {
			t.Fatal(err)
		}
		idx := uint32(i)
		set[idx] = &MinerSnapshot{
			Index:           idx,
			Address:         fmt.Sprintf("miner-%03d", i),
			PubKey:          append([]byte(nil), pub...),
			EffectiveWeight: uint64(10_000 - i),
		}
	}
	return set
}

func TestSelectTopStakeCommitteeUsesAllBelowCap(t *testing.T) {
	set := committeeTestSet(t, 5)
	committee, _, err := SelectTopStakeCommittee(set)
	if err != nil {
		t.Fatal(err)
	}
	if len(committee) != 5 {
		t.Fatalf("committee size=%d want 5", len(committee))
	}
	if got := quorumThresholdForMinerCount(len(committee), 0.667); got != 4 {
		t.Fatalf("5-member quorum=%d want 4", got)
	}
}

func TestSelectTopStakeCommitteeCapsAt100(t *testing.T) {
	set := committeeTestSet(t, 120)
	committee, _, err := SelectTopStakeCommittee(set)
	if err != nil {
		t.Fatal(err)
	}
	if len(committee) != 100 {
		t.Fatalf("committee size=%d want 100", len(committee))
	}
	for index := uint32(0); index < 100; index++ {
		if committee[index] == nil {
			t.Fatalf("top-stake miner %d missing", index)
		}
	}
	for index := uint32(100); index < 120; index++ {
		if committee[index] != nil {
			t.Fatalf("lower-stake miner %d unexpectedly selected", index)
		}
	}
	if got := quorumThresholdForMinerCount(len(committee), 0.667); got != 67 {
		t.Fatalf("100-member quorum=%d want 67", got)
	}
}

func TestTrackerRequestsSignaturesOnlyFromTop100Committee(t *testing.T) {
	set := committeeTestSet(t, 120)
	bootstrap := make([]MinerSnapshot, 0, len(set))
	active := make([]interfaces.CertMinerSnapshot, 0, len(set))
	for index := uint32(0); index < 120; index++ {
		miner := set[index]
		bootstrap = append(bootstrap, *miner)
		active = append(active, interfaces.CertMinerSnapshot{
			Index:           miner.Index,
			Address:         miner.Address,
			PublicKey:       append([]byte(nil), miner.PubKey...),
			EffectiveWeight: miner.EffectiveWeight,
		})
	}
	tracker := NewTracker(Config{CheckpointInterval: 5, Quorum: 0.667})
	if err := tracker.UpdateMinerSet(bootstrap); err != nil {
		t.Fatal(err)
	}
	tracker.OnCheckpointExecuted(5, []byte("block-5"), []byte("state-root-5"), active)

	if _, _, ok := tracker.GetPendingMinerByAddress(5, set[99].Address); !ok {
		t.Fatal("rank-100 Active miner was not selected")
	}
	if _, _, ok := tracker.GetPendingMinerByAddress(5, set[100].Address); ok {
		t.Fatal("rank-101 Active miner was allowed to sign")
	}
	tracker.mu.RLock()
	defer tracker.mu.RUnlock()
	if tracker.pending == nil || tracker.pending.MinerCount != 120 || tracker.pending.CommitteeSize != 100 {
		t.Fatalf("unexpected pending metadata: %+v", tracker.pending)
	}
}

func TestSelectTopStakeCommitteeTreatsEachHotAsIndependent(t *testing.T) {
	set := committeeTestSet(t, 3)
	set[0].EffectiveWeight = 500
	set[1].EffectiveWeight = 500
	set[2].EffectiveWeight = 400
	committee, _, err := SelectTopStakeCommittee(set)
	if err != nil {
		t.Fatal(err)
	}
	if len(committee) != 3 || committee[0] == nil || committee[1] == nil || committee[2] == nil {
		t.Fatalf("unexpected independent-hot committee: %#v", committee)
	}
}

func TestSelectTopStakeCommitteeValidatesOnlySelectedKeys(t *testing.T) {
	set := committeeTestSet(t, 101)
	set[100].PubKey = []byte("invalid-unselected-key")
	if _, _, err := SelectTopStakeCommittee(set); err != nil {
		t.Fatalf("unselected key should not require repeated curve validation: %v", err)
	}
	set[100].EffectiveWeight = 1_000_000
	if _, _, err := SelectTopStakeCommittee(set); err == nil {
		t.Fatal("selected invalid BLS key was accepted")
	}
}

func TestTrackerRejectsInvalidChangedActiveKey(t *testing.T) {
	set := committeeTestSet(t, 1)
	tracker := NewTracker(Config{CheckpointInterval: 5, Quorum: 0.667})
	if err := tracker.UpdateMinerSet([]MinerSnapshot{*set[0]}); err != nil {
		t.Fatal(err)
	}
	active := []interfaces.CertMinerSnapshot{
		{
			Index:           set[0].Index,
			Address:         set[0].Address,
			PublicKey:       append([]byte(nil), set[0].PubKey...),
			EffectiveWeight: set[0].EffectiveWeight,
		},
		{
			Index:           1,
			Address:         "invalid-miner",
			PublicKey:       []byte("invalid-key"),
			EffectiveWeight: 1,
		},
	}
	tracker.OnCheckpointExecuted(5, []byte("block-5"), []byte("state-root-5"), active)
	if _, ok := tracker.GetPendingCertDigest(5); ok {
		t.Fatal("checkpoint accepted an invalid changed Active BLS key")
	}
}

func TestValidatorSetRootBindsAddressAndWeight(t *testing.T) {
	set := committeeTestSet(t, 2)
	root, err := ValidatorSetRoot(pb.GetDefaultChainID(), set)
	if err != nil {
		t.Fatal(err)
	}
	changed, _ := cloneMinerSet(set)
	changed[0].EffectiveWeight++
	weightRoot, err := ValidatorSetRoot(pb.GetDefaultChainID(), changed)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(root, weightRoot) {
		t.Fatal("validator root did not bind effective weight")
	}
	changed, _ = cloneMinerSet(set)
	changed[0].Address = "different-hot"
	addressRoot, err := ValidatorSetRoot(pb.GetDefaultChainID(), changed)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(root, addressRoot) {
		t.Fatal("validator root did not bind hot address")
	}
}
