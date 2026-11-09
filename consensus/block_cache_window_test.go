package consensus

import (
	"dex/pb"
	"testing"
)

func TestCacheBlockRejectsTooFarFutureBlock(t *testing.T) {
	SetBlockCacheAcceptedHeight(100)
	nearID := "test-near-future-cache-window"
	farID := "test-far-future-cache-window"
	defer RemoveCachedBlock(nearID)
	defer RemoveCachedBlock(farID)

	near := &pb.Block{
		BlockHash: nearID,
		Header:    &pb.BlockHeader{Height: 100 + MaxFutureBlockCacheWindow},
	}
	if !CacheBlock(near) {
		t.Fatal("block inside future window should be cached")
	}
	if _, ok := GetCachedBlock(nearID); !ok {
		t.Fatal("near future block missing from cache")
	}

	far := &pb.Block{
		BlockHash: farID,
		Header:    &pb.BlockHeader{Height: 100 + MaxFutureBlockCacheWindow + 1},
	}
	if CacheBlock(far) {
		t.Fatal("block outside future window should be rejected")
	}
	if _, ok := GetCachedBlock(farID); ok {
		t.Fatal("far future block must not stay in cache")
	}
}
