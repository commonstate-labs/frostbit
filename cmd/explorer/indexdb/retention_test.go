package indexdb

import (
	"fmt"
	"testing"

	"dex/pb"
)

func TestPruneToKeepLimitedRetainsSharedDetailUntilLastIndexedHeight(t *testing.T) {
	idb, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer idb.Close()

	shared := testTransferTx("shared-tx", "alice")
	newest := testTransferTx("newest-tx", "alice")
	for _, item := range []struct {
		height uint64
		tx     *pb.AnyTx
	}{
		{height: 1, tx: shared},
		{height: 2, tx: shared},
		{height: 3, tx: newest},
	} {
		if err := idb.IndexBlock(&pb.Block{Header: &pb.BlockHeader{Height: item.height}, Body: []*pb.AnyTx{item.tx}}); err != nil {
			t.Fatalf("IndexBlock(%d): %v", item.height, err)
		}
	}

	                                                                          
	                                                                          
	                      
	if err := idb.PruneToKeepLimited(3, 1, 1); err != nil {
		t.Fatalf("first PruneToKeepLimited: %v", err)
	}
	if got, err := idb.GetTxRecord("shared-tx"); err != nil || got == nil {
		t.Fatalf("shared detail after first prune = %+v, err=%v", got, err)
	}
	if got, err := idb.GetBlockTxs(1); err != nil || len(got) != 0 {
		t.Fatalf("pruned height 1 records = %+v, err=%v", got, err)
	}
	if got, err := idb.GetBlockTxs(2); err != nil || len(got) != 1 || got[0].TxID != "shared-tx" {
		t.Fatalf("retained height 2 records = %+v, err=%v", got, err)
	}
	if refs, err := idb.Get(KeyTxDetailRef("shared-tx")); err != nil || refs != "1" {
		t.Fatalf("shared detail refcount after first prune = %q, err=%v", refs, err)
	}

	if err := idb.PruneToKeepLimited(3, 1, 1); err != nil {
		t.Fatalf("second PruneToKeepLimited: %v", err)
	}
	if got, err := idb.GetTxRecord("shared-tx"); err != nil || got != nil {
		t.Fatalf("shared detail after final reference pruned = %+v, err=%v", got, err)
	}
	if got, err := idb.GetBlockTxs(3); err != nil || len(got) != 1 || got[0].TxID != "newest-tx" {
		t.Fatalf("newest retained block records = %+v, err=%v", got, err)
	}
	metadata, err := idb.GetHistoryMetadata()
	if err != nil || metadata == nil || !metadata.Partial || metadata.BaseHeight != 3 {
		t.Fatalf("history metadata after retention = %+v, err=%v", metadata, err)
	}
	if count, err := idb.GetAddressTxCount("receiver"); err != nil || count != 1 {
		t.Fatalf("receiver retained transaction count = %d, err=%v", count, err)
	}
}

func TestIndexBlockCountsOneDetailReferencePerHeight(t *testing.T) {
	idb, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer idb.Close()

	duplicate := testTransferTx("duplicate-in-height", "alice")
	block := &pb.Block{
		BlockHash: "block-1",
		Header:    &pb.BlockHeader{Height: 1},
		Body:      []*pb.AnyTx{duplicate, duplicate},
	}
	if err := idb.IndexBlock(block); err != nil {
		t.Fatalf("IndexBlock: %v", err)
	}
	if refs, err := idb.Get(KeyTxDetailRef("duplicate-in-height")); err != nil || refs != "1" {
		t.Fatalf("same-height detail refs = %q, err=%v; want 1", refs, err)
	}
	if got, err := idb.GetBlockTxs(1); err != nil || len(got) != 2 {
		t.Fatalf("same-height block slots = %+v, err=%v; want 2", got, err)
	}

	if err := idb.IndexBlock(&pb.Block{BlockHash: "block-2", Header: &pb.BlockHeader{Height: 2}}); err != nil {
		t.Fatalf("IndexBlock(2): %v", err)
	}
	if err := idb.PruneToKeepLimited(2, 1, 1); err != nil {
		t.Fatalf("PruneToKeepLimited: %v", err)
	}
	if got, err := idb.GetTxRecord("duplicate-in-height"); err != nil || got != nil {
		t.Fatalf("detail after owning height pruned = %+v, err=%v; want nil", got, err)
	}
}

