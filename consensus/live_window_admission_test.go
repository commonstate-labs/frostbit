package consensus

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"dex/config"
	"dex/db"
	"dex/interfaces"
	"dex/keys"
	"dex/liveness"
	"dex/logs"
	"dex/pb"
	statedb "dex/stateDB"
	"dex/types"
	"dex/utils"
	"dex/vm"

	"google.golang.org/protobuf/proto"
)

type liveWindowCountingStore struct {
	interfaces.BlockStore
	mu       sync.Mutex
	addCalls int
}

func (s *liveWindowCountingStore) PrevalidateDeferredLiveBlock(block *types.Block, acceptedHeight uint64) error {
	return forwardDeferredLiveBlockValidation(s.BlockStore, block, acceptedHeight)
}

func (s *liveWindowCountingStore) Add(block *types.Block) (bool, error) {
	s.mu.Lock()
	s.addCalls++
	s.mu.Unlock()
	return s.BlockStore.Add(block)
}

func (s *liveWindowCountingStore) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addCalls
}

func newLiveWindowHarness(t *testing.T) (*liveWindowCountingStore, *ProposalManager, *types.Block) {
	t.Helper()
	store := &liveWindowCountingStore{BlockStore: NewMemoryBlockStore()}
	pm := mustNewProposalManagerWithProposer(t,
		"window-gate-node",
		nil,
		store,
		&NodeConfig{ProposalInterval: time.Hour},
		NewEventBus(),
		nil,
		config.WindowConfig{
			Enabled: true,
			Stages: []config.WindowStage{
				{Duration: time.Hour},
				{Duration: time.Hour},
				{Duration: 0},
			},
		},
		logs.NewNodeLogger(t.Name(), 0),
	)
	pm.lastBlockTime = time.Now()
	block := &types.Block{
		ID: "block-1-proposer-w2-live-window-gate",
		Header: types.BlockHeader{
			Height:   1,
			ParentID: "genesis",
			Proposer: "proposer",
			Miner:    "node_proposer",
			Window:   2,
		},
	}
	return store, pm, block
}

func TestLiveWindowDeferralTraceDoesNotAdmitOrRepeat(t *testing.T) {
	t.Setenv("DEX_CONSENSUS_TRACE", "1")
	logger := logs.NewNodeLogger(t.Name(), 20)
	defer logs.SetThreadLogger(logger)()
	store, pm, block := newLiveWindowHarness(t)
	for i := 0; i < 2; i++ {
		added, deferred, err := pm.admitLiveBlock(block)
		if err != nil || added || !deferred || store.calls() != 0 {
			t.Fatalf("trace changed live admission: added=%v deferred=%v err=%v", added, deferred, err)
		}
	}
	count := 0
	for _, entry := range logger.GetBuffer() {
		if !strings.Contains(entry.Message, "event=live_block_deferred") {
			continue
		}
		count++
		for _, want := range []string{"height=1", "accepted=0", "block_window=2", "local_window=0", "reason=future_window"} {
			if !strings.Contains(entry.Message, want) {
				t.Errorf("missing %q in %s", want, entry.Message)
			}
		}
	}
	if count != 1 {
		t.Fatalf("want one new-cache diagnostic, got %d", count)
	}
}

func cachedLiveProposalCount(pm *ProposalManager) int {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return len(pm.cachedProposalIDs)
}

func openLiveWindow(pm *ProposalManager, window int) {
	pm.mu.Lock()
	pm.lastBlockTime = time.Now().Add(-3 * time.Hour)
	pm.mu.Unlock()
	pm.processCachedProposals(window)
}

func assertDeferredThenAdmitted(t *testing.T, store *liveWindowCountingStore, pm *ProposalManager, blockID string) {
	t.Helper()
	if got := store.calls(); got != 0 {
		t.Fatalf("future-window block reached BlockStore.Add early: calls=%d", got)
	}
	if got := cachedLiveProposalCount(pm); got != 1 {
		t.Fatalf("deferred proposal count=%d, want 1", got)
	}
	if _, exists := store.Get(blockID); exists {
		t.Fatal("future-window block entered candidate store before its local window")
	}

	openLiveWindow(pm, 2)
	if got := store.calls(); got != 1 {
		t.Fatalf("due proposal Add calls=%d, want 1", got)
	}
	if _, exists := store.Get(blockID); !exists {
		t.Fatal("proposal was not admitted after its local window opened")
	}
}

