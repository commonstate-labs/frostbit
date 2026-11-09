package main

import (
	"context"
	"dex/config"
	"dex/keys"
	"dex/pb"
	"dex/vm"
	"dex/witness"
	"flag"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
)

func TestGenesisWitnessRegistrationStartupPolicy(t *testing.T) {
	originalFlags, originalArgs := flag.CommandLine, os.Args
	t.Cleanup(func() { flag.CommandLine, os.Args = originalFlags, originalArgs })
	peers := make([]PeerInfo, 5)
	for i := range peers {
		peers[i] = PeerInfo{Index: i, Address: fmt.Sprintf("miner-%d", i), IsMiner: true}
	}
	for _, tt := range []struct {
		name string
		args []string
		want bool
	}{
		{"default", []string{"dex"}, true},
		{"no simulator", []string{"dex", "--no-simulator"}, true},
		{"stress simulator", []string{"dex", "--simulator-in-single"}, true},
		{"opt out", []string{"dex", "--auto-register-witness=false"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			flag.CommandLine = flag.NewFlagSet("dex-test", flag.ContinueOnError)
			flag.CommandLine.SetOutput(io.Discard)
			os.Args = tt.args
			opts := parseRuntimeOptions()
			for _, peer := range peers {
				if got := shouldAutoRegisterWitness(opts, peer.Address, peers); got != tt.want {
					t.Fatalf("registration for %s = %v, want %v", peer.Address, got, tt.want)
				}
			}
			if shouldAutoRegisterWitness(opts, "latejoin-observer", peers) {
				t.Fatal("non-genesis observer must not auto-stake")
			}
		})
	}
}

func newWitnessRegistrationFixture(t *testing.T) (*TxSimulator, *NodeInstance) {
	t.Helper()
	s, node := newSimulatorAdmissionFixture(t, nil)
	s.witnessStakeAmount = config.DefaultWitnessConfig().MinStakeAmount
	return s, node
}

func TestWitnessRegistrationAdmissionRecoveryAndFinalization(t *testing.T) {
	s, node := newWitnessRegistrationFixture(t)
	                                                               
	s.witnessStakeAmount = "3300000000"
	if done, err := s.registerWitnessIfNeeded(node); err != nil || done {
		t.Fatalf("first registration = %v, %v; admission must await finalization", done, err)
	}
	pending := node.TxPool.PendingAccountTxs(node.Address)
	if len(pending) != 1 || pending[0].GetWitnessStakeTx().Amount != s.witnessStakeAmount {
		t.Fatalf("expected one configured stake transaction, got %v", pending)
	}
	tx := pending[0]
	if tx.GetBase().Nonce != 0 || tx.GetWitnessStakeTx().Op != pb.OrderOp_ADD {
		t.Fatalf("unexpected registration transaction: %v", tx)
	}
	if done, err := s.registerWitnessIfNeeded(node); err != nil || done || node.TxPool.PendingLen() != 1 {
		t.Fatalf("pending registration was duplicated or treated as finalized: done=%v err=%v", done, err)
	}
	                                                                                 
	restarted := NewTxSimulator([]*NodeInstance{node}, config.DefaultFrostConfig(), config.DefaultWitnessConfig(), nil)
	if done, err := restarted.registerWitnessIfNeeded(node); err != nil || done || node.TxPool.PendingLen() != 1 {
		t.Fatalf("restart duplicated pending registration: done=%v err=%v", done, err)
	}
	node.TxPool.RemoveTx(tx.GetTxId())
	if done, err := restarted.registerWitnessIfNeeded(node); err != nil || done {
		t.Fatalf("registration recovery = %v, %v", done, err)
	}
	replayed := node.TxPool.PendingAccountTxs(node.Address)
	if len(replayed) != 1 || !proto.Equal(replayed[0], tx) {
		t.Fatal("dropped registration must be resubmitted unchanged, including nonce and signature")
	}

	                                                                                     
	svc := witness.NewService(nil)
	registry := vm.NewHandlerRegistry()
	if err := vm.RegisterDefaultHandlers(registry, config.DefaultConfig(), svc); err != nil {
		t.Fatal(err)
	}
	executor := vm.NewExecutorWithWitnessService(node.DBManager, registry, nil, svc)
	block := &pb.Block{BlockHash: "witness-registration-block", Header: &pb.BlockHeader{
		Height: 2, StateRoot: node.DBManager.GetStateFingerprint(),
	}, Body: []*pb.AnyTx{tx}}
	if err := executor.CommitFinalizedBlock(block); err != nil {
		t.Fatalf("commit registration: %v", err)
	}
	receipt := executor.TakeCommittedReceipts(block.BlockHash)[tx.GetTxId()]
	if receipt == nil || receipt.Status != "SUCCEED" {
		t.Fatalf("registration receipt = %v", receipt)
	}
	node.TxPool.RemoveTx(tx.GetTxId())
	raw, exists, err := node.DBManager.GetState(keys.KeyWitnessInfo(node.Address))
	var info pb.WitnessInfo
	if err != nil || !exists || proto.Unmarshal(raw, &info) != nil || info.Status != pb.WitnessStatus_WITNESS_ACTIVE || info.StakeAmount != s.witnessStakeAmount {
		t.Fatalf("finalized witness = %v, exists=%v err=%v", &info, exists, err)
	}
	raw, _, err = node.DBManager.GetState(keys.KeyBalance(node.Address, "FB"))
	var balance pb.TokenBalanceRecord
	if err != nil || proto.Unmarshal(raw, &balance) != nil || balance.GetBalance().GetWitnessLockedBalance() != s.witnessStakeAmount || balance.GetBalance().GetBalance() != "96699999000" {
		t.Fatalf("registration balances = %v, err=%v", &balance, err)
	}
	if nonce, ok := finalizedSimAccountNonce(node); !ok || nonce != 1 {
		t.Fatalf("finalized nonce = %d, %v", nonce, ok)
	}
	restarted = NewTxSimulator([]*NodeInstance{node}, config.DefaultFrostConfig(), config.DefaultWitnessConfig(), nil)
	if done, err := restarted.registerWitnessIfNeeded(node); err != nil || !done || node.TxPool.PendingLen() != 0 {
		t.Fatalf("finalized restart must not stake again: done=%v err=%v", done, err)
	}
}

