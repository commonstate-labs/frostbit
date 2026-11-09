package consensus

import (
	"dex/config"
	"dex/db"
	"dex/interfaces"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"dex/txpool"
	"dex/types"
	"dex/utils"
	"path/filepath"
	"reflect"
	"testing"
)

type mockLivenessTrackerForSyncReports struct {
	sampled []uint32
	offline []uint32
}

func (m *mockLivenessTrackerForSyncReports) ReportSampled([]string) {}
func (m *mockLivenessTrackerForSyncReports) ReportOffline([]string) {}
func (m *mockLivenessTrackerForSyncReports) GetSampledReports() []uint32 {
	return append([]uint32(nil), m.sampled...)
}
func (m *mockLivenessTrackerForSyncReports) GetOfflineReports() []uint32 {
	return append([]uint32(nil), m.offline...)
}
func (m *mockLivenessTrackerForSyncReports) ProcessBlock(interfaces.LivenessWriteView, uint64, []uint32, []uint32) ([]uint32, []interfaces.LivenessSlashEvent) {
	return nil, nil
}
func (m *mockLivenessTrackerForSyncReports) RegisterMinerAtHeight(interfaces.LivenessWriteView, string, uint64, uint64) (uint32, error) {
	return 0, nil
}
func (m *mockLivenessTrackerForSyncReports) RemoveMiner(interfaces.LivenessWriteView, string, uint64) error {
	return nil
}
func (m *mockLivenessTrackerForSyncReports) UnregisterMiner(interfaces.LivenessWriteView, string) error {
	return nil
}
func (m *mockLivenessTrackerForSyncReports) UnregisterMinersBatch(interfaces.LivenessWriteView, []string) error {
	return nil
}
func (m *mockLivenessTrackerForSyncReports) IsActive(string) bool            { return true }
func (m *mockLivenessTrackerForSyncReports) IsActiveOrProbation(string) bool { return true }
func (m *mockLivenessTrackerForSyncReports) IsRegistered(string) bool        { return true }
func (m *mockLivenessTrackerForSyncReports) GetActiveCount() int             { return 0 }
func (m *mockLivenessTrackerForSyncReports) GetAddressByIndex(uint32) (string, bool) {
	return "", false
}
func (m *mockLivenessTrackerForSyncReports) GetActiveMiners() []string { return nil }
func (m *mockLivenessTrackerForSyncReports) GetMinerState(string) int  { return 0 }
func (m *mockLivenessTrackerForSyncReports) GetMinerSnapshot(string) *interfaces.LivenessMinerSnapshot {
	return nil
}

func TestDBBlockToConsensusPreservesLivenessReports(t *testing.T) {
	adapter := NewConsensusAdapter(nil)
	dbBlock := &pb.Block{
		BlockHash: "block-liveness-db-to-types",
		Header: &pb.BlockHeader{
			Height:         7,
			PrevBlockHash:  "parent-6",
			Timestamp:      12345,
			StateRoot:      []byte("state-root"),
			TxsHash:        "tx-hash",
			Miner:          keys.KeyNode() + "7",
			Window:         2,
			VrfProof:       []byte{1, 2},
			VrfOutput:      []byte{3, 4},
			PublicKey:      []byte{5, 6},
			OfflineReports: []uint32{2, 4},
			SampledReports: []uint32{1, 3, 5},
		},
	}

	block, err := adapter.DBBlockToConsensus(dbBlock)
	if err != nil {
		t.Fatalf("DBBlockToConsensus failed: %v", err)
	}
	if block.Header.Proposer != "7" {
		t.Fatalf("unexpected proposer: %s", block.Header.Proposer)
	}
	if block.Header.Miner != keys.KeyNode()+"7" {
		t.Fatalf("unexpected raw miner: %s", block.Header.Miner)
	}
	if !reflect.DeepEqual(block.Header.OfflineReports, []uint32{2, 4}) {
		t.Fatalf("offline reports lost: %v", block.Header.OfflineReports)
	}
	if !reflect.DeepEqual(block.Header.SampledReports, []uint32{1, 3, 5}) {
		t.Fatalf("sampled reports lost: %v", block.Header.SampledReports)
	}
}

func TestConsensusBlockToDBPrefersBlockHeaderLivenessReports(t *testing.T) {
	adapter := NewConsensusAdapter(nil)
	block := &types.Block{
		ID: "block-liveness-types-to-db",
		Header: types.BlockHeader{
			Height:         8,
			ParentID:       "parent-7",
			Timestamp:      23456,
			StateRoot:      "state-root",
			TxHash:         "tx-hash",
			Proposer:       "8",
			Miner:          keys.KeyNode() + "8",
			Window:         1,
			VRFProof:       []byte{7},
			VRFOutput:      []byte{8},
			PublicKey:      []byte{9},
			OfflineReports: []uint32{6},
			SampledReports: []uint32{1, 6},
		},
	}

	dbBlock := adapter.ConsensusBlockToDB(block, nil)
	if dbBlock == nil || dbBlock.Header == nil {
		t.Fatal("ConsensusBlockToDB returned nil header")
	}
	if !reflect.DeepEqual(dbBlock.Header.OfflineReports, []uint32{6}) {
		t.Fatalf("offline reports not preserved: %v", dbBlock.Header.OfflineReports)
	}
	if !reflect.DeepEqual(dbBlock.Header.SampledReports, []uint32{1, 6}) {
		t.Fatalf("sampled reports not preserved: %v", dbBlock.Header.SampledReports)
	}
	if dbBlock.Header.Miner != keys.KeyNode()+"8" {
		t.Fatalf("raw miner not preserved: %s", dbBlock.Header.Miner)
	}
}

