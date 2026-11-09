package liveness

import (
	"reflect"
	"testing"

	"dex/keys"
)

func TestFormalUnregisterMinersBatchValidatesBeforeMutation(t *testing.T) {
	tracker, state := newTrackerWithStore(defaultTestCfg())
	aliceIndex, err := tracker.RegisterMinerAtHeight(state, "alice", 1000, 0)
	if err != nil {
		t.Fatal(err)
	}
	bobIndex, err := tracker.RegisterMinerAtHeight(state, "bob", 1000, 0)
	if err != nil {
		t.Fatal(err)
	}

	                                                                       
	                                                                       
	tracker.miners[bobIndex].Address = "not-bob"
	wantMiners := cloneFormalMinerMap(tracker.miners)
	wantAddresses := cloneFormalAddressIndex(tracker.addrToIndex)
	wantState := cloneFormalByteMap(state.data)

	if err := tracker.UnregisterMinersBatch(state, []string{"alice", "bob"}); err == nil {
		t.Fatal("expected corrupt second miner to reject the batch")
	}
	if !reflect.DeepEqual(tracker.miners, wantMiners) {
		t.Fatal("failed batch changed miner registry")
	}
	if !reflect.DeepEqual(tracker.addrToIndex, wantAddresses) {
		t.Fatal("failed batch changed address index")
	}
	if !reflect.DeepEqual(state.data, wantState) {
		t.Fatal("failed batch changed persisted liveness state")
	}
	if _, ok := state.data[keys.KeyLivenessMiner(aliceIndex)]; !ok {
		t.Fatal("failed batch deleted the valid first miner")
	}
}

func cloneFormalMinerMap(input map[uint32]*MinerInfo) map[uint32]*MinerInfo {
	out := make(map[uint32]*MinerInfo, len(input))
	for index, miner := range input {
		if miner == nil {
			out[index] = nil
			continue
		}
		copyMiner := *miner
		copyMiner.PendingStakeBatches = append([]StakeMaturityBatch(nil), miner.PendingStakeBatches...)
		out[index] = &copyMiner
	}
	return out
}

func cloneFormalAddressIndex(input map[string]uint32) map[string]uint32 {
	out := make(map[string]uint32, len(input))
	for address, index := range input {
		out[address] = index
	}
	return out
}

func cloneFormalByteMap(input map[string][]byte) map[string][]byte {
	out := make(map[string][]byte, len(input))
	for key, value := range input {
		out[key] = append([]byte(nil), value...)
	}
	return out
}
