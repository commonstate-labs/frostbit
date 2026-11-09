package vm

import (
	"dex/keys"
	"dex/liveness"
	"dex/pb"
	"dex/rent"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
)

type countingMinerExitStateView struct {
	StateView
	scans map[string]int
}

func (s *countingMinerExitStateView) Scan(prefix string) (map[string][]byte, error) {
	s.scans[prefix]++
	return s.StateView.Scan(prefix)
}

func TestMinerExitBatchReadFailureKeepsTrackerAndStateUntouched(t *testing.T) {
	base := newMemStateView()
	hot := newMinerTestKey(t).GetAddress()
	reward := newMinerTestKey(t).GetAddress()
	seedMinerTestAccount(t, base, &pb.Account{Address: hot, RewardAddress: reward}, "7", "125")
	seedMinerTestAccount(t, base, &pb.Account{Address: reward}, "11", "0")
	exitRaw, err := proto.Marshal(&pb.MinerExitRecord{MinerAddress: hot, RewardAddress: reward, ExitHeight: 1})
	if err != nil {
		t.Fatal(err)
	}
	base.Set(keys.KeyMinerExitRecord(hot), exitRaw)

	cfg := liveness.DefaultConfig()
	cfg.FreezeBlocks = 1
	cfg.MinStake = 1
	tracker := liveness.NewTracker(cfg)
	if _, err := tracker.RegisterMinerAtHeight(base, hot, 1, 0); err != nil {
		t.Fatal(err)
	}
	if err := tracker.RemoveMiner(base, hot, 1); err != nil {
		t.Fatal(err)
	}

	beforeHot := GetBalance(base, hot, "FB")
	beforeReward := GetBalance(base, reward, "FB")
	sv := keyedReadErrorStateView{StateView: base, key: keys.KeyAccount(reward)}
	err = (&Executor{LivenessTracker: tracker}).processMinerExitUnlocks(
		&pb.Block{Header: &pb.BlockHeader{Height: 2}}, sv,
	)
	if err == nil {
		t.Fatal("reward account read failure must abort the batch")
	}
	if !strings.Contains(err.Error(), "injected state read failure") {
		t.Fatalf("unexpected batch error: %v", err)
	}
	if !tracker.IsRegistered(hot) {
		t.Fatal("tracker changed before the failing batch plan completed")
	}
	if _, exists, getErr := base.Get(keys.KeyMinerExitRecord(hot)); getErr != nil || !exists {
		t.Fatalf("exit record changed after failed plan: exists=%v err=%v", exists, getErr)
	}
	afterHot := GetBalance(base, hot, "FB")
	afterReward := GetBalance(base, reward, "FB")
	if afterHot.Balance != beforeHot.Balance || afterHot.MinerLockedBalance != beforeHot.MinerLockedBalance {
		t.Fatalf("hot balance changed after failed plan: before=%+v after=%+v", beforeHot, afterHot)
	}
	if afterReward.Balance != beforeReward.Balance || afterReward.MinerLockedBalance != beforeReward.MinerLockedBalance {
		t.Fatalf("reward balance changed after failed plan: before=%+v after=%+v", beforeReward, afterReward)
	}
}

func TestMinerExitBatchScansActiveTransitionsOnce(t *testing.T) {
	base := newMemStateView()
	for i := 0; i < 32; i++ {
		hot := newMinerTestKey(t).GetAddress()
		seedMinerTestAccount(t, base, &pb.Account{Address: hot, RewardAddress: hot}, "0", "1")
		exitRaw, err := proto.Marshal(&pb.MinerExitRecord{MinerAddress: hot, RewardAddress: hot, ExitHeight: 1})
		if err != nil {
			t.Fatal(err)
		}
		base.Set(keys.KeyMinerExitRecord(hot), exitRaw)
	}
	sv := &countingMinerExitStateView{StateView: base, scans: make(map[string]int)}
	if err := (&Executor{}).processMinerExitUnlocks(
		&pb.Block{Header: &pb.BlockHeader{Height: 1 + MinerExitFreezeBlocks}}, sv,
	); err != nil {
		t.Fatal(err)
	}
	if got := sv.scans[keys.KeyFrostVaultTransitionActivePrefix()]; got != 1 {
		t.Fatalf("active transition scans=%d want 1 for 32 exits", got)
	}
}