func TestConsensusBlockToDBFallsBackToCachedLivenessReports(t *testing.T) {
	blockID := "block-liveness-cache-fallback"
	defer RemoveCachedBlock(blockID)

	CacheBlock(&pb.Block{
		BlockHash: blockID,
		Header: &pb.BlockHeader{
			OfflineReports: []uint32{9},
			SampledReports: []uint32{2, 9},
		},
	})

	adapter := NewConsensusAdapter(nil)
	block := &types.Block{
		ID: blockID,
		Header: types.BlockHeader{
			Height:    9,
			ParentID:  "parent-8",
			StateRoot: "state-root",
			TxHash:    "tx-hash",
			Proposer:  "9",
			Window:    0,
		},
	}

	dbBlock := adapter.ConsensusBlockToDB(block, nil)
	if !reflect.DeepEqual(dbBlock.Header.OfflineReports, []uint32{9}) {
		t.Fatalf("offline reports fallback lost: %v", dbBlock.Header.OfflineReports)
	}
	if !reflect.DeepEqual(dbBlock.Header.SampledReports, []uint32{2, 9}) {
		t.Fatalf("sampled reports fallback lost: %v", dbBlock.Header.SampledReports)
	}
}

func TestPendingBlockBufferBuildFullPbBlockPreservesLivenessReports(t *testing.T) {
	buffer := &PendingBlockBuffer{}
	block := &types.Block{
		ID: "block-liveness-pending-buffer",
		Header: types.BlockHeader{
			Height:         10,
			ParentID:       "parent-9",
			StateRoot:      "state-root",
			TxHash:         "tx-hash",
			Proposer:       "10",
			Miner:          keys.KeyNode() + "10",
			Window:         3,
			OfflineReports: []uint32{11},
			SampledReports: []uint32{10, 11},
		},
	}

	fullBlock := buffer.buildFullPbBlock(block, nil)
	if !reflect.DeepEqual(fullBlock.Header.OfflineReports, []uint32{11}) {
		t.Fatalf("offline reports lost in pending buffer rebuild: %v", fullBlock.Header.OfflineReports)
	}
	if !reflect.DeepEqual(fullBlock.Header.SampledReports, []uint32{10, 11}) {
		t.Fatalf("sampled reports lost in pending buffer rebuild: %v", fullBlock.Header.SampledReports)
	}
	if fullBlock.Header.Miner != keys.KeyNode()+"10" {
		t.Fatalf("raw miner lost in pending buffer rebuild: %s", fullBlock.Header.Miner)
	}
}

func TestExtractBoundLivenessReportsTag(t *testing.T) {
	tag := types.LivenessReportsTag([]uint32{1, 2}, []uint32{2})
	got, ok := extractBoundLivenessReportsTag("block-10-miner-w1-abcdef12-deadbeef-" + tag)
	if !ok || got != tag {
		t.Fatalf("expected bound reports tag %s, got %s ok=%v", tag, got, ok)
	}
	if _, ok := extractBoundLivenessReportsTag("block-10-miner-w1-abcdef12"); ok {
		t.Fatal("old block ID format should not be treated as report-bound")
	}
}

func TestRealBlockProposerProposeBlockEmbedsLivenessReports(t *testing.T) {
	logger := logs.NewNodeLogger("test", 100)
	dbManager, err := db.NewManager(filepath.Join(t.TempDir(), "liveness_proposer"), logger)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	defer dbManager.Close()

	pool, err := txpool.NewTxPool(dbManager, &mockTxValidator{}, "test", logger)
	if err != nil {
		t.Fatalf("NewTxPool failed: %v", err)
	}

	keyMgr := utils.NewKeyManager()
	if err := keyMgr.InitKeyRandom(); err != nil {
		t.Fatalf("InitKeyRandom failed: %v", err)
	}

	cfg := config.DefaultConfig()
	tracker := &mockLivenessTrackerForSyncReports{
		sampled: []uint32{1, 4},
		offline: []uint32{4},
	}

	proposer := &RealBlockProposer{
		maxBlocksPerHeight:  2,
		maxTxsPerBlock:      cfg.TxPool.MaxTxsPerBlock,
		maxTxsLimitPerBlock: cfg.TxPool.MaxTxsLimitPerBlock,
		pool:                pool,
		dbManager:           dbManager,
		vrfProvider:         utils.NewVRFProvider(),
		windowConfig:        cfg.Window,
		livenessTracker:     tracker,
		livenessReporter:    tracker,
		keyMgr:              keyMgr,
		selfAddress:         keyMgr.GetAddress(),
	}

	block, err := proposer.ProposeBlock("parent-10", 11, types.NodeID("11"), 0)
	if err != nil {
		t.Fatalf("ProposeBlock failed: %v", err)
	}
	defer RemoveCachedBlock(block.ID)

	if !reflect.DeepEqual(block.Header.SampledReports, tracker.sampled) {
		t.Fatalf("types block missing sampled reports: %v", block.Header.SampledReports)
	}
	if !reflect.DeepEqual(block.Header.OfflineReports, tracker.offline) {
		t.Fatalf("types block missing offline reports: %v", block.Header.OfflineReports)
	}
	if block.Header.Miner != keys.KeyNode()+"11" {
		t.Fatalf("types block missing raw miner: %s", block.Header.Miner)
	}

	cached, ok := GetCachedBlock(block.ID)
	if !ok || cached == nil || cached.Header == nil {
		t.Fatal("cached pb block missing")
	}
	if !reflect.DeepEqual(cached.Header.SampledReports, tracker.sampled) {
		t.Fatalf("cached pb block missing sampled reports: %v", cached.Header.SampledReports)
	}
	if !reflect.DeepEqual(cached.Header.OfflineReports, tracker.offline) {
		t.Fatalf("cached pb block missing offline reports: %v", cached.Header.OfflineReports)
	}
}
