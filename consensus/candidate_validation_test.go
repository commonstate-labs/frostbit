package consensus

import (
	"bytes"
	"dex/db"
	"dex/interfaces"
	"dex/logs"
	statedb "dex/stateDB"
	"dex/types"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

func TestValidateCandidatePreStateRootRechecksFutureCandidateAtCurrentHeight(t *testing.T) {
	logger := logs.NewNodeLogger("candidate-root-test", 0)
	dbm, err := db.NewManager(filepath.Join(t.TempDir(), "db"), logger)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	defer dbm.Close()

	localRoot := dbm.GetStateFingerprint()
	if len(localRoot) != statedb.StateRootSize {
		t.Fatalf("unexpected local root length: got=%d want=%d", len(localRoot), statedb.StateRootSize)
	}
	genesis := &types.Block{ID: "genesis", Header: types.BlockHeader{Height: 0}}
	store := &RealBlockStore{
		dbManager:          dbm,
		blockCache:         map[string]*types.Block{"genesis": genesis},
		heightIndex:        map[uint64][]*types.Block{0: {genesis}},
		finalizedBlocks:    map[uint64]*types.Block{0: genesis},
		lastAccepted:       genesis,
		lastAcceptedHeight: 0,
	}

	valid := &types.Block{
		ID: "valid-current",
		Header: types.BlockHeader{
			Height:    1,
			ParentID:  "genesis",
			StateRoot: string(localRoot),
		},
	}
	if err := store.ValidateCandidatePreStateRoot(valid); err != nil {
		t.Fatalf("valid current candidate rejected: %v", err)
	}

	badRoot := bytes.Repeat([]byte{0x7f}, statedb.StateRootSize)
	future := &types.Block{
		ID: "bad-future",
		Header: types.BlockHeader{
			Height:    2,
			ParentID:  "parent-1",
			StateRoot: string(badRoot),
		},
	}
	if err := store.ValidateCandidatePreStateRoot(future); err != nil {
		t.Fatalf("future candidate should be deferred, got: %v", err)
	}

	parent := &types.Block{ID: "parent-1", Header: types.BlockHeader{Height: 1, ParentID: "genesis"}}
	store.mu.Lock()
	store.lastAccepted = parent
	store.lastAcceptedHeight = 1
	store.finalizedBlocks[1] = parent
	store.mu.Unlock()

	err = store.ValidateCandidatePreStateRoot(future)
	if !errors.Is(err, ErrInvalidCandidatePreStateRoot) {
		t.Fatalf("future candidate was not rejected after becoming current: %v", err)
	}
}

func TestRealBlockStoreDiscardCandidateKeepsFinalizedBlock(t *testing.T) {
	candidate := &types.Block{ID: "candidate", Header: types.BlockHeader{Height: 1}}
	store := &RealBlockStore{
		blockCache:      map[string]*types.Block{candidate.ID: candidate},
		heightIndex:     map[uint64][]*types.Block{1: {candidate}},
		finalizedBlocks: make(map[uint64]*types.Block),
		maxHeight:       1,
	}
	if !store.DiscardCandidate(1, candidate.ID) {
		t.Fatal("expected unfinalized candidate to be discarded")
	}
	if _, ok := store.blockCache[candidate.ID]; ok {
		t.Fatal("discarded candidate remains in block cache")
	}
	if got := store.heightIndex[1]; len(got) != 0 {
		t.Fatalf("discarded candidate remains in height index: %v", got)
	}
	if store.maxHeight != 0 {
		t.Fatalf("discard did not lower max height: %d", store.maxHeight)
	}

	store.blockCache[candidate.ID] = candidate
	store.heightIndex[1] = []*types.Block{candidate}
	store.finalizedBlocks[1] = candidate
	if store.DiscardCandidate(1, candidate.ID) {
		t.Fatal("finalized block must not be discarded")
	}
	if _, ok := store.blockCache[candidate.ID]; !ok {
		t.Fatal("finalized block was removed from block cache")
	}
}

type candidateGateTestStore struct {
	interfaces.BlockStore
	mu            sync.Mutex
	invalid       map[string]bool
	discarded     map[string]bool
	finalizeErr   error
	validateCalls int
}

func (s *candidateGateTestStore) ValidateCandidatePreStateRoot(block *types.Block) error {
	s.mu.Lock()
	s.validateCalls++
	invalid := block != nil && s.invalid[block.ID]
	s.mu.Unlock()
	if invalid {
		return fmt.Errorf("%w: block=%s", ErrInvalidCandidatePreStateRoot, block.ID)
	}
	return nil
}

func (s *candidateGateTestStore) validationCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.validateCalls
}

