package handlers

import (
	"testing"

	"dex/pb"
)

func TestShouldImportBlockTxToPendingSkipsExecutionGuards(t *testing.T) {
	accountTx := func(base *pb.BaseMessage) *pb.AnyTx {
		return &pb.AnyTx{Content: &pb.AnyTx_Transaction{Transaction: &pb.Transaction{Base: base}}}
	}
	protocolTx := &pb.AnyTx{Content: &pb.AnyTx_CertSignTx{CertSignTx: &pb.CertSignTx{
		Base: &pb.BaseMessage{TargetHeight: 1, OnlyProposer: "miner"},
	}}}

	cases := []struct {
		name string
		tx   *pb.AnyTx
		want bool
	}{
		{name: "nil", tx: nil, want: false},
		{name: "ordinary", tx: accountTx(&pb.BaseMessage{}), want: true},
		{name: "target height", tx: accountTx(&pb.BaseMessage{TargetHeight: 10}), want: false},
		{name: "only proposer", tx: accountTx(&pb.BaseMessage{OnlyProposer: "miner"}), want: false},
		{name: "protocol guard ignored", tx: protocolTx, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldImportBlockTxToPending(tc.tx); got != tc.want {
				t.Fatalf("shouldImportBlockTxToPending()=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestCloneBlockTxForPendingDoesNotAliasSealedBlockTx(t *testing.T) {
	original := &pb.AnyTx{Content: &pb.AnyTx_Transaction{Transaction: &pb.Transaction{
		Base: &pb.BaseMessage{TxId: "clone-isolation", Status: pb.Status_PENDING},
	}}}
	cloned := cloneBlockTxForPending(original)
	if cloned == nil || cloned == original || cloned.GetBase() == original.GetBase() {
		t.Fatal("pending transaction was not deep-cloned")
	}
	original.GetBase().Status = pb.Status_SUCCEED
	original.GetBase().ExecutedHeight = 9
	if cloned.GetBase().Status != pb.Status_PENDING || cloned.GetBase().ExecutedHeight != 0 {
		t.Fatalf("sealed block mutation leaked into pending clone: %+v", cloned.GetBase())
	}
}