func TestWitnessRegistrationPreservesExistingExitAndSlash(t *testing.T) {
	for _, status := range []pb.WitnessStatus{pb.WitnessStatus_WITNESS_UNSTAKING, pb.WitnessStatus_WITNESS_EXITED, pb.WitnessStatus_WITNESS_SLASHED} {
		t.Run(status.String(), func(t *testing.T) {
			s, node := newWitnessRegistrationFixture(t)
			raw, err := proto.Marshal(&pb.WitnessInfo{Address: node.Address, Status: status, StakeAmount: "0"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := node.DBManager.SyncFinalizedStateUpdates(2, []interface{}{testStateWriteOp{key: keys.KeyWitnessInfo(node.Address), value: raw}}, true); err != nil {
				t.Fatal(err)
			}
			if done, err := s.registerWitnessIfNeeded(node); err != nil || !done || node.TxPool.PendingLen() != 0 {
				t.Fatalf("existing witness must remain unchanged: done=%v err=%v", done, err)
			}
		})
	}
}

func TestWitnessRegistrationWaitsForFundingWithoutConsumingNonce(t *testing.T) {
	s, node := newWitnessRegistrationFixture(t)
	syncSimulatorCapitalState(t, node.DBManager, 2, 0, node.Address, "FB", s.witnessStakeAmount)
	if done, err := s.registerWitnessIfNeeded(node); err == nil || done || node.TxPool.PendingLen() != 0 {
		t.Fatalf("stake without fee funding must wait: done=%v err=%v", done, err)
	}
	syncSimulatorCapitalState(t, node.DBManager, 3, 0, node.Address, "FB", "100000000000")
	if done, err := s.registerWitnessIfNeeded(node); err != nil || done {
		t.Fatalf("funded registration = %v, %v", done, err)
	}
	pending := node.TxPool.PendingAccountTxs(node.Address)
	if len(pending) != 1 || pending[0].GetBase().Nonce != 0 {
		t.Fatal("waiting for funds must not consume an account nonce")
	}
}

func TestWitnessRegistrationStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() { (&TxSimulator{}).runWitnessRegistration(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("registration worker did not stop")
	}
}
