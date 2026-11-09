package consensus

import (
	"path/filepath"
	"reflect"
	"testing"

	"dex/config"
	"dex/db"
	"dex/logs"
	"dex/pb"
	"dex/protocol"
	"dex/txpool"
	"dex/types"
	"dex/utils"

	"google.golang.org/protobuf/proto"
)

type proposerOnlineHeaderReporter struct {
	*mockLivenessTrackerForSyncReports
	online []uint32
	drains int
}

func (r *proposerOnlineHeaderReporter) GetLivenessReportsWithOnline() (sampled, offline, online []uint32) {
	r.drains++
	return append([]uint32(nil), r.sampled...), append([]uint32(nil), r.offline...), append([]uint32(nil), r.online...)
}

func (r *proposerOnlineHeaderReporter) GetSampledReports() []uint32 {
	panic("proposer must drain all three lists atomically")
}

func (r *proposerOnlineHeaderReporter) GetOfflineReports() []uint32 {
	panic("proposer must drain all three lists atomically")
}

func TestOnlineReportsProposerUsesAtomicThreeListReporter(t *testing.T) {
	logger := logs.NewNodeLogger("online-proposer", 100)
	database, err := db.NewManager(filepath.Join(t.TempDir(), "online_proposer"), logger)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	pool, err := txpool.NewTxPool(database, &mockTxValidator{}, "online-proposer", logger)
	if err != nil {
		t.Fatal(err)
	}
	keyManager := utils.NewKeyManager()
	if err := keyManager.InitKeyRandom(); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	reporter := &proposerOnlineHeaderReporter{
		mockLivenessTrackerForSyncReports: &mockLivenessTrackerForSyncReports{sampled: []uint32{1, 2, 3}, offline: []uint32{2}},
		online:                            []uint32{1, 3},
	}
	proposer := &RealBlockProposer{
		maxBlocksPerHeight: 2, maxTxsPerBlock: cfg.TxPool.MaxTxsPerBlock, maxTxsLimitPerBlock: cfg.TxPool.MaxTxsLimitPerBlock,
		pool: pool, dbManager: database, vrfProvider: utils.NewVRFProvider(), windowConfig: cfg.Window,
		livenessTracker: reporter, livenessReporter: reporter, keyMgr: keyManager, selfAddress: keyManager.GetAddress(),
	}
	block, err := proposer.ProposeBlock("parent-online", 1, types.NodeID(keyManager.GetAddress()), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer clearCachedBlockForTest(block.ID)
	if reporter.drains != 1 || !reflect.DeepEqual(block.Header.OnlineReports, reporter.online) {
		t.Fatalf("atomic online drain lost: drains=%d reports=%v", reporter.drains, block.Header.OnlineReports)
	}
	cached, ok := GetCachedBlock(block.ID)
	if !ok || !reflect.DeepEqual(cached.Header.OnlineReports, reporter.online) {
		t.Fatalf("proposal cache lost online reports: %v", cached)
	}
	if err := ValidateBlockCommitment(cached); err != nil {
		t.Fatalf("proposer did not bind online reports before signing/caching: %v", err)
	}
}

func TestOnlineReportsHeaderShape(t *testing.T) {
	valid := types.BlockHeader{SampledReports: []uint32{1, 2, 3}, OfflineReports: []uint32{2}, OnlineReports: []uint32{1, 3}}
	if err := validateLivenessReportShape(valid); err != nil {
		t.Fatalf("valid three-list header rejected: %v", err)
	}
	for name, online := range map[string][]uint32{
		"unsorted": {3, 1}, "duplicate": {1, 1}, "outside sampled": {4}, "overlaps offline": {2},
	} {
		t.Run(name, func(t *testing.T) {
			header := valid
			header.OnlineReports = online
			if err := validateLivenessReportShape(header); err == nil {
				t.Fatalf("malformed online reports accepted: %v", online)
			}
		})
	}
	oversized := valid
	oversized.OnlineReports = make([]uint32, protocol.MaxLivenessReportsPerBlock+1)
	if err := validateLivenessReportShape(oversized); err == nil {
		t.Fatal("oversized online reports accepted")
	}
}

func TestOnlineReportsAdapterAndPendingBlockRoundTrip(t *testing.T) {
	block := commitmentTestBlock(t, commitmentTestTx("online-tx"))
	block.Header.OnlineReports = []uint32{1, 3}
	adapter := NewConsensusAdapter(nil)
	converted, err := adapter.DBBlockToConsensusWithPayload(block)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(converted.Header.OnlineReports, []uint32{1, 3}) {
		t.Fatalf("pb-to-types lost online reports: %v", converted.Header.OnlineReports)
	}
	converted.ID = converted.Header.ComputeIDForChain(pb.GetDefaultChainID())
	block.BlockHash = converted.ID
	for name, rebuilt := range map[string]*pb.Block{
		"adapter":        adapter.ConsensusBlockToDB(converted, block.Body),
		"pending buffer": (&PendingBlockBuffer{}).buildFullPbBlock(converted, block.Body),
	} {
		t.Run(name, func(t *testing.T) {
			if !reflect.DeepEqual(rebuilt.Header.OnlineReports, []uint32{1, 3}) {
				t.Fatalf("online reports lost: %v", rebuilt.Header.OnlineReports)
			}
			if err := ValidateBlockCommitment(rebuilt); err != nil {
				t.Fatalf("rebuilt online commitment rejected: %v", err)
			}
			rebuilt.Header.OnlineReports[0] = 3
			if converted.Header.OnlineReports[0] != 1 {
				t.Fatal("rebuilt header aliases the source online slice")
			}
		})
	}
	block.Header.OnlineReports[0] = 9
	if converted.Header.OnlineReports[0] != 1 {
		t.Fatal("converted header aliases the protobuf online slice")
	}
}

func TestOnlineReportsCacheFallbackAndProposalIsolation(t *testing.T) {
	const id = "online-reports-cache-fallback"
	defer clearCachedBlockForTest(id)
	cached := &pb.Block{BlockHash: id, Header: &pb.BlockHeader{
		Height: blockCacheAcceptedHeight.Load(), SampledReports: []uint32{1, 2}, OfflineReports: []uint32{2}, OnlineReports: []uint32{1},
	}}
	if !CacheBlock(cached) {
		t.Fatal("failed to cache online header")
	}
	rebuilt := (&ConsensusAdapter{}).ConsensusBlockToDB(&types.Block{ID: id}, nil)
	if !reflect.DeepEqual(rebuilt.Header.OnlineReports, []uint32{1}) {
		t.Fatalf("cached online reports lost: %v", rebuilt.Header.OnlineReports)
	}
	rebuilt.Header.OnlineReports[0] = 99
	if cached.Header.OnlineReports[0] != 1 {
		t.Fatal("cache fallback returned an aliased online slice")
	}
	proposal := &types.Block{ID: id, Header: types.BlockHeader{OnlineReports: []uint32{1, 3}}}
	cloned := cloneLiveProposal(proposal)
	cloned.Header.OnlineReports[0] = 99
	if proposal.Header.OnlineReports[0] != 1 {
		t.Fatal("proposal clone shares online storage")
	}
	withoutOnline := *proposal
	withoutOnline.Header.OnlineReports = nil
	if got, want := proposalCacheSize(proposal)-proposalCacheSize(&withoutOnline), uint64(8); got != want {
		t.Fatalf("online cache byte accounting delta=%d want %d", got, want)
	}
}

func TestOnlineReportsTamperingInvalidatesFullCommitment(t *testing.T) {
	block := commitmentTestBlock(t)
	block.Header.OnlineReports = []uint32{1, 3}
	converted, err := (&ConsensusAdapter{}).DBBlockToConsensus(block)
	if err != nil {
		t.Fatal(err)
	}
	block.BlockHash = converted.Header.ComputeIDForChain(pb.GetDefaultChainID())
	if err := ValidateBlockCommitment(block); err != nil {
		t.Fatalf("valid online block rejected: %v", err)
	}
	for name, online := range map[string][]uint32{"remove": nil, "replace": {1}, "reorder": {3, 1}} {
		t.Run(name, func(t *testing.T) {
			changed := proto.Clone(block).(*pb.Block)
			changed.Header.OnlineReports = online
			if err := ValidateBlockCommitment(changed); err == nil {
				t.Fatalf("online header tampering accepted: %v", online)
			}
		})
	}
}
