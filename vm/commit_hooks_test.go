package vm

import (
	iface "dex/interfaces"
	"dex/pb"
	"fmt"
	"math/big"
	"testing"
)

type staticActiveLivenessTracker struct {
	miners []string
	active map[string]bool
}

func (*staticActiveLivenessTracker) ProcessBlock(iface.LivenessWriteView, uint64, []uint32, []uint32) ([]uint32, []iface.LivenessSlashEvent) {
	return nil, nil
}

func (*staticActiveLivenessTracker) RegisterMinerAtHeight(iface.LivenessWriteView, string, uint64, uint64) (uint32, error) {
	return 0, nil
}

func (*staticActiveLivenessTracker) RemoveMiner(iface.LivenessWriteView, string, uint64) error {
	return nil
}

func (*staticActiveLivenessTracker) UnregisterMiner(iface.LivenessWriteView, string) error {
	return nil
}

func (*staticActiveLivenessTracker) UnregisterMinersBatch(iface.LivenessWriteView, []string) error {
	return nil
}

func (m *staticActiveLivenessTracker) IsActive(address string) bool {
	return m.active[address]
}

func (*staticActiveLivenessTracker) IsActiveOrProbation(string) bool { return false }
func (*staticActiveLivenessTracker) IsRegistered(string) bool        { return false }
func (m *staticActiveLivenessTracker) GetActiveCount() int           { return len(m.miners) }
func (*staticActiveLivenessTracker) GetAddressByIndex(uint32) (string, bool) {
	return "", false
}
func (m *staticActiveLivenessTracker) GetActiveMiners() []string {
	return append([]string(nil), m.miners...)
}
func (*staticActiveLivenessTracker) GetMinerState(string) int { return 0 }
func (*staticActiveLivenessTracker) GetMinerSnapshot(string) *iface.LivenessMinerSnapshot {
	return nil
}

func TestDistributeSlashedFBOddTotalConservesFunds(t *testing.T) {
	const proposer = "proposer"

	miners := []string{proposer}
	active := map[string]bool{proposer: true}
	eligibleOthers := make([]string, 0, 10)
	for i := 0; i < 10; i++ {
		address := fmt.Sprintf("eligible-%02d", i)
		miners = append(miners, address)
		active[address] = true
		eligibleOthers = append(eligibleOthers, address)
	}

	slashedAddrs := make(map[string]struct{}, 9)
	slashedMiners := make([]string, 0, 9)
	for i := 0; i < 9; i++ {
		address := fmt.Sprintf("slashed-%02d", i)
		miners = append(miners, address)
		active[address] = true
		slashedAddrs[address] = struct{}{}
		slashedMiners = append(slashedMiners, address)
	}

	executor := &Executor{LivenessTracker: &staticActiveLivenessTracker{
		miners: miners,
		active: active,
	}}
	sv := newMemStateView()
	slashedTotal := big.NewInt(45)
	executor.distributeSlashedFB(&pb.Block{Header: &pb.BlockHeader{
		Height: 2,
		Miner:  proposer,
	}}, slashedTotal, slashedAddrs, sv)

	balanceOf := func(address string) *big.Int {
		t.Helper()
		balance, ok := new(big.Int).SetString(GetBalance(sv, address, "FB").Balance, 10)
		if !ok {
			t.Fatalf("invalid FB balance for %s", address)
		}
		return balance
	}

	proposerBalance := balanceOf(proposer)
	if proposerBalance.Cmp(big.NewInt(25)) != 0 {
		t.Fatalf("proposer balance=%s, want 25", proposerBalance)
	}

	distributed := new(big.Int).Set(proposerBalance)
	for _, address := range eligibleOthers {
		balance := balanceOf(address)
		if balance.Cmp(big.NewInt(2)) != 0 {
			t.Fatalf("eligible miner %s balance=%s, want 2", address, balance)
		}
		distributed.Add(distributed, balance)
	}
	for _, address := range slashedMiners {
		if balance := balanceOf(address); balance.Sign() != 0 {
			t.Fatalf("slashed miner %s received balance=%s", address, balance)
		}
	}
	if distributed.Cmp(slashedTotal) != 0 {
		t.Fatalf("distributed total=%s, want %s", distributed, slashedTotal)
	}
}
