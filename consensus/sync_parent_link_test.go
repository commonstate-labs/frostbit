package consensus

import (
	"dex/interfaces"
	"dex/logs"
	"dex/pb"
	"dex/types"
	"testing"
)

                                                                 
                                                                                               
                                                    
                                                                                                      
                                                                     
                                                          

                                                                                                
type stubBlockStore struct {
	lastID     string
	lastHeight uint64
	blocks     map[string]*types.Block
}

func (s *stubBlockStore) Add(*types.Block) (bool, error)               { return false, nil }
func (s *stubBlockStore) Get(id string) (*types.Block, bool)           { b, ok := s.blocks[id]; return b, ok }
func (s *stubBlockStore) GetByHeight(uint64) []*types.Block            { return nil }
func (s *stubBlockStore) GetLastAccepted() (string, uint64)            { return s.lastID, s.lastHeight }
func (s *stubBlockStore) GetFinalizedAtHeight(uint64) (*types.Block, bool) { return nil, false }
func (s *stubBlockStore) GetBlocksFromHeight(uint64, uint64) []*types.Block { return nil }
func (s *stubBlockStore) GetCurrentHeight() uint64                     { return s.lastHeight }
func (s *stubBlockStore) GetPendingBlocksCount() int                   { return 0 }
func (s *stubBlockStore) GetPendingBlocks() []*types.Block             { return nil }
func (s *stubBlockStore) SetFinalized(uint64, string) error            { return nil }

var _ interfaces.BlockStore = (*stubBlockStore)(nil)

func newParentLinkSyncManager(store *stubBlockStore) *SyncManager {
	sm := &SyncManager{
		nodeID:      "node-self",
		store:       store,
		Logger:      logs.NewNodeLogger("sync-parent-test", 0),
		PeerHeights: map[types.NodeID]uint64{},
	}
	sm.peerRep = sm.newPeerBanTracker()
	return sm
}

func mkBlock(height uint64, parentID string) *types.Block {
	return &types.Block{
		Header: types.BlockHeader{Height: height, ParentID: parentID, Proposer: "p"},
	}
}

func TestVerifyParentLink_RejectsWrongParentAtLastAcceptedPlusOne(t *testing.T) {
	store := &stubBlockStore{lastID: "blk-accepted-100", lastHeight: 100, blocks: map[string]*types.Block{}}
	sm := newParentLinkSyncManager(store)

	                                                                
	bad := mkBlock(101, "wrong-parent")
	if sm.verifyParentLink(bad, "peer-attacker") {
		t.Fatal("verifyParentLink should reject block whose parentID != lastAccepted")
	}
}

func TestVerifyParentLink_AcceptsCorrectParentAtLastAcceptedPlusOne(t *testing.T) {
	store := &stubBlockStore{lastID: "blk-accepted-100", lastHeight: 100, blocks: map[string]*types.Block{}}
	sm := newParentLinkSyncManager(store)

	ok := mkBlock(101, "blk-accepted-100")
	if !sm.verifyParentLink(ok, "peer-honest") {
		t.Fatal("correct parent link must pass")
	}
}

func TestVerifyParentLink_RejectsNonContiguousKnownParent(t *testing.T) {
	                                                                                                   
	parent := mkBlock(100, "blk-99")
	parent.ID = "blk-parent-100"
	store := &stubBlockStore{
		lastID:     "blk-accepted-50",
		lastHeight: 50,                                            
		blocks:     map[string]*types.Block{"blk-parent-100": parent},
	}
	sm := newParentLinkSyncManager(store)

	bad := mkBlock(110, "blk-parent-100")
	if sm.verifyParentLink(bad, "peer-attacker") {
		t.Fatal("must reject when parent.Height+1 != block.Height")
	}
}

func TestVerifyParentLink_PassesWhenParentNotYetLocal(t *testing.T) {
	                                                                                
	store := &stubBlockStore{lastID: "blk-50", lastHeight: 50, blocks: map[string]*types.Block{}}
	sm := newParentLinkSyncManager(store)

	blk := mkBlock(110, "blk-unknown-parent")
	if !sm.verifyParentLink(blk, "peer-honest") {
		t.Fatal("should pass (not yet determinable) when parent absent from local store")
	}
}

func TestVerifyParentLink_NilBlockIsSafe(t *testing.T) {
	store := &stubBlockStore{lastID: "x", lastHeight: 1, blocks: map[string]*types.Block{}}
	sm := newParentLinkSyncManager(store)
	if !sm.verifyParentLink(nil, "peer") {
		t.Fatal("nil block must not crash or fail")
	}
}

                                                                             
                                                                                                               
func TestVerifyParentLink_PeerBannedAfterRepeatedFailures(t *testing.T) {
	store := &stubBlockStore{lastID: "blk-100", lastHeight: 100, blocks: map[string]*types.Block{}}
	sm := newParentLinkSyncManager(store)

	peer := types.NodeID("peer-attacker")
	bad := mkBlock(101, "wrong-parent")
	for i := 0; i < peerBanFailThreshold; i++ {
		if sm.verifyParentLink(bad, peer) {
			t.Fatalf("round %d: bad parent must be rejected", i)
		}
	}
	if !sm.isPeerBanned(peer) {
		t.Fatalf("peer should be banned after %d failures", peerBanFailThreshold)
	}
	                              
	if sm.isPeerBanned("peer-honest") {
		t.Fatal("unrelated peer must not be banned")
	}
}

                                                                                                          
var _ = pb.BlockHeader{}
