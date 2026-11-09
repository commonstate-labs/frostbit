package consensus

import (
	"bytes"
	"dex/pb"
	"dex/txpool"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
)

func commitmentTestTx(id string) *pb.AnyTx {
	return &pb.AnyTx{Content: &pb.AnyTx_Transaction{Transaction: &pb.Transaction{
		Base: &pb.BaseMessage{TxId: id},
	}}}
}

func commitmentTestBlock(t *testing.T, txs ...*pb.AnyTx) *pb.Block {
	t.Helper()
	header := &pb.BlockHeader{
		Height:         12,
		PrevBlockHash:  "parent-11",
		Timestamp:      123456789,
		StateRoot:      bytes.Repeat([]byte{0x11}, 32),
		TxsHash:        txpool.ComputeTxsHash(txs),
		Miner:          "7",
		Window:         2,
		VrfProof:       []byte{1, 2, 3},
		VrfOutput:      []byte{4, 5, 6},
		PublicKey:      []byte{7, 8, 9},
		OfflineReports: []uint32{2, 4},
		SampledReports: []uint32{1, 2, 3, 4},
	}
	converted, err := (&ConsensusAdapter{}).DBBlockToConsensus(&pb.Block{Header: header})
	if err != nil {
		t.Fatalf("convert header: %v", err)
	}
	return &pb.Block{
		BlockHash: converted.Header.ComputeIDForChain(pb.GetDefaultChainID()),
		Header:    header,
		Body:      txs,
	}
}

func TestValidateBlockCommitmentBindsOrderedBody(t *testing.T) {
	txA := commitmentTestTx("tx-a")
	txB := commitmentTestTx("tx-b")
	block := commitmentTestBlock(t, txA, txB)
	if err := ValidateBlockCommitment(block); err != nil {
		t.Fatalf("valid block rejected: %v", err)
	}

	reordered := proto.Clone(block).(*pb.Block)
	reordered.Body = []*pb.AnyTx{txB, txA}
	if err := ValidateBlockCommitment(reordered); err == nil || !strings.Contains(err.Error(), "txs_hash mismatch") {
		t.Fatalf("reordered body should fail txs hash validation, got %v", err)
	}

	replaced := proto.Clone(block).(*pb.Block)
	replaced.Body = []*pb.AnyTx{txA, commitmentTestTx("tx-c")}
	if err := ValidateBlockCommitment(replaced); err == nil || !strings.Contains(err.Error(), "txs_hash mismatch") {
		t.Fatalf("replaced body should fail txs hash validation, got %v", err)
	}
}

func TestValidateBlockCommitmentBindsFullHeader(t *testing.T) {
	block := commitmentTestBlock(t, commitmentTestTx("tx-a"))
	mutated := proto.Clone(block).(*pb.Block)
	mutated.Header.StateRoot = bytes.Repeat([]byte{0x22}, 32)
	if err := ValidateBlockCommitment(mutated); err == nil || !strings.Contains(err.Error(), "block_hash mismatch") {
		t.Fatalf("mutated state root should fail block hash validation, got %v", err)
	}
}

func TestValidateBlockCommitmentRejectsUncommittedTransaction(t *testing.T) {
	block := commitmentTestBlock(t)
	block.Body = []*pb.AnyTx{commitmentTestTx("")}
	if err := ValidateBlockCommitment(block); err == nil || !strings.Contains(err.Error(), "missing canonical tx_id") {
		t.Fatalf("empty tx ID should be rejected, got %v", err)
	}
}

func TestCacheBlockDoesNotReplaceCompletePayload(t *testing.T) {
	blockID := "cache-complete-immutable"
	defer clearCachedBlockForTest(blockID)
	emptyHash := txpool.ComputeTxsHash(nil)
	first := &pb.Block{BlockHash: blockID, Header: &pb.BlockHeader{Height: 1, TxsHash: emptyHash, StateRoot: []byte("first")}}
	second := &pb.Block{BlockHash: blockID, Header: &pb.BlockHeader{Height: 1, TxsHash: emptyHash, StateRoot: []byte("second")}}
	if !CacheBlock(first) || !SealCachedBlock(first) || !CacheBlock(second) {
		t.Fatal("cache insertion unexpectedly failed")
	}
	got, ok := GetCachedBlock(blockID)
	if !ok || !bytes.Equal(got.Header.StateRoot, first.Header.StateRoot) {
		t.Fatalf("complete cached payload was replaced: %+v", got)
	}
}

func TestRemoveCachedBlockPreservesSealedPayload(t *testing.T) {
	previousHeight := blockCacheAcceptedHeight.Load()
	blockCacheAcceptedHeight.Store(100)
	defer blockCacheAcceptedHeight.Store(previousHeight)

	blockID := "cache-seal-survives-network-cleanup"
	defer clearCachedBlockForTest(blockID)
	emptyHash := txpool.ComputeTxsHash(nil)
	original := &pb.Block{BlockHash: blockID, Header: &pb.BlockHeader{Height: 100, TxsHash: emptyHash, StateRoot: []byte("original")}}
	if !SealCachedBlock(original) {
		t.Fatal("failed to seal original payload")
	}

	farFuture := &pb.Block{BlockHash: blockID, Header: &pb.BlockHeader{Height: 100 + MaxFutureBlockCacheWindow + 1, TxsHash: emptyHash}}
	if CacheBlock(farFuture) {
		t.Fatal("far-future payload should be rejected")
	}
	RemoveCachedBlock(blockID)

	tampered := &pb.Block{BlockHash: blockID, Header: &pb.BlockHeader{Height: 100, TxsHash: emptyHash, StateRoot: []byte("tampered")}}
	if !CacheBlock(tampered) {
		t.Fatal("same-ID cache attempt unexpectedly failed")
	}
	got, ok := GetCachedBlock(blockID)
	if !ok || !bytes.Equal(got.Header.StateRoot, original.Header.StateRoot) {
		t.Fatalf("sealed payload was removed or replaced: %+v", got)
	}
}

func TestRemoveCachedBlockDeletesUnsealedPayload(t *testing.T) {
	blockID := "cache-unsealed-removable"
	defer clearCachedBlockForTest(blockID)
	if !CacheBlock(&pb.Block{BlockHash: blockID, Header: &pb.BlockHeader{Height: blockCacheAcceptedHeight.Load()}}) {
		t.Fatal("failed to cache unsealed payload")
	}
	RemoveCachedBlock(blockID)
	if _, ok := GetCachedBlock(blockID); ok {
		t.Fatal("unsealed payload should be removed")
	}
}

func clearCachedBlockForTest(blockID string) {
	blockCacheMu.Lock()
	defer blockCacheMu.Unlock()
	removeCachedBlockLocked(blockID)
}
