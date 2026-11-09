package vm

import (
	"errors"
	"testing"

	"dex/keys"
	"dex/liveness"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

type formalMinerExitFaultView struct {
	StateView
	batchErrorKey string
	scanErrorKey  string
}

func (s formalMinerExitFaultView) GetMany(stateKeys []string) (map[string][]byte, error) {
	for _, stateKey := range stateKeys {
		if stateKey == s.batchErrorKey {
			return nil, errors.New("injected miner exit batch read failure")
		}
	}
	if getter, ok := s.StateView.(interface {
		GetMany([]string) (map[string][]byte, error)
	}); ok {
		return getter.GetMany(stateKeys)
	}
	return getStateMany(s.StateView, stateKeys)
}

func (s formalMinerExitFaultView) Scan(prefix string) (map[string][]byte, error) {
	if prefix == s.scanErrorKey {
		return nil, errors.New("injected miner exit activity scan failure")
	}
	return s.StateView.Scan(prefix)
}

func seedFormalMinerExit(t *testing.T, sv StateView, tracker *liveness.Tracker, hot, reward string, height uint64) {
	t.Helper()
	seedMinerTestAccount(t, sv, &pb.Account{Address: hot, RewardAddress: reward}, "7", "125")
	record, err := proto.Marshal(&pb.MinerExitRecord{
		MinerAddress:  hot,
		RewardAddress: reward,
		ExitHeight:    1,
	})
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyMinerExitRecord(hot), record)
	if _, err := tracker.RegisterMinerAtHeight(sv, hot, MinMinerStake.Uint64(), 0); err != nil {
		t.Fatal(err)
	}
	if height < 1+MinerExitFreezeBlocks {
		t.Fatalf("invalid mature height %d", height)
	}
}

func assertFormalMinerExitUntouched(t *testing.T, sv StateView, tracker *liveness.Tracker, hot, reward string, height uint64) {
	t.Helper()
	if !tracker.IsRegistered(hot) {
		t.Fatal("failing miner exit unregistered the liveness member")
	}
	if got := GetBalance(sv, hot, "FB"); got.Balance != "7" || got.MinerLockedBalance != "125" {
		t.Fatalf("failing miner exit changed hot balance: %+v", got)
	}
	if got := GetBalance(sv, reward, "FB"); got.Balance != "0" || got.MinerLockedBalance != "0" {
		t.Fatalf("failing miner exit changed reward balance: %+v", got)
	}
	if _, exists, err := sv.Get(keys.KeyMinerExitRecord(hot)); err != nil || !exists {
		t.Fatalf("failing miner exit consumed exit record: exists=%v err=%v", exists, err)
	}
	if _, exists, err := sv.Get(keys.KeyAccount(reward)); err != nil || exists {
		t.Fatalf("failing miner exit created reward account: exists=%v err=%v", exists, err)
	}
	if activity, err := sv.Scan(keys.KeyUserActivityHeightPrefix(reward, height)); err != nil || len(activity) != 0 {
		t.Fatalf("failing miner exit wrote activity: count=%d err=%v", len(activity), err)
	}
}

func TestFormalMinerExitBatchReadFailurePreservesTrackerAndState(t *testing.T) {
	const height = uint64(1 + MinerExitFreezeBlocks)
	base := newFormalMinerExitStateView()
	hot := newMinerTestKey(t).GetAddress()
	reward := newMinerTestKey(t).GetAddress()
	tracker := liveness.NewTracker(liveness.DefaultConfig())
	seedFormalMinerExit(t, base, tracker, hot, reward, height)

	view := formalMinerExitFaultView{StateView: base, batchErrorKey: keys.KeyAccount(reward)}
	err := (&Executor{LivenessTracker: tracker}).processMinerExitUnlocks(
		&pb.Block{Header: &pb.BlockHeader{Height: height}}, view,
	)
	if err == nil {
		t.Fatal("expected reward-state batch read failure")
	}
	assertFormalMinerExitUntouched(t, base, tracker, hot, reward, height)
}

func TestFormalMinerExitLateFailureRevertsStateBeforeTrackerMutation(t *testing.T) {
	const height = uint64(1 + MinerExitFreezeBlocks)
	base := newFormalMinerExitStateView()
	hot := newMinerTestKey(t).GetAddress()
	reward := newMinerTestKey(t).GetAddress()
	tracker := liveness.NewTracker(liveness.DefaultConfig())
	seedFormalMinerExit(t, base, tracker, hot, reward, height)

	view := formalMinerExitFaultView{
		StateView:    base,
		scanErrorKey: keys.KeyUserActivityHeightPrefix(reward, height),
	}
	err := (&Executor{LivenessTracker: tracker}).processMinerExitUnlocks(
		&pb.Block{Header: &pb.BlockHeader{Height: height}}, view,
	)
	if err == nil {
		t.Fatal("expected activity allocation failure")
	}
	assertFormalMinerExitUntouched(t, base, tracker, hot, reward, height)
}

func newFormalMinerExitStateView() StateView {
	return NewStateView(
		func(string) ([]byte, error) { return nil, nil },
		func(string) (map[string][]byte, error) { return nil, nil },
	)
}