func TestLiveWindowGateDefersPutGossipAndHTTPAddBlock(t *testing.T) {
	t.Run("Put", func(t *testing.T) {
		store, pm, block := newLiveWindowHarness(t)
		h := &MessageHandler{
			nodeID:          "receiver",
			store:           store,
			proposalManager: pm,
			events:          NewEventBus(),
		}
		h.handlePut(types.Message{Type: types.MsgPut, From: "relay", Block: block})
		assertDeferredThenAdmitted(t, store, pm, block.ID)
	})

	t.Run("Gossip", func(t *testing.T) {
		store, pm, block := newLiveWindowHarness(t)
		gm := NewGossipManager(
			"receiver",
			&gossipRelayTestTransport{},
			store,
			&GossipConfig{Fanout: 1, Interval: time.Second},
			&gossipRelayTestEventBus{},
			logs.NewNodeLogger(t.Name(), 0),
		)
		gm.SetProposalManager(pm)
		gm.HandleGossip(types.Message{Type: types.MsgGossip, From: "relay", Block: block})
		assertDeferredThenAdmitted(t, store, pm, block.ID)
	})

	t.Run("HTTP AddBlock", func(t *testing.T) {
		store, pm, block := newLiveWindowHarness(t)
		manager := &ConsensusNodeManager{
			Node:    &Node{proposalManager: pm, events: NewEventBus()},
			store:   store,
			adapter: &ConsensusAdapter{},
		}
		dbBlock := &pb.Block{
			BlockHash: block.ID,
			Header: &pb.BlockHeader{
				Height:        block.Header.Height,
				PrevBlockHash: block.Header.ParentID,
				Miner:         block.Header.Miner,
				Window:        int32(block.Header.Window),
			},
		}
		if err := manager.AddBlock(dbBlock); err != nil {
			t.Fatalf("AddBlock returned error for deferred live block: %v", err)
		}
		assertDeferredThenAdmitted(t, store, pm, block.ID)
	})
}

func TestLiveWindowGateDefersFutureHeightUntilItBecomesNext(t *testing.T) {
	store, pm, _ := newLiveWindowHarness(t)
	child := &types.Block{
		ID: "block-2-proposer-w0-future-height",
		Header: types.BlockHeader{
			Height:   2,
			ParentID: "parent-1",
			Proposer: "proposer",
			Window:   0,
		},
	}
	added, deferred, err := pm.admitLiveBlock(child)
	if err != nil || added || !deferred {
		t.Fatalf("future-height admission = added=%t deferred=%t err=%v", added, deferred, err)
	}
	if got := store.calls(); got != 0 {
		t.Fatalf("future-height block reached Add early: calls=%d", got)
	}

	parent := &types.Block{ID: "parent-1", Header: types.BlockHeader{Height: 1, ParentID: "genesis"}}
	if added, err := store.BlockStore.Add(parent); err != nil || !added {
		t.Fatalf("add parent fixture: added=%t err=%v", added, err)
	}
	if err := store.BlockStore.SetFinalized(1, parent.ID); err != nil {
		t.Fatalf("finalize parent fixture: %v", err)
	}
	pm.mu.Lock()
	pm.lastBlockTime = time.Now()
	pm.mu.Unlock()
	pm.processCachedProposals(0)
	if got := store.calls(); got != 1 {
		t.Fatalf("current-height child Add calls=%d, want 1", got)
	}
	if _, exists := store.Get(child.ID); !exists {
		t.Fatal("future-height child was not admitted after its parent finalized")
	}
}

func TestHistoricalSyncStorePathRemainsWindowIndependent(t *testing.T) {
	store, _, block := newLiveWindowHarness(t)
	added, err := store.Add(block)
	if err != nil || !added {
		t.Fatalf("direct verified-sync Add must not use the live clock: added=%t err=%v", added, err)
	}
	if got := store.calls(); got != 1 {
		t.Fatalf("direct verified-sync Add calls=%d, want 1", got)
	}
}

func TestLiveWindowGateRejectsUnsignedPayloadBeforeCaching(t *testing.T) {
	keyManager := utils.NewKeyManager()
	if err := keyManager.InitKeyRandom(); err != nil {
		t.Fatal(err)
	}
	proposer := keyManager.GetAddress()
	header := types.BlockHeader{
		Height:    1,
		ParentID:  "genesis",
		StateRoot: string(make([]byte, 32)),
		TxHash:    emptyTxsHash,
		Proposer:  proposer,
		Miner:     keys.KeyNode() + proposer,
		Window:    2,
		PublicKey: proposalValidationBLSKey(t, keyManager),
	}
	blockID := header.ComputeIDForChain(pb.GetDefaultChainID())
	dbBlock := (&ConsensusAdapter{}).ConsensusBlockToDB(&types.Block{ID: blockID, Header: header}, []*pb.AnyTx{})
	candidate, err := (&ConsensusAdapter{}).DBBlockToConsensusWithPayload(dbBlock)
	if err != nil {
		t.Fatal(err)
	}

	genesis := &types.Block{ID: "genesis", Header: types.BlockHeader{Height: 0}}
	store := &RealBlockStore{
		blockCache:      map[string]*types.Block{"genesis": genesis},
		heightIndex:     map[uint64][]*types.Block{0: {genesis}},
		finalizedBlocks: map[uint64]*types.Block{0: genesis},
		lastAccepted:    genesis,
	}
	pm := mustNewProposalManagerWithProposer(t,
		"window-signature-gate",
		nil,
		store,
		&NodeConfig{ProposalInterval: time.Hour},
		NewEventBus(),
		nil,
		config.WindowConfig{
			Enabled: true,
			Stages: []config.WindowStage{
				{Duration: time.Hour},
				{Duration: time.Hour},
				{Duration: 0},
			},
		},
		logs.NewNodeLogger(t.Name(), 0),
	)

	added, deferred, err := pm.admitLiveBlock(candidate)
	if err == nil || !strings.Contains(err.Error(), "missing proposer signature") {
		t.Fatalf("unsigned future-window payload admission = added=%t deferred=%t err=%v", added, deferred, err)
	}
	if got := cachedLiveProposalCount(pm); got != 0 {
		t.Fatalf("unsigned future-window payload entered cache: count=%d", got)
	}
}

