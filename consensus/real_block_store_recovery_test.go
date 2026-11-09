package consensus

import (
	"fmt"
	"testing"
	"time"

	"dex/config"
	"dex/db"
	"dex/logs"
	"dex/pb"
	"dex/types"
)

func TestRealBlockStoreRestartIgnoresPersistedCandidatesAboveFinalizedState(t *testing.T) {
	manager, err := db.NewManagerWithConfig(t.TempDir(), logs.NewNodeLogger("finalized_recovery", 0), config.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	manager.InitWriteQueue(64, time.Hour)

	for _, height := range []uint64{10, 12} {
		block := &pb.Block{
			BlockHash: "block-" + fmt.Sprint(height),
			Header:    &pb.BlockHeader{Height: height, Miner: "miner"},
		}
		if err := manager.SaveBlock(block); err != nil {
			t.Fatal(err)
		}
	}
	if err := manager.ForceFlush(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.SyncFinalizedStateUpdatesForBlock(10, "block-10", nil, false); err != nil {
		t.Fatal(err)
	}

	store, ok := NewRealBlockStore(types.NodeID("node"), manager, nil, config.DefaultConfig()).(*RealBlockStore)
	if !ok {
		t.Fatal("expected real block store")
	}
	if err := store.RecoveryError(); err != nil {
		t.Fatal(err)
	}
	if id, height := store.GetLastAccepted(); id != "block-10" || height != 10 {
		t.Fatalf("recovered accepted tip=(%s,%d), want (block-10,10)", id, height)
	}
	if height := store.GetCurrentHeight(); height != 10 {
		t.Fatalf("recovered current height=%d, want finalized height 10", height)
	}
	if _, exists := store.GetFinalizedAtHeight(12); exists {
		t.Fatal("persisted candidate at height 12 was promoted to finalized on restart")
	}
	blocks := store.GetBlocksFromHeight(10, 12)
	if len(blocks) != 1 || blocks[0].ID != "block-10" {
		t.Fatalf("finalized sync range=%v, want only block-10", blocks)
	}
}