func TestMinerExitZeroPrincipalDoesNotCreateRewardAccount(t *testing.T) {
	sv := newMemStateView()
	hot := newMinerTestKey(t).GetAddress()
	reward := newMinerTestKey(t).GetAddress()
	seedMinerTestAccount(t, sv, &pb.Account{Address: hot, RewardAddress: reward}, "7", "0")
	exitRaw, err := proto.Marshal(&pb.MinerExitRecord{MinerAddress: hot, RewardAddress: reward, ExitHeight: 1})
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyMinerExitRecord(hot), exitRaw)
	if err := (&Executor{}).processMinerExitUnlocks(
		&pb.Block{Header: &pb.BlockHeader{Height: 1 + MinerExitFreezeBlocks}}, sv,
	); err != nil {
		t.Fatal(err)
	}
	if _, exists, err := sv.Get(keys.KeyAccount(reward)); err != nil {
		t.Fatal(err)
	} else if exists {
		t.Fatal("zero-principal exit created an empty reward account")
	}
	if _, exists, err := sv.Get(keys.KeyRentOwnerMarker(reward, rent.OwnerMarkerInit)); err != nil {
		t.Fatal(err)
	} else if exists {
		t.Fatal("zero-principal exit marked a missing reward account as initialized")
	}
}

func TestMinerExitCrossLinkedTransitionIndexFailsClosed(t *testing.T) {
	base := newMemStateView()
	hot := newMinerTestKey(t).GetAddress()
	reward := newMinerTestKey(t).GetAddress()
	seedMinerTestAccount(t, base, &pb.Account{Address: hot, RewardAddress: reward}, "7", "125")
	seedMinerTestAccount(t, base, &pb.Account{Address: reward}, "11", "0")
	exitRaw, err := proto.Marshal(&pb.MinerExitRecord{MinerAddress: hot, RewardAddress: reward, ExitHeight: 1})
	if err != nil {
		t.Fatal(err)
	}
	base.Set(keys.KeyMinerExitRecord(hot), exitRaw)

	transition := &pb.VaultTransitionState{
		Chain: "eth", VaultId: 2, EpochId: 3,
		Lifecycle: VaultLifecycleDraining, DkgStatus: DKGStatusNotStarted,
		OldCommitteeMembers: []string{"another-miner"},
	}
	transitionRaw, err := proto.Marshal(transition)
	if err != nil {
		t.Fatal(err)
	}
	targetKey := keys.KeyFrostVaultTransition(transition.Chain, transition.VaultId, transition.EpochId)
	base.Set(targetKey, transitionRaw)
	base.Set(keys.KeyFrostVaultTransitionActive("btc", 1, 2), []byte(targetKey))

	cfg := liveness.DefaultConfig()
	cfg.FreezeBlocks = 1
	cfg.MinStake = 1
	tracker := liveness.NewTracker(cfg)
	if _, err := tracker.RegisterMinerAtHeight(base, hot, 1, 0); err != nil {
		t.Fatal(err)
	}
	if err := tracker.RemoveMiner(base, hot, 1); err != nil {
		t.Fatal(err)
	}

	err = (&Executor{LivenessTracker: tracker}).processMinerExitUnlocks(
		&pb.Block{Header: &pb.BlockHeader{Height: 2}}, base,
	)
	if err == nil || !strings.Contains(err.Error(), "active vault transition index mismatch") {
		t.Fatalf("cross-linked active transition must fail closed, got %v", err)
	}
	if !tracker.IsRegistered(hot) {
		t.Fatal("cross-linked transition unregistered the miner")
	}
	if _, exists, getErr := base.Get(keys.KeyMinerExitRecord(hot)); getErr != nil || !exists {
		t.Fatalf("cross-linked transition consumed exit record: exists=%v err=%v", exists, getErr)
	}
	if got := GetBalance(base, hot, "FB"); got.Balance != "7" || got.MinerLockedBalance != "125" {
		t.Fatalf("cross-linked transition changed hot balance: %+v", got)
	}
}