func TestLiveWindowGateCachesOnlyAuthenticatedEligibleFutureWindow(t *testing.T) {
	logger := logs.NewNodeLogger(t.Name(), 0)
	dbm, err := db.NewManager(filepath.Join(t.TempDir(), "db"), logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dbm.Close() })

	keyManager := utils.NewKeyManager()
	if err := keyManager.InitKeyRandom(); err != nil {
		t.Fatal(err)
	}
	proposer := keyManager.GetAddress()
	publicKey := proposalValidationBLSKey(t, keyManager)
	accountRaw, err := proto.Marshal(&pb.Account{
		Address: proposer,
		Index:   7,
		IsMiner: true,
		PublicKeys: &pb.PublicKeys{Keys: map[int32][]byte{
			int32(pb.SignAlgo_SIGN_ALGO_BLS_BN256_G2): publicKey,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	livenessRaw, err := json.Marshal(&liveness.MinerInfo{
		Index:   7,
		Address: proposer,
		State:   liveness.StateActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dbm.SyncStateUpdates(1, []interface{}{
		&vm.WriteOp{Key: keys.KeyAccount(proposer), Value: accountRaw},
		&vm.WriteOp{Key: keys.KeyLivenessMiner(7), Value: livenessRaw},
	}); err != nil {
		t.Fatal(err)
	}

	output, proof, err := keyManager.GenerateVRF(1, 2, "genesis", types.NodeID(proposer))
	if err != nil {
		t.Fatal(err)
	}
	stateRoot, _ := statedb.ReadFingerprintFromProvider(dbm)
	header := types.BlockHeader{
		Height:    1,
		ParentID:  "genesis",
		StateRoot: string(stateRoot),
		TxHash:    emptyTxsHash,
		Proposer:  proposer,
		Miner:     keys.KeyNode() + proposer,
		Window:    2,
		VRFProof:  proof,
		VRFOutput: output,
		PublicKey: publicKey,
	}
	blockID := header.ComputeIDForChain(pb.GetDefaultChainID())
	minerSignature, err := signBlockProposal(keyManager, blockID)
	if err != nil {
		t.Fatal(err)
	}
	dbBlock := (&ConsensusAdapter{}).ConsensusBlockToDB(&types.Block{
		ID: blockID, Header: header, MinerSignature: minerSignature,
	}, []*pb.AnyTx{})
	candidate, err := (&ConsensusAdapter{}).DBBlockToConsensusWithPayload(dbBlock)
	if err != nil {
		t.Fatal(err)
	}

	genesis := &types.Block{ID: "genesis", Header: types.BlockHeader{Height: 0}}
	windowConfig := config.WindowConfig{
		Enabled: true,
		Stages: []config.WindowStage{
			{Duration: time.Hour, Probability: 1},
			{Duration: time.Hour, Probability: 1},
			{Duration: 0, Probability: 1},
		},
	}
	store := &RealBlockStore{
		dbManager:       dbm,
		blockCache:      map[string]*types.Block{"genesis": genesis},
		heightIndex:     map[uint64][]*types.Block{0: {genesis}},
		finalizedBlocks: map[uint64]*types.Block{0: genesis},
		lastAccepted:    genesis,
		windowConfig:    windowConfig,
	}
	pm := mustNewProposalManagerWithProposer(t,
		"window-authenticated-gate",
		nil,
		store,
		&NodeConfig{ProposalInterval: time.Hour},
		NewEventBus(),
		nil,
		windowConfig,
		logger,
	)

	added, deferred, err := pm.admitLiveBlock(candidate)
	if err != nil || added || !deferred {
		t.Fatalf("authenticated future-window admission = added=%t deferred=%t err=%v", added, deferred, err)
	}
	if got := cachedLiveProposalCount(pm); got != 1 {
		t.Fatalf("authenticated future-window proposal cache count=%d, want 1", got)
	}
	if _, exists := store.Get(blockID); exists {
		t.Fatal("authenticated future-window proposal entered candidate store before window 2")
	}
}
