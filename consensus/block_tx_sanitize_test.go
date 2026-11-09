package consensus

import (
	"dex/pb"
	"testing"
)

func TestSanitizeBlockTxsForCanonicalValidationDoesNotRewriteWitnessRequest(t *testing.T) {
	pb.SetDefaultChainID(1001)
	tx := &pb.AnyTx{Content: &pb.AnyTx_WitnessRequestTx{WitnessRequestTx: &pb.WitnessRequestTx{
		Base:            &pb.BaseMessage{FromAddress: "requester", ChainId: 1001},
		NativeChain:     "eth",
		NativeTxHash:    "0xdeposit",
		Amount:          "100",
		ReceiverAddress: "receiver",
	}}}
	tx.GetBase().TxId = tx.GenerateTxID()
	tx.GetWitnessRequestTx().NativeContract = "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"
	if canonicalTxIDForValidation(tx) == tx.GetBase().TxId {
		t.Fatal("test setup must create a mutated canonical mismatch")
	}

	block := &pb.Block{BlockHash: "b1", Header: &pb.BlockHeader{Height: 7}, Body: []*pb.AnyTx{tx}}
	sanitized, changed := sanitizeBlockTxsForCanonicalValidation(block)
	if changed {
		t.Fatal("witness request consensus input must not be rewritten")
	}
	if sanitized != block {
		t.Fatal("sanitizer returned a cloned block without a change")
	}
	if block.Body[0].GetWitnessRequestTx().NativeContract == "" {
		t.Fatal("original request was mutated")
	}
}