func TestIndexBlockRejectsInvalidDetailReferenceAtomically(t *testing.T) {
	idb, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer idb.Close()

	if err := idb.Set(KeyTxDetailRef("corrupt-ref"), "not-a-count"); err != nil {
		t.Fatalf("seed corrupt ref: %v", err)
	}
	block := &pb.Block{
		BlockHash: "block-1",
		Header:    &pb.BlockHeader{Height: 1},
		Body:      []*pb.AnyTx{testTransferTx("corrupt-ref", "alice")},
	}
	if err := idb.IndexBlock(block); err == nil {
		t.Fatal("IndexBlock unexpectedly accepted a corrupt detail refcount")
	}
	if manifest, err := idb.Get(KeyHeightManifest(1)); err != nil || manifest != "" {
		t.Fatalf("manifest after rejected block = %q, err=%v; want empty", manifest, err)
	}
	if syncHeight, err := idb.GetSyncHeight(); err != nil || syncHeight != 0 {
		t.Fatalf("sync height after rejected block = %d, err=%v; want 0", syncHeight, err)
	}
	if blockSlot, err := idb.Get(KeyBlockTx(1, 0)); err != nil || blockSlot != "" {
		t.Fatalf("block slot after rejected block = %q, err=%v; want empty", blockSlot, err)
	}
}

func TestIndexBlockRejectsDanglingDetailReferenceAtomically(t *testing.T) {
	idb, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer idb.Close()

	if err := idb.Set(KeyTxDetailRef("dangling-ref"), "1"); err != nil {
		t.Fatalf("seed dangling ref: %v", err)
	}
	block := &pb.Block{
		BlockHash: "block-1",
		Header:    &pb.BlockHeader{Height: 1},
		Body:      []*pb.AnyTx{testTransferTx("dangling-ref", "alice")},
	}
	if err := idb.IndexBlock(block); err == nil {
		t.Fatal("IndexBlock unexpectedly accepted a dangling detail refcount")
	}
	if manifest, err := idb.Get(KeyHeightManifest(1)); err != nil || manifest != "" {
		t.Fatalf("manifest after rejected block = %q, err=%v; want empty", manifest, err)
	}
	if syncHeight, err := idb.GetSyncHeight(); err != nil || syncHeight != 0 {
		t.Fatalf("sync height after rejected block = %d, err=%v; want 0", syncHeight, err)
	}
}

func TestIndexBlockSameHeightIsIdempotentForRetention(t *testing.T) {
	idb, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer idb.Close()

	block := &pb.Block{
		BlockHash: "block-1",
		Header:    &pb.BlockHeader{Height: 1},
		Body:      []*pb.AnyTx{testTransferTx("idempotent-tx", "alice")},
	}
	if err := idb.IndexBlock(block); err != nil {
		t.Fatalf("first IndexBlock: %v", err)
	}
	if err := idb.IndexBlock(block); err != nil {
		t.Fatalf("replayed IndexBlock: %v", err)
	}
	if refs, err := idb.Get(KeyTxDetailRef("idempotent-tx")); err != nil || refs != "1" {
		t.Fatalf("tx detail refs after replay = %q, err=%v; want 1", refs, err)
	}

	conflict := &pb.Block{BlockHash: "different-block-1", Header: &pb.BlockHeader{Height: 1}}
	if err := idb.IndexBlock(conflict); err == nil {
		t.Fatal("conflicting replay unexpectedly succeeded")
	}
}

func TestPruneToKeepLimitedResumesFromPersistedCursorAfterReopen(t *testing.T) {
	dir := t.TempDir()
	idb, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for height := uint64(1); height <= 5; height++ {
		block := &pb.Block{BlockHash: fmt.Sprintf("block-%d", height), Header: &pb.BlockHeader{Height: height}}
		if err := idb.IndexBlock(block); err != nil {
			t.Fatalf("IndexBlock(%d): %v", height, err)
		}
	}
	if err := idb.PruneToKeepLimited(5, 1, 2); err != nil {
		t.Fatalf("first prune: %v", err)
	}
	if err := idb.Close(); err != nil {
		t.Fatalf("close before reopen: %v", err)
	}

	idb, err = New(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer idb.Close()
	if err := idb.PruneToKeepLimited(5, 1, 2); err != nil {
		t.Fatalf("second prune: %v", err)
	}
	for height := uint64(1); height <= 4; height++ {
		if hash, err := idb.GetBlockHash(height); err != nil || hash != "" {
			t.Fatalf("height %d survived resumed prune: hash=%q err=%v", height, hash, err)
		}
	}
	if hash, err := idb.GetBlockHash(5); err != nil || hash != "block-5" {
		t.Fatalf("recent height was pruned: hash=%q err=%v", hash, err)
	}
	metadata, err := idb.GetHistoryMetadata()
	if err != nil || metadata == nil || metadata.BaseHeight != 5 {
		t.Fatalf("history metadata after resumed prune=%+v err=%v", metadata, err)
	}
}
