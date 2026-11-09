package vm_test

import (
	"dex/keys"
	"dex/pb"
	"dex/vm"
	"strings"
	"testing"
)

func TestBuildSpecResultRejectsExecutionGuardBeforeExecutionMetadata(t *testing.T) {
	tx := &pb.AnyTx{Content: &pb.AnyTx_Transaction{Transaction: &pb.Transaction{
		Base: &pb.BaseMessage{
			TxId:         "guarded-transfer",
			FromAddress:  "alice",
			Fee:          "1000",
			TargetHeight: 8,
			OnlyProposer: "miner-a",
		},
		To:           "bob",
		TokenAddress: "FB",
		Amount:       "1",
	}}}
	block := &pb.Block{
		BlockHash: "wrong-guard-block",
		Header: &pb.BlockHeader{
			Height:        7,
			PrevBlockHash: "parent-6",
			Miner:         keys.KeyNode() + "miner-a",
		},
		Body: []*pb.AnyTx{tx},
	}
	executor := vm.NewExecutor(NewMockDB(), vm.NewHandlerRegistry(), vm.NewSpecExecLRU(1))

	result, err := executor.BuildSpecResult(block)
	if err != nil {
		t.Fatalf("BuildSpecResult() error = %v", err)
	}
	if result == nil || result.Valid || !strings.Contains(result.Reason, "target_height") {
		t.Fatalf("guard mismatch must invalidate block, result = %+v", result)
	}
	if tx.GetBase().ExecutedHeight != 0 {
		t.Fatalf("execution metadata changed before guard rejection: executed_height=%d", tx.GetBase().ExecutedHeight)
	}
}