func (s *candidateGateTestStore) DiscardCandidate(_ uint64, blockID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.discarded == nil {
		s.discarded = make(map[string]bool)
	}
	s.discarded[blockID] = true
	return true
}

func (s *candidateGateTestStore) Get(id string) (*types.Block, bool) {
	s.mu.Lock()
	discarded := s.discarded[id]
	s.mu.Unlock()
	if discarded {
		return nil, false
	}
	return s.BlockStore.Get(id)
}

func (s *candidateGateTestStore) GetByHeight(height uint64) []*types.Block {
	blocks := s.BlockStore.GetByHeight(height)
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := blocks[:0]
	for _, block := range blocks {
		if block != nil && !s.discarded[block.ID] {
			kept = append(kept, block)
		}
	}
	return kept
}

func (s *candidateGateTestStore) SetFinalized(height uint64, blockID string) error {
	if s.finalizeErr != nil {
		return s.finalizeErr
	}
	return s.BlockStore.SetFinalized(height, blockID)
}

type candidateGateTestTransport struct {
	mu   sync.Mutex
	sent []types.Message
}

func (t *candidateGateTestTransport) Send(_ types.NodeID, msg types.Message) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sent = append(t.sent, msg)
	return nil
}
func (t *candidateGateTestTransport) Receive() <-chan types.Message                { return nil }
func (t *candidateGateTestTransport) Broadcast(types.Message, []types.NodeID)      {}
func (t *candidateGateTestTransport) SamplePeers(types.NodeID, int) []types.NodeID { return nil }
func (t *candidateGateTestTransport) GetAllPeers(types.NodeID) []types.NodeID      { return nil }

func (t *candidateGateTestTransport) lastMessage() types.Message {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.sent) == 0 {
		return types.Message{}
	}
	return t.sent[len(t.sent)-1]
}

func newCandidateGateTestEngine(store interfaces.BlockStore, blockID string, confidence int) *SnowmanEngine {
	events := NewEventBus()
	engine := NewSnowmanEngine(
		"node-test",
		store,
		&ConsensusConfig{K: 1, AlphaPreference: 1, AlphaConfidence: 1, Beta: 2},
		events,
		logs.NewNodeLogger("candidate-gate-test", 0),
	).(*SnowmanEngine)
	sb := NewSnowball(events)
	sb.AddCandidate(blockID)
	sb.state.Confidence = confidence
	sb.lastVotes[blockID] = 1
	engine.snowballs[1] = sb
	return engine
}

type candidateGatePreferenceEngine struct {
	interfaces.ConsensusEngine
	preference           string
	finalizingPreference string
	preferenceCalls      int
}

func (e *candidateGatePreferenceEngine) GetPreference(uint64) string {
	e.preferenceCalls++
	return e.preference
}

func (e *candidateGatePreferenceEngine) finalizingBlockID(uint64) string {
	return e.finalizingPreference
}

func TestSendChitsSkipsRevalidationForExactFinalizingPreference(t *testing.T) {
	baseStore := NewMemoryBlockStore()
	block := &types.Block{ID: "finalizing-block", Header: types.BlockHeader{Height: 1, ParentID: "genesis"}}
	if _, err := baseStore.Add(block); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	store := &candidateGateTestStore{BlockStore: baseStore}
	engine := newCandidateGateTestEngine(store, block.ID, 3)
	reserved, _ := engine.reserveFinalization(1, block.ID)
	if !reserved {
		t.Fatal("failed to reserve finalizing preference")
	}
	defer engine.releaseFinalization(1, block.ID)

	transport := &candidateGateTestTransport{}
	handler := NewMessageHandler(
		"node-test",
		false,
		transport,
		store,
		engine,
		NewEventBus(),
		&ConsensusConfig{},
		logs.NewNodeLogger("candidate-gate-test", 0),
	)
	handler.SetLivenessTracker(verifiedParticipantTrackerForTest())

	handler.sendChits("peer", 8, 1, nil, 0, nil)
	msg := transport.lastMessage()
	if msg.PreferredID != block.ID {
		t.Fatalf("finalizing preference = %q, want %q", msg.PreferredID, block.ID)
	}
	if msg.AcceptedID != "genesis" || msg.AcceptedHeight != 0 {
		t.Fatalf("finalizing preference was exposed as accepted: id=%q height=%d", msg.AcceptedID, msg.AcceptedHeight)
	}
	if got := store.validationCount(); got != 0 {
		t.Fatalf("exact finalizing preference was revalidated %d times", got)
	}
}

