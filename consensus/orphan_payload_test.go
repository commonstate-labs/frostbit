package consensus

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"dex/config"
	"dex/db"
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

func TestExactPayloadOrphanWaitsForFinalizedParent(t *testing.T) {
	store := &RealBlockStore{orphanBlocks: make(map[string]*types.Block)}
	child := &types.Block{
		ID:               "reordered-child",
		Header:           types.BlockHeader{Height: 10, ParentID: "parent-9"},
		CanonicalPayload: []byte("exact-body"),
	}
	store.mu.Lock()
	store.queueOrphanLocked(child)
	store.mu.Unlock()

	if ready := store.takeOrphansAfterFinalized("other-parent", 9); len(ready) != 0 {
		t.Fatalf("orphan released for wrong parent: %+v", ready)
	}
	ready := store.takeOrphansAfterFinalized("parent-9", 9)
	if len(ready) != 1 || ready[0] != child {
		t.Fatalf("orphan was not released after exact parent finalized: %+v", ready)
	}
	if store.orphanBytes != 0 || len(store.orphanOrder) != 0 {
		t.Fatalf("released orphan retained budget/order: bytes=%d order=%v", store.orphanBytes, store.orphanOrder)
	}
}

func TestExactPayloadOrphanBufferIsBounded(t *testing.T) {
	store := &RealBlockStore{orphanBlocks: make(map[string]*types.Block)}
	store.mu.Lock()
	for i := 0; i < maxPendingParentOrphans+20; i++ {
		store.queueOrphanLocked(&types.Block{
			ID:               fmt.Sprintf("orphan-%03d", i),
			Header:           types.BlockHeader{Height: 2, ParentID: "missing-parent"},
			CanonicalPayload: []byte("body"),
		})
	}
	got := len(store.orphanBlocks)
	store.mu.Unlock()
	if got != maxPendingParentOrphans {
		t.Fatalf("orphan buffer size=%d, want %d", got, maxPendingParentOrphans)
	}
}

func TestExactPayloadOrphanBufferEnforcesByteBudget(t *testing.T) {
	store := &RealBlockStore{
		orphanBlocks: make(map[string]*types.Block),
		orphanLimit:  10,
	}
	first := &types.Block{
		ID:               "first",
		Header:           types.BlockHeader{Height: 2, ParentID: "missing-parent"},
		CanonicalPayload: []byte("123456"),
	}
	second := &types.Block{
		ID:               "second",
		Header:           types.BlockHeader{Height: 2, ParentID: "missing-parent"},
		CanonicalPayload: []byte("abcdef"),
	}

	store.mu.Lock()
	store.queueOrphanLocked(first)
	store.queueOrphanLocked(second)
	store.mu.Unlock()

	if _, exists := store.orphanBlocks[first.ID]; exists {
		t.Fatal("oldest orphan was not evicted to satisfy byte budget")
	}
	if got := store.orphanBlocks[second.ID]; got != second {
		t.Fatalf("new orphan missing after byte-budget eviction: %+v", got)
	}
	if store.orphanBytes != len(second.CanonicalPayload) {
		t.Fatalf("orphan bytes=%d, want %d", store.orphanBytes, len(second.CanonicalPayload))
	}
}

func TestExactPayloadOrphanReplacementDoesNotDoubleCount(t *testing.T) {
	store := &RealBlockStore{
		orphanBlocks: make(map[string]*types.Block),
		orphanLimit:  16,
	}
	original := &types.Block{
		ID:               "same-id",
		Header:           types.BlockHeader{Height: 2, ParentID: "parent-1"},
		CanonicalPayload: []byte("1234"),
	}
	other := &types.Block{
		ID:               "other-id",
		Header:           types.BlockHeader{Height: 2, ParentID: "parent-1"},
		CanonicalPayload: []byte("abc"),
	}
	replacement := &types.Block{
		ID:               original.ID,
		Header:           original.Header,
		CanonicalPayload: []byte("12345"),
	}

	store.mu.Lock()
	store.queueOrphanLocked(original)
	store.queueOrphanLocked(other)
	store.queueOrphanLocked(replacement)
	store.mu.Unlock()

	if len(store.orphanBlocks) != 2 || len(store.orphanOrder) != 2 {
		t.Fatalf("replacement duplicated queue entry: blocks=%d order=%v", len(store.orphanBlocks), store.orphanOrder)
	}
	wantBytes := len(replacement.CanonicalPayload) + len(other.CanonicalPayload)
	if store.orphanBytes != wantBytes {
		t.Fatalf("replacement bytes=%d, want %d", store.orphanBytes, wantBytes)
	}
	ready := store.takeOrphansAfterFinalized("parent-1", 1)
	if len(ready) != 2 || store.orphanBytes != 0 {
		t.Fatalf("finalized cleanup did not release replacement budget: ready=%d bytes=%d", len(ready), store.orphanBytes)
	}
}