func TestSendChitsRevalidatesPreferenceAfterFinalizingReservationReleased(t *testing.T) {
	baseStore := NewMemoryBlockStore()
	block := &types.Block{ID: "released-finalizing-block", Header: types.BlockHeader{Height: 1, ParentID: "genesis"}}
	if _, err := baseStore.Add(block); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	store := &candidateGateTestStore{BlockStore: baseStore}
	engine := newCandidateGateTestEngine(store, block.ID, 3)
	reserved, _ := engine.reserveFinalization(1, block.ID)
	if !reserved {
		t.Fatal("failed to reserve finalizing preference")
	}
	engine.releaseFinalization(1, block.ID)

	transport := &candidateGateTestTransport{}
	handler := NewMessageHandler(
		"node-test",
		false,
		transport,
		store,
		engine,
		NewEventBus(),
		&ConsensusConfig{},
		logs.NewNodeLogger("candidate-gate-test", 0),
	)
	handler.SetLivenessTracker(verifiedParticipantTrackerForTest())

	handler.sendChits("peer", 9, 1, nil, 0, nil)
	if got := transport.lastMessage().PreferredID; got != block.ID {
		t.Fatalf("released preference = %q, want %q", got, block.ID)
	}
	if got := store.validationCount(); got == 0 {
		t.Fatal("released finalizing preference bypassed normal validation")
	}
}

func TestSendChitsUsesExactFinalizingIDInsteadOfDifferentEnginePreference(t *testing.T) {
	baseStore := NewMemoryBlockStore()
	finalizing := &types.Block{ID: "finalizing-a", Header: types.BlockHeader{Height: 1, ParentID: "genesis"}}
	other := &types.Block{ID: "other-preference-b", Header: types.BlockHeader{Height: 1, ParentID: "genesis"}}
	for _, block := range []*types.Block{finalizing, other} {
		if _, err := baseStore.Add(block); err != nil {
			t.Fatalf("Add %s failed: %v", block.ID, err)
		}
	}
	store := &candidateGateTestStore{BlockStore: baseStore}
	engine := &candidateGatePreferenceEngine{
		preference:           other.ID,
		finalizingPreference: finalizing.ID,
	}
	transport := &candidateGateTestTransport{}
	handler := NewMessageHandler(
		"node-test",
		false,
		transport,
		store,
		engine,
		NewEventBus(),
		&ConsensusConfig{},
		logs.NewNodeLogger("candidate-gate-test", 0),
	)
	handler.SetLivenessTracker(verifiedParticipantTrackerForTest())

	handler.sendChits("peer", 10, 1, nil, 0, nil)
	if got := transport.lastMessage().PreferredID; got != finalizing.ID {
		t.Fatalf("preference = %q, want exact finalizing ID %q", got, finalizing.ID)
	}
	if got := store.validationCount(); got != 0 {
		t.Fatalf("exact finalizing preference was revalidated %d times", got)
	}
	if engine.preferenceCalls != 0 {
		t.Fatalf("exact finalizing fast path called GetPreference %d times", engine.preferenceCalls)
	}
}