func TestMissingParentBlockWithInvalidVRFIsNotQueued(t *testing.T) {
	keyManager := utils.NewKeyManager()
	if err := keyManager.InitKeyRandom(); err != nil {
		t.Fatal(err)
	}
	proposer := keyManager.GetAddress()
	genesis := &types.Block{ID: "genesis", Header: types.BlockHeader{Height: 0}}
	store := &RealBlockStore{
		blockCache:         map[string]*types.Block{"genesis": genesis},
		heightIndex:        map[uint64][]*types.Block{0: {genesis}},
		finalizedBlocks:    map[uint64]*types.Block{0: genesis},
		lastAccepted:       genesis,
		lastAcceptedHeight: 0,
		orphanBlocks:       make(map[string]*types.Block),
		orphanLimit:        defaultMaxPendingParentOrphanBytes,
	}
	header := types.BlockHeader{
		Height:    2,
		ParentID:  "missing-parent",
		StateRoot: string(make([]byte, 32)),
		TxHash:    emptyTxsHash,
		Proposer:  proposer,
		Miner:     keys.KeyNode() + proposer,
		PublicKey: proposalValidationBLSKey(t, keyManager),
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
	clearCachedBlockForTest(blockID)
	t.Cleanup(func() { clearCachedBlockForTest(blockID) })

	added, err := store.Add(candidate)
	if err == nil || !strings.Contains(err.Error(), "VRF validation failed") {
		t.Fatalf("invalid VRF returned added=%v err=%v", added, err)
	}
	if len(store.orphanBlocks) != 0 || store.orphanBytes != 0 {
		t.Fatalf("invalid VRF payload entered orphan buffer: blocks=%d bytes=%d", len(store.orphanBlocks), store.orphanBytes)
	}
}

func TestValidExactPayloadOrphanIsRetriedAfterParentFinalizes(t *testing.T) {
	logger := logs.NewNodeLogger("orphan-retry-test", 0)
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

	const parentID = "finalized-parent-1"
	vrfOutput, vrfProof, err := keyManager.GenerateVRF(2,
		0,
		parentID,
		types.NodeID(proposer),
	)
	if err != nil {
		t.Fatal(err)
	}
	stateRoot, _ := statedb.ReadFingerprintFromProvider(dbm)
	header := types.BlockHeader{
		Height:    2,
		ParentID:  parentID,
		StateRoot: string(stateRoot),
		TxHash:    emptyTxsHash,
		Proposer:  proposer,
		Miner:     keys.KeyNode() + proposer,
		Window:    0,
		VRFProof:  vrfProof,
		VRFOutput: vrfOutput,
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
	child, err := (&ConsensusAdapter{}).DBBlockToConsensusWithPayload(dbBlock)
	if err != nil {
		t.Fatal(err)
	}
	clearCachedBlockForTest(blockID)
	t.Cleanup(func() { clearCachedBlockForTest(blockID) })

	genesis := &types.Block{ID: "genesis", Header: types.BlockHeader{Height: 0}}
	store := &RealBlockStore{
		dbManager:          dbm,
		blockCache:         map[string]*types.Block{"genesis": genesis},
		heightIndex:        map[uint64][]*types.Block{0: {genesis}},
		finalizedBlocks:    map[uint64]*types.Block{0: genesis},
		lastAccepted:       genesis,
		lastAcceptedHeight: 0,
		orphanBlocks:       make(map[string]*types.Block),
		orphanLimit:        defaultMaxPendingParentOrphanBytes,
		windowConfig: config.WindowConfig{
			Enabled: true,
			Stages:  []config.WindowStage{{Probability: 1}},
		},
	}
	if added, err := store.Add(child); added || err == nil || !isMissingParentBlockError(err) {
		t.Fatalf("missing-parent child added=%v err=%v", added, err)
	}
	if len(store.orphanBlocks) != 1 || store.orphanBytes == 0 {
		t.Fatalf("valid reordered child was not buffered: blocks=%d bytes=%d", len(store.orphanBlocks), store.orphanBytes)
	}

	parent := &types.Block{ID: parentID, Header: types.BlockHeader{Height: 1, ParentID: "genesis"}}
	store.mu.Lock()
	store.blockCache[parentID] = parent
	store.heightIndex[1] = []*types.Block{parent}
	store.finalizedBlocks[1] = parent
	store.lastAccepted = parent
	store.lastAcceptedHeight = 1
	store.mu.Unlock()
	store.retryOrphansAfterFinalized(parentID, 1)

	store.mu.RLock()
	admitted := store.blockCache[blockID]
	remainingOrphans := len(store.orphanBlocks)
	remainingBytes := store.orphanBytes
	store.mu.RUnlock()
	if admitted == nil {
		t.Fatal("valid reordered child was not admitted after parent finalized")
	}
	if remainingOrphans != 0 || remainingBytes != 0 {
		t.Fatalf("retry retained orphan budget: blocks=%d bytes=%d", remainingOrphans, remainingBytes)
	}
}