func TestSendChitsAbstainsWhenExactFinalizingPreferenceIsUnavailable(t *testing.T) {
	tests := []struct {
		name         string
		finalizing   *types.Block
		finalizingID string
	}{
		{name: "missing", finalizingID: "missing-finalizing"},
		{
			name:         "wrong parent",
			finalizing:   &types.Block{ID: "wrong-parent-finalizing", Header: types.BlockHeader{Height: 1, ParentID: "not-genesis"}},
			finalizingID: "wrong-parent-finalizing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseStore := NewMemoryBlockStore()
			ordinary := &types.Block{ID: "ordinary-" + tt.name, Header: types.BlockHeader{Height: 1, ParentID: "genesis"}}
			if _, err := baseStore.Add(ordinary); err != nil {
				t.Fatalf("Add ordinary block failed: %v", err)
			}
			if tt.finalizing != nil {
				if _, err := baseStore.Add(tt.finalizing); err != nil {
					t.Fatalf("Add finalizing block failed: %v", err)
				}
			}
			store := &candidateGateTestStore{BlockStore: baseStore}
			engine := &candidateGatePreferenceEngine{
				preference:           ordinary.ID,
				finalizingPreference: tt.finalizingID,
			}
			transport := &candidateGateTestTransport{}
			handler := NewMessageHandler(
				"node-test",
				false,
				transport,
				store,
				engine,
				NewEventBus(),
				&ConsensusConfig{},
				logs.NewNodeLogger("candidate-gate-test", 0),
			)
			handler.SetLivenessTracker(verifiedParticipantTrackerForTest())

			handler.sendChits("peer", 11, 1, nil, 0, nil)
			if got := transport.lastMessage().PreferredID; got != "" {
				t.Fatalf("invalid finalizing preference fell back to %q", got)
			}
			if got := store.validationCount(); got != 0 {
				t.Fatalf("invalid finalizing preference invoked validator %d times", got)
			}
			if engine.preferenceCalls != 0 {
				t.Fatalf("invalid finalizing preference fell through to GetPreference %d times", engine.preferenceCalls)
			}
		})
	}
}

func TestSendChitsAbstainsAndClearsInvalidPreference(t *testing.T) {
	baseStore := NewMemoryBlockStore()
	bad := &types.Block{ID: "bad-root", Header: types.BlockHeader{Height: 1, ParentID: "genesis"}}
	if _, err := baseStore.Add(bad); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	store := &candidateGateTestStore{
		BlockStore: baseStore,
		invalid:    map[string]bool{bad.ID: true},
		discarded:  make(map[string]bool),
	}
	engine := newCandidateGateTestEngine(store, bad.ID, 3)
	transport := &candidateGateTestTransport{}
	handler := NewMessageHandler(
		"node-test",
		false,
		transport,
		store,
		engine,
		NewEventBus(),
		&ConsensusConfig{},
		logs.NewNodeLogger("candidate-gate-test", 0),
	)
	handler.SetLivenessTracker(verifiedParticipantTrackerForTest())

	handler.sendChits("peer", 7, 1, nil, 0, nil)
	if got := transport.lastMessage().PreferredID; got != "" {
		t.Fatalf("voted for invalid candidate: %s", got)
	}
	if got := engine.GetPreference(1); got != "" {
		t.Fatalf("invalid preference was not cleared: %s", got)
	}
	if got := engine.snowballs[1].GetConfidence(); got != 0 {
		t.Fatalf("invalid preference confidence was not cleared: %d", got)
	}
	store.mu.Lock()
	discarded := store.discarded[bad.ID]
	store.mu.Unlock()
	if !discarded {
		t.Fatal("invalid candidate was not discarded")
	}
}

func TestDeterministicFinalizationFailureClearsPreference(t *testing.T) {
	baseStore := NewMemoryBlockStore()
	bad := &types.Block{ID: "bad-finalize-root", Header: types.BlockHeader{Height: 1, ParentID: "genesis"}}
	if _, err := baseStore.Add(bad); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	store := &candidateGateTestStore{
		BlockStore:  baseStore,
		discarded:   make(map[string]bool),
		finalizeErr: fmt.Errorf("%w: block=%s", ErrInvalidCandidatePreStateRoot, bad.ID),
	}
	engine := newCandidateGateTestEngine(store, bad.ID, 3)

	engine.finalizeBlock(1, bad.ID, nil)
	if got := engine.GetPreference(1); got != "" {
		t.Fatalf("invalid finalization preference was not cleared: %s", got)
	}
	if got := engine.snowballs[1].GetConfidence(); got != 0 {
		t.Fatalf("invalid finalization confidence was not cleared: %d", got)
	}
	store.mu.Lock()
	discarded := store.discarded[bad.ID]
	store.mu.Unlock()
	if !discarded {
		t.Fatal("deterministically invalid finalized candidate was not discarded")
	}
}

var _ interfaces.ConsensusEngine = (*SnowmanEngine)(nil)
var _ interfaces.Transport = (*candidateGateTestTransport)(nil)
