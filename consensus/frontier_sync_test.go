package consensus

import (
	"bytes"
	"dex/interfaces"
	"dex/logs"
	"dex/pb"
	"dex/servicepeer"
	"dex/types"
	"dex/utils"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
)

type frontierCoreTestTransport struct {
	mu         sync.Mutex
	peers      []types.NodeID
	queries    chan AcceptedFrontierQuery
	queryPeers []types.NodeID
	sends      []types.NodeID
	sent       []types.Message
	failQuery  map[types.NodeID]bool
	failSend   map[types.NodeID]bool
	                                                                     
	                                                                        
	failServiceSend map[types.NodeID]bool
}

                                                                           
                                                                             
type boundedFrontierTestTransport struct {
	*frontierCoreTestTransport
}

                                                                             
                                                                               
                                                                               
                                                                           
type synchronousFrontierCallbackTransport struct {
	*frontierCoreTestTransport
	signers           map[types.NodeID]interfaces.NodeSigner
	failAfterCallback map[types.NodeID]bool
	targetBlockID     string
	belowTarget       bool
}

func (t *synchronousFrontierCallbackTransport) SendAcceptedFrontierQuery(peer types.NodeID, query AcceptedFrontierQuery, callback func(AcceptedFrontierVote)) error {
	t.mu.Lock()
	t.queryPeers = append(t.queryPeers, peer)
	signer := t.signers[peer]
	fail := t.failAfterCallback[peer]
	t.mu.Unlock()

	vote := AcceptedFrontierVote{
		Peer:             peer,
		RequestDigest:    ComputeAcceptedFrontierRequestDigest(query),
		Status:           AcceptedFrontierStatusOK,
		AcceptedHeight:   query.TargetHeight,
		ObservedAnchorID: query.AnchorBlockID,
		TargetHeight:     query.TargetHeight,
		TargetBlockID:    t.targetBlockID,
	}
	if t.belowTarget {
		vote.Status = AcceptedFrontierStatusBelowTarget
		vote.AcceptedHeight = query.AnchorHeight
		vote.TargetBlockID = ""
	}
	if signer != nil {
		signature, err := signer.Sign(ComputeAcceptedFrontierDigest(query, vote))
		if err != nil {
			return err
		}
		vote.Signature = signature
	}
	callback(vote)
	if fail {
		return fmt.Errorf("query failed after callback")
	}
	return nil
}

func (t *boundedFrontierTestTransport) SamplePeers(_ types.NodeID, count int) []types.NodeID {
	peers := append([]types.NodeID(nil), t.peers...)
	if count < len(peers) {
		peers = peers[:count]
	}
	return peers
}

func (t *frontierCoreTestTransport) Send(to types.NodeID, msg types.Message) error {
	t.mu.Lock()
	t.sends = append(t.sends, to)
	t.sent = append(t.sent, msg)
	fail := t.failSend[to] || (msg.ServiceSync && t.failServiceSend[to])
	t.mu.Unlock()
	if fail {
		return fmt.Errorf("send failed")
	}
	return nil
}

func (*frontierCoreTestTransport) Receive() <-chan types.Message           { return nil }
func (*frontierCoreTestTransport) Broadcast(types.Message, []types.NodeID) {}
func (t *frontierCoreTestTransport) SamplePeers(types.NodeID, int) []types.NodeID {
	return append([]types.NodeID(nil), t.peers...)
}
func (t *frontierCoreTestTransport) GetAllPeers(types.NodeID) []types.NodeID {
	return append([]types.NodeID(nil), t.peers...)
}
func (t *frontierCoreTestTransport) SendAcceptedFrontierQuery(peer types.NodeID, query AcceptedFrontierQuery, _ func(AcceptedFrontierVote)) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.failQuery[peer] {
		return fmt.Errorf("query failed")
	}
	t.queryPeers = append(t.queryPeers, peer)
	if t.queries != nil {
		t.queries <- query
	}
	return nil
}

type frontierCoreTestStore struct {
	mu             sync.Mutex
	blocks         map[string]*types.Block
	finalized      map[uint64]*types.Block
	lastID         string
	lastHeight     uint64
	calls          []string
	finalizedReads []uint64
}

                                                                      
                                                                              
                                                                              
type frontierCandidateTestStore struct {
	*frontierCoreTestStore
}

func (s *frontierCandidateTestStore) GetByHeight(height uint64) []*types.Block {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]*types.Block, 0)
	for _, block := range s.blocks {
		if block != nil && block.Header.Height == height {
			result = append(result, block)
		}
	}
	return result
}

type frontierCandidateValidationStore struct {
	*frontierCandidateTestStore
	err      error
	validate func(*types.Block) error
}

func (s *frontierCandidateValidationStore) ValidateCandidatePreStateRoot(block *types.Block) error {
	if s.validate != nil {
		return s.validate(block)
	}
	return s.err
}

func newFrontierCoreTestStore(anchorID string, anchorHeight uint64) *frontierCoreTestStore {
	anchor := &types.Block{ID: anchorID, Header: types.BlockHeader{Height: anchorHeight}}
	return &frontierCoreTestStore{
		blocks:     map[string]*types.Block{anchorID: anchor},
		finalized:  map[uint64]*types.Block{anchorHeight: anchor},
		lastID:     anchorID,
		lastHeight: anchorHeight,
	}
}

func (s *frontierCoreTestStore) Add(block *types.Block) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if block == nil {
		return false, fmt.Errorf("nil block")
	}
	if _, exists := s.blocks[block.ID]; exists {
		return false, nil
	}
	s.calls = append(s.calls, "add:"+block.ID)
	s.blocks[block.ID] = block
	return true, nil
}
func (s *frontierCoreTestStore) Get(id string) (*types.Block, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.blocks[id]
	return b, ok
}
func (s *frontierCoreTestStore) GetByHeight(height uint64) []*types.Block {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b, ok := s.finalized[height]; ok {
		return []*types.Block{b}
	}
	return nil
}
func (s *frontierCoreTestStore) GetLastAccepted() (string, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastID, s.lastHeight
}
func (s *frontierCoreTestStore) GetFinalizedAtHeight(height uint64) (*types.Block, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finalizedReads = append(s.finalizedReads, height)
	b, ok := s.finalized[height]
	return b, ok
}
func (s *frontierCoreTestStore) GetBlocksFromHeight(from, to uint64) []*types.Block {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]*types.Block, 0, to-from+1)
	for height := from; height <= to; height++ {
		if b, ok := s.finalized[height]; ok {
			result = append(result, b)
		}
	}
	return result
}
func (s *frontierCoreTestStore) GetCurrentHeight() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastHeight
}
func (*frontierCoreTestStore) GetPendingBlocksCount() int       { return 0 }
func (*frontierCoreTestStore) GetPendingBlocks() []*types.Block { return nil }
func (s *frontierCoreTestStore) SetFinalized(height uint64, blockID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	block, ok := s.blocks[blockID]
	if !ok {
		return fmt.Errorf("missing block %s", blockID)
	}
	if height != s.lastHeight+1 || block.Header.ParentID != s.lastID {
		return fmt.Errorf("non-contiguous finalize")
	}
	s.calls = append(s.calls, "finalize:"+blockID)
	s.finalized[height] = block
	s.lastID = blockID
	s.lastHeight = height
	return nil
}

var _ interfaces.BlockStore = (*frontierCoreTestStore)(nil)

type frontierCoreTestEventBus struct {
	mu     sync.Mutex
	events []interfaces.Event
}

func (*frontierCoreTestEventBus) Subscribe(types.EventType, interfaces.EventHandler) {}
func (b *frontierCoreTestEventBus) Publish(event interfaces.Event) {
	b.mu.Lock()
	b.events = append(b.events, event)
	b.mu.Unlock()
}
func (b *frontierCoreTestEventBus) PublishAsync(event interfaces.Event) { b.Publish(event) }

func newFrontierCoreSyncManager(store interfaces.BlockStore, transport interfaces.Transport, k, alpha int) *SyncManager {
	return NewSyncManager(
		"self",
		transport,
		store,
		&SyncConfig{BatchSize: 50, Timeout: time.Second, FrontierPollTimeout: time.Second},
		&ConsensusConfig{K: k, AlphaPreference: alpha, AlphaConfidence: alpha},
		nil,
		logs.NewNodeLogger("frontier-core-test", 0),
	)
}

func newVerifiedFrontierServicePeer(t *testing.T, endpoint string, archiveFrom, archiveTo uint64, failureThreshold uint32) (*servicepeer.Store, types.NodeID) {
	t.Helper()
	signer := utils.NewKeyManager()
	if err := signer.InitKey(fmt.Sprintf("%064x", 987654)); err != nil {
		t.Fatalf("InitKey: %v", err)
	}
	const chainID uint64 = 5150
	store, err := servicepeer.NewStore(chainID, "", servicepeer.Config{
		CandidateCapacity: 4,
		VerifiedCapacity:  4,
		FailureThreshold:  failureThreshold,
		Random:            bytes.NewReader(bytes.Repeat([]byte{0x37}, 4096)),
	})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	record, err := servicepeer.NewSignedRecord(servicepeer.Record{
		ChainID:     chainID,
		Endpoint:    endpoint,
		ArchiveFrom: archiveFrom,
		ArchiveTo:   archiveTo,
		Sequence:    1,
	}, signer)
	if err != nil {
		t.Fatalf("NewSignedRecord: %v", err)
	}
	if _, err := store.UpsertCandidate(record); err != nil {
		t.Fatalf("UpsertCandidate: %v", err)
	}
	if err := store.MarkVerified(record.NodeID, record.Sequence, archiveTo); err != nil {
		t.Fatalf("MarkVerified: %v", err)
	}
	if err := store.MarkArchiveProof(record.NodeID, archiveFrom, archiveTo); err != nil {
		t.Fatalf("MarkArchiveProof: %v", err)
	}
	return store, types.NodeID(record.NodeID)
}

func TestFrontierStageRequiresExactlyKDistinctSampleAndAlphaDispatches(t *testing.T) {
	store := newFrontierCoreTestStore("block-5", 5)
	transport := &frontierCoreTestTransport{peers: []types.NodeID{"peer-1", "peer-2"}}
	sm := newFrontierCoreSyncManager(store, transport, 3, 2)
	session := &SyncSession{ID: 1, Stage: SyncStageFrontier, TargetHeight: 6, PeerAcceptedHeight: 6}
	sm.activeSession = session
	sm.Syncing = true

	serr := sm.execFrontierStage(session)
	if serr == nil || serr.Kind != SyncErrNetwork || !strings.Contains(serr.Message, "need K=3") {
		t.Fatalf("expected fixed-K sampling failure, got %#v", serr)
	}
	if len(transport.queryPeers) != 0 {
		t.Fatalf("queries must not dispatch with a shrunken sample, got %v", transport.queryPeers)
	}

	transport.peers = []types.NodeID{"peer-1", "peer-1", "peer-2"}
	serr = sm.execFrontierStage(session)
	if serr == nil || serr.Kind != SyncErrNetwork || !strings.Contains(serr.Message, "need K=3") {
		t.Fatalf("duplicate peer identities must not fill distinct frontier slots, got %#v", serr)
	}
	if len(transport.queryPeers) != 0 {
		t.Fatalf("duplicate sample must not dispatch a shrunken frontier poll, got %v", transport.queryPeers)
	}

	transport.peers = []types.NodeID{"peer-1", "peer-2", "peer-3"}
	transport.failQuery = map[types.NodeID]bool{"peer-3": true}
	serr = sm.execFrontierStage(session)
	if serr != nil {
		t.Fatalf("one synchronous dispatch failure must be an abstention when Alpha=2 remains possible: %v", serr)
	}
	if sm.frontierRound == nil || len(sm.frontierRound.sampled) != 3 || len(sm.frontierRound.dispatched) != 2 {
		t.Fatalf("round must preserve fixed K=3 and record two actual dispatches: %+v", sm.frontierRound)
	}

	                                                                       
	sm.frontierRound = nil
	transport.queryPeers = nil
	transport.failQuery = map[types.NodeID]bool{"peer-2": true, "peer-3": true}
	serr = sm.execFrontierStage(session)
	if serr == nil || serr.Kind != SyncErrNetwork || !strings.Contains(serr.Message, "need Alpha=2") {
		t.Fatalf("expected dispatch failure when fewer than Alpha peers can answer, got %#v", serr)
	}
	if got := len(transport.queryPeers); got != 1 {
		t.Fatalf("all fixed-K peers must be attempted even when only one dispatch succeeds, got %d", got)
	}
}

func TestFrontierRoundCapturesDynamicParamsAtAnchor(t *testing.T) {
	store := newFrontierCoreTestStore("block-5", 5)
	transport := &frontierCoreTestTransport{
		peers: []types.NodeID{"peer-1", "peer-2", "peer-3", "peer-4"},
	}
	sm := newFrontierCoreSyncManager(store, transport, 15, 12)
	tracker := &mutableConsensusSnapshotTracker{height: 5, count: 5}
	provider := NewConsensusParamsProvider(dynamicParamsTestBase())
	provider.SetLivenessTracker(tracker)
	sm.consensusParams = provider
	session := &SyncSession{ID: 46, Stage: SyncStageFrontier, TargetHeight: 6, PeerAcceptedHeight: 6}
	sm.activeSession = session
	sm.Syncing = true

	if serr := sm.execFrontierStage(session); serr != nil {
		t.Fatalf("dynamic frontier start failed: %v", serr)
	}
	if sm.frontierRound == nil || sm.frontierRound.k != 4 || sm.frontierRound.alpha != 3 {
		t.Fatalf("captured frontier tuple = %+v, want K=4 Alpha=3", sm.frontierRound)
	}

	                                                                           
	                                                       
	tracker.set(6, 20)
	if sm.frontierRound.k != 4 || sm.frontierRound.alpha != 3 {
		t.Fatalf("in-flight frontier tuple changed to K=%d Alpha=%d", sm.frontierRound.k, sm.frontierRound.alpha)
	}
	params, _, ok := provider.ParamsForAnchor(6)
	if !ok || params.K != 15 || params.AlphaConfidence != 12 {
		t.Fatalf("next anchor did not resolve production profile: params=%+v ok=%t", params, ok)
	}
}

func TestLightFrontierYieldsToLocalSnowmanCandidateBeforePoll(t *testing.T) {
	baseStore := newFrontierCoreTestStore("block-5", 5)
	baseStore.blocks["local-6"] = &types.Block{
		ID: "local-6",
		Header: types.BlockHeader{
			Height:   6,
			ParentID: "block-5",
		},
	}
	store := &frontierCandidateTestStore{frontierCoreTestStore: baseStore}
	transport := &frontierCoreTestTransport{
		peers: []types.NodeID{"peer-1", "peer-2", "peer-3"},
	}
	sm := newFrontierCoreSyncManager(store, transport, 3, 2)
	session := &SyncSession{
		ID:                 41,
		Stage:              SyncStageFrontier,
		TargetHeight:       6,
		PeerAcceptedHeight: 6,
		Light:              true,
	}
	sm.activeSession = session

	sm.runStateMachine(session)
	if sm.activeSession != nil || sm.lastSession == nil || sm.lastSession.Stage != SyncStageDone {
		t.Fatalf("local candidate should finish the light session: active=%+v last=%+v",
			sm.activeSession, sm.lastSession)
	}
	if len(transport.queryPeers) != 0 {
		t.Fatalf("light sync queried peers despite a local Snowman candidate: %v", transport.queryPeers)
	}
}

func TestLightCandidateOwnershipIgnoresOnlyDeterministicInvalidCandidate(t *testing.T) {
	newStore := func(err error, parentID string) *frontierCandidateValidationStore {
		if parentID == "" {
			parentID = "block-5"
		}
		baseStore := newFrontierCoreTestStore("block-5", 5)
		baseStore.blocks["local-6"] = &types.Block{
			ID: "local-6",
			Header: types.BlockHeader{
				Height:   6,
				ParentID: parentID,
			},
		}
		return &frontierCandidateValidationStore{
			frontierCandidateTestStore: &frontierCandidateTestStore{
				frontierCoreTestStore: baseStore,
			},
			err: err,
		}
	}

	tests := []struct {
		name     string
		err      error
		parentID string
		want     bool
	}{
		{name: "valid", want: true},
		{
			name: "deterministically invalid",
			err:  fmt.Errorf("%w: test candidate", ErrInvalidCandidatePreStateRoot),
			want: false,
		},
		{
			name: "transient local validation failure",
			err:  fmt.Errorf("StateDB temporarily unavailable"),
			want: true,
		},
		{
			name:     "wrong parent",
			parentID: "fork-5",
			want:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm := newFrontierCoreSyncManager(newStore(tt.err, tt.parentID), nil, 3, 2)
			if got := sm.hasLocalSnowmanCandidate("block-5", 6); got != tt.want {
				t.Fatalf("candidate ownership=%t, want %t", got, tt.want)
			}
		})
	}
}

func TestOrdinaryFrontierDoesNotYieldToPendingCandidate(t *testing.T) {
	baseStore := newFrontierCoreTestStore("block-5", 5)
	baseStore.blocks["local-6"] = &types.Block{
		ID: "local-6",
		Header: types.BlockHeader{
			Height:   6,
			ParentID: "block-5",
		},
	}
	store := &frontierCandidateTestStore{frontierCoreTestStore: baseStore}
	transport := &frontierCoreTestTransport{
		peers: []types.NodeID{"peer-1", "peer-2", "peer-3"},
	}
	sm := newFrontierCoreSyncManager(store, transport, 3, 2)
	session := &SyncSession{
		ID:                 42,
		Stage:              SyncStageFrontier,
		TargetHeight:       6,
		PeerAcceptedHeight: 6,
	}
	sm.activeSession = session
	sm.Syncing = true

	if serr := sm.execFrontierStage(session); serr != nil {
		t.Fatalf("ordinary frontier was changed by light-only ownership rule: %v", serr)
	}
	if len(transport.queryPeers) != 3 {
		t.Fatalf("ordinary frontier queried %d peers, want 3", len(transport.queryPeers))
	}
}

func TestFrontierRoundCompletionWaitsOnlyForSuccessfullyDispatchedPeers(t *testing.T) {
	round := &frontierRound{
		startedAt: time.Now(),
		sampled: map[types.NodeID]struct{}{
			"peer-1": {}, "peer-2": {}, "peer-3": {},
		},
		dispatched: map[types.NodeID]struct{}{
			"peer-1": {}, "peer-2": {},
		},
		responses: map[types.NodeID]AcceptedFrontierVote{
			"peer-1": {Peer: "peer-1"},
		},
	}
	if frontierRoundResponsesComplete(round) {
		t.Fatal("round must wait for the second successfully dispatched peer")
	}
	round.responses["peer-2"] = AcceptedFrontierVote{Peer: "peer-2"}
	if !frontierRoundResponsesComplete(round) {
		t.Fatal("synchronously failed peer-3 must be an abstention, not an impossible callback wait")
	}
}

func TestFrontierNoQuorumSummaryIsDeterministic(t *testing.T) {
	round := &frontierRound{
		sampled: map[types.NodeID]struct{}{
			"peer-1": {}, "peer-2": {}, "peer-3": {}, "peer-4": {}, "peer-5": {},
		},
		dispatched: map[types.NodeID]struct{}{
			"peer-1": {}, "peer-2": {}, "peer-3": {}, "peer-4": {},
		},
		responses: map[types.NodeID]AcceptedFrontierVote{
			"peer-3": {Status: AcceptedFrontierStatusOK, AcceptedHeight: 6, TargetBlockID: "block-z"},
			"peer-1": {Status: AcceptedFrontierStatusBelowTarget, AcceptedHeight: 5},
			"peer-2": {Status: AcceptedFrontierStatusBelowTarget, AcceptedHeight: 5},
		},
	}
	want := `sampled=5 dispatched=4 responses=3 missing=1 dispatch_failed=1 votes=[{status="below_target" acceptedHeight=5 targetBlockID="" count=2},{status="ok" acceptedHeight=6 targetBlockID="block-z" count=1}]`
	for i := 0; i < 20; i++ {
		if got := frontierNoQuorumSummary(round); got != want {
			t.Fatalf("non-deterministic or incomplete summary:\n got: %s\nwant: %s", got, want)
		}
	}
}

func TestFrontierTimeoutIgnoresRoundWhileDispatchIsInProgress(t *testing.T) {
	sm := newFrontierCoreSyncManager(newFrontierCoreTestStore("block-5", 5), nil, 3, 2)
	session := &SyncSession{ID: 18, Stage: SyncStageFrontier}
	round := &frontierRound{
		sessionID: session.ID,
		query:     AcceptedFrontierQuery{Nonce: bytes.Repeat([]byte{0x18}, AcceptedFrontierNonceSize)},
		                                                                         
		sampled:   map[types.NodeID]struct{}{"peer-1": {}, "peer-2": {}, "peer-3": {}},
		responses: make(map[types.NodeID]AcceptedFrontierVote),
	}
	sm.activeSession = session
	sm.frontierRound = round
	sm.Syncing = true

	sm.processAcceptedFrontierTimeout(time.Now().Add(24 * time.Hour))
	if sm.frontierRound != round || sm.activeSession != session || session.StageRetries != 0 {
		t.Fatalf("timeout tick must not retire a round before dispatch completes: round=%p active=%p retries=%d",
			sm.frontierRound, sm.activeSession, session.StageRetries)
	}
}

func TestFrontierSynchronousCallbacksCountOnlySuccessfulDispatches(t *testing.T) {
	signer1 := newAcceptedFrontierTestSigner(t)
	signer2 := newAcceptedFrontierTestSigner(t)
	peer1 := types.NodeID(signer1.GetAddress())
	peer2 := types.NodeID(signer2.GetAddress())
	failedPeer := types.NodeID("failed-peer")
	base := &frontierCoreTestTransport{peers: []types.NodeID{peer1, peer2, failedPeer}}
	transport := &synchronousFrontierCallbackTransport{
		frontierCoreTestTransport: base,
		signers: map[types.NodeID]interfaces.NodeSigner{
			peer1: signer1,
			peer2: signer2,
		},
		failAfterCallback: map[types.NodeID]bool{failedPeer: true},
		targetBlockID:     "block-6",
	}
	sm := newFrontierCoreSyncManager(newFrontierCoreTestStore("block-5", 5), transport, 3, 2)
	session := &SyncSession{ID: 19, Stage: SyncStageFrontier, TargetHeight: 6, PeerAcceptedHeight: 6}
	sm.activeSession = session
	sm.Syncing = true

	if serr := sm.execFrontierStage(session); serr != nil {
		t.Fatalf("Alpha successful synchronous dispatches must keep the fixed-K round viable: %v", serr)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		sm.Mu.RLock()
		stage := session.Stage
		targetID := session.FrontierTargetID
		supporters := make(map[types.NodeID]struct{}, len(session.frontierSupporters))
		for peer := range session.frontierSupporters {
			supporters[peer] = struct{}{}
		}
		round := sm.frontierRound
		sm.Mu.RUnlock()
		if stage == SyncStageBlockCatchUp {
			if targetID != "block-6" || len(supporters) != 2 {
				t.Fatalf("unexpected synchronous-callback quorum: target=%s supporters=%v", targetID, supporters)
			}
			if _, counted := supporters[failedPeer]; counted {
				t.Fatal("callback paired with a synchronous dispatch error must remain an abstention")
			}
			if round != nil {
				t.Fatal("quorum must retire the exact active frontier round")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for buffered synchronous callbacks; stage=%s", stage)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestLightFrontierNoQuorumStopsAfterOneRound(t *testing.T) {
	const (
		k     = 3
		alpha = 2
	)
	peers := make([]types.NodeID, 0, k)
	signers := make(map[types.NodeID]interfaces.NodeSigner, k)
	for i := 0; i < k; i++ {
		signer := newAcceptedFrontierTestSigner(t)
		peer := types.NodeID(signer.GetAddress())
		peers = append(peers, peer)
		signers[peer] = signer
	}
	base := &frontierCoreTestTransport{peers: peers}
	transport := &synchronousFrontierCallbackTransport{
		frontierCoreTestTransport: base,
		signers:                   signers,
		belowTarget:               true,
	}
	sm := newFrontierCoreSyncManager(newFrontierCoreTestStore("block-5", 5), transport, k, alpha)
	session := &SyncSession{
		ID:                 45,
		Stage:              SyncStageFrontier,
		TargetHeight:       6,
		PeerAcceptedHeight: 6,
		Light:              true,
	}
	sm.activeSession = session

	if serr := sm.execFrontierStage(session); serr != nil {
		t.Fatalf("light frontier failed before its fully answered round: %v", serr)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		sm.Mu.RLock()
		active := sm.activeSession
		last := sm.lastSession
		sm.Mu.RUnlock()
		if active == nil && last != nil {
			if last.Stage != SyncStageDone {
				t.Fatalf("not-ready light frontier ended as %s, want done", last.Stage)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for not-ready light frontier to finish")
		}
		time.Sleep(time.Millisecond)
	}

	time.Sleep(25 * time.Millisecond)
	base.mu.Lock()
	queryCount := len(base.queryPeers)
	base.mu.Unlock()
	if queryCount != k {
		t.Fatalf("not-ready light frontier sent %d queries, want exactly one K=%d round", queryCount, k)
	}
}

func TestFrontierStageReplacesBannedPeersBeforeFixingK(t *testing.T) {
	store := newFrontierCoreTestStore("block-5", 5)
	base := &frontierCoreTestTransport{peers: []types.NodeID{"banned", "peer-1", "peer-2", "peer-3"}}
	transport := &boundedFrontierTestTransport{frontierCoreTestTransport: base}
	sm := newFrontierCoreSyncManager(store, transport, 3, 2)
	session := &SyncSession{ID: 16, Stage: SyncStageFrontier, TargetHeight: 6, PeerAcceptedHeight: 6}
	sm.activeSession = session
	sm.Syncing = true
	for i := 0; i < peerBanFailThreshold; i++ {
		sm.recordPeerFailure("banned", "test ban")
	}

	if serr := sm.execFrontierStage(session); serr != nil {
		t.Fatalf("healthy replacements should preserve fixed K after filtering a banned peer: %v", serr)
	}
	base.mu.Lock()
	queried := append([]types.NodeID(nil), base.queryPeers...)
	base.mu.Unlock()
	want := []types.NodeID{"peer-1", "peer-2", "peer-3"}
	if fmt.Sprint(queried) != fmt.Sprint(want) {
		t.Fatalf("unexpected frontier sample after banned-peer replacement: got=%v want=%v", queried, want)
	}
}

func TestFrontierStageRejectsNonIntersectingQuorumThreshold(t *testing.T) {
	store := newFrontierCoreTestStore("block-5", 5)
	transport := &frontierCoreTestTransport{peers: []types.NodeID{"peer-1", "peer-2", "peer-3", "peer-4"}}
	sm := newFrontierCoreSyncManager(store, transport, 4, 2)
	session := &SyncSession{ID: 14, Stage: SyncStageFrontier, TargetHeight: 6, PeerAcceptedHeight: 6}
	sm.activeSession = session
	sm.Syncing = true

	serr := sm.execFrontierStage(session)
	if serr == nil || serr.Kind != SyncErrDeterministic || !strings.Contains(serr.Message, "K/2 < Alpha") {
		t.Fatalf("K=4 Alpha=2 must be rejected as a non-intersecting quorum, got %#v", serr)
	}
	if len(transport.queryPeers) != 0 {
		t.Fatalf("invalid quorum configuration must fail before dispatch, got %v", transport.queryPeers)
	}
}

func TestFrontierNetworkRetriesExhaustWithoutFallbackLoop(t *testing.T) {
	store := newFrontierCoreTestStore("block-5", 5)
	transport := &frontierCoreTestTransport{peers: []types.NodeID{"peer-1", "peer-2"}}
	sm := newFrontierCoreSyncManager(store, transport, 3, 2)
	session := &SyncSession{ID: 11, Stage: SyncStageFrontier, TargetHeight: 6, PeerAcceptedHeight: 6}
	sm.activeSession = session
	sm.Syncing = true
	done := make(chan struct{})
	go func() {
		sm.runStateMachine(session)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("frontier K shortage entered a retry/fallback loop")
	}
	if sm.activeSession != nil || sm.lastSession == nil || sm.lastSession.Stage != SyncStageFailed {
		t.Fatalf("exhausted frontier session must fail cleanly: active=%v last=%+v", sm.activeSession, sm.lastSession)
	}
}

func TestFrontierTimeoutLowersTargetToAlphaHighAndUsesFreshNonce(t *testing.T) {
	store := newFrontierCoreTestStore("block-5", 5)
	transport := &frontierCoreTestTransport{
		peers:   []types.NodeID{"peer-1", "peer-2", "peer-3"},
		queries: make(chan AcceptedFrontierQuery, 3),
	}
	sm := newFrontierCoreSyncManager(store, transport, 3, 2)
	session := &SyncSession{ID: 2, Stage: SyncStageFrontier, TargetHeight: 10, PeerAcceptedHeight: 10}
	oldNonce := bytes.Repeat([]byte{0x7a}, AcceptedFrontierNonceSize)
	oldQuery := AcceptedFrontierQuery{
		Requester:     sm.nodeID,
		Nonce:         oldNonce,
		AnchorHeight:  5,
		AnchorBlockID: "block-5",
		TargetHeight:  10,
	}
	sm.activeSession = session
	sm.Syncing = true
	sm.frontierRound = &frontierRound{
		sessionID:  session.ID,
		query:      oldQuery,
		startedAt:  time.Now().Add(-2 * time.Second),
		k:          3,
		alpha:      2,
		sampled:    map[types.NodeID]struct{}{"peer-1": {}, "peer-2": {}, "peer-3": {}},
		dispatched: map[types.NodeID]struct{}{"peer-1": {}, "peer-2": {}, "peer-3": {}},
		responses: map[types.NodeID]AcceptedFrontierVote{
			"peer-1": {Peer: "peer-1", Status: AcceptedFrontierStatusBelowTarget, AcceptedHeight: 7, ObservedAnchorID: "block-5", TargetHeight: 10},
			"peer-2": {Peer: "peer-2", Status: AcceptedFrontierStatusBelowTarget, AcceptedHeight: 6, ObservedAnchorID: "block-5", TargetHeight: 10},
		},
	}

	sm.processAcceptedFrontierTimeout(time.Now())
	select {
	case query := <-transport.queries:
		if query.TargetHeight != 6 {
			t.Fatalf("next target must be Alpha-th highest accepted height 6, got %d", query.TargetHeight)
		}
		if bytes.Equal(query.Nonce, oldNonce) {
			t.Fatal("lowered frontier round must use a fresh nonce")
		}
		if session.PeerAcceptedHeight != 6 {
			t.Fatalf("lowered Alpha height must replace the untrusted session hint, got %d", session.PeerAcceptedHeight)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for lowered frontier round")
	}
}

func TestFrontierSingleVoteCannotPollutePeerHeight(t *testing.T) {
	signer := newAcceptedFrontierTestSigner(t)
	peer := types.NodeID(signer.GetAddress())
	query := AcceptedFrontierQuery{
		Requester: "self", Nonce: bytes.Repeat([]byte{4}, AcceptedFrontierNonceSize),
		AnchorHeight:  5,
		AnchorBlockID: "block-5", TargetHeight: 10,
	}
	session := &SyncSession{ID: 12, Stage: SyncStageFrontier}
	sm := newFrontierCoreSyncManager(newFrontierCoreTestStore("block-5", 5), nil, 2, 2)
	sm.activeSession = session
	sm.Syncing = true
	sm.frontierRound = &frontierRound{
		sessionID: session.ID, query: query,
		k: 2, alpha: 2,
		sampled:    map[types.NodeID]struct{}{peer: {}, "other-peer": {}},
		dispatched: map[types.NodeID]struct{}{peer: {}, "other-peer": {}},
		responses:  make(map[types.NodeID]AcceptedFrontierVote),
	}
	vote := AcceptedFrontierVote{
		Peer: peer, RequestDigest: ComputeAcceptedFrontierRequestDigest(query),
		Status: AcceptedFrontierStatusBelowTarget, AcceptedHeight: 9,
		ObservedAnchorID: "block-5", TargetHeight: 10,
	}
	sig, err := signer.Sign(ComputeAcceptedFrontierDigest(query, vote))
	if err != nil {
		t.Fatal(err)
	}
	vote.Signature = sig
	sm.HandleAcceptedFrontierVote(vote)
	sm.HandleAcceptedFrontierVote(vote)
	if _, exists := sm.PeerHeights[peer]; exists {
		t.Fatal("one signed frontier response must not update the global peer-height high water")
	}
	if got := len(sm.frontierRound.responses); got != 1 {
		t.Fatalf("duplicate frontier response counted as %d identities, want 1", got)
	}
	if sm.activeSession != session || session.Stage != SyncStageFrontier {
		t.Fatalf("duplicate frontier response advanced the session: active=%v stage=%s", sm.activeSession, session.Stage)
	}
}

func TestFrontierLateVoteFromOldNonceIsIgnoredWithoutPeerFailure(t *testing.T) {
	signer := newAcceptedFrontierTestSigner(t)
	peer := types.NodeID(signer.GetAddress())
	oldQuery := AcceptedFrontierQuery{
		Requester: "self", Nonce: bytes.Repeat([]byte{0x31}, AcceptedFrontierNonceSize),
		AnchorHeight:  5,
		AnchorBlockID: "block-5", TargetHeight: 6,
	}
	newQuery := oldQuery
	newQuery.Nonce = bytes.Repeat([]byte{0x32}, AcceptedFrontierNonceSize)
	session := &SyncSession{ID: 13, Stage: SyncStageFrontier}
	sm := newFrontierCoreSyncManager(newFrontierCoreTestStore("block-5", 5), nil, 1, 1)
	sm.activeSession = session
	sm.Syncing = true
	sm.frontierRound = &frontierRound{
		sessionID: session.ID, query: newQuery,
		k: 1, alpha: 1,
		sampled: map[types.NodeID]struct{}{peer: {}}, dispatched: map[types.NodeID]struct{}{peer: {}},
		responses: make(map[types.NodeID]AcceptedFrontierVote),
	}
	lateVote := AcceptedFrontierVote{
		Peer: peer, RequestDigest: ComputeAcceptedFrontierRequestDigest(oldQuery),
		Status: AcceptedFrontierStatusOK, AcceptedHeight: 6,
		ObservedAnchorID: "block-5", TargetHeight: 6, TargetBlockID: "block-6",
	}
	sig, err := signer.Sign(ComputeAcceptedFrontierDigest(oldQuery, lateVote))
	if err != nil {
		t.Fatal(err)
	}
	lateVote.Signature = sig
	for i := 0; i < peerBanFailThreshold; i++ {
		sm.HandleAcceptedFrontierVote(lateVote)
	}
	if sm.isPeerBanned(peer) {
		t.Fatal("a valid delayed vote from an old nonce must not count as a peer failure")
	}
	if got := len(sm.frontierRound.responses); got != 0 {
		t.Fatalf("old-round vote must not count in the active round, got %d responses", got)
	}
	if sm.activeSession != session || session.Stage != SyncStageFrontier {
		t.Fatalf("old-round vote must not advance or fail the active session: active=%v stage=%s", sm.activeSession, session.Stage)
	}
}

func TestFrontierVoteAfterLocalRoundTimeoutIsIgnored(t *testing.T) {
	signer := newAcceptedFrontierTestSigner(t)
	peer := types.NodeID(signer.GetAddress())
	query := AcceptedFrontierQuery{
		Requester:     "self",
		Nonce:         bytes.Repeat([]byte{0x41}, AcceptedFrontierNonceSize),
		AnchorHeight:  5,
		AnchorBlockID: "block-5",
		TargetHeight:  6,
	}
	session := &SyncSession{ID: 14, Stage: SyncStageFrontier}
	sm := newFrontierCoreSyncManager(newFrontierCoreTestStore("block-5", 5), nil, 1, 1)
	sm.activeSession = session
	sm.Syncing = true
	sm.frontierRound = &frontierRound{
		sessionID:  session.ID,
		query:      query,
		startedAt:  time.Now().Add(-2 * sm.frontierPollTimeout()),
		k:          1,
		alpha:      1,
		sampled:    map[types.NodeID]struct{}{peer: {}},
		dispatched: map[types.NodeID]struct{}{peer: {}},
		responses:  make(map[types.NodeID]AcceptedFrontierVote),
	}
	vote := AcceptedFrontierVote{
		Peer:             peer,
		RequestDigest:    ComputeAcceptedFrontierRequestDigest(query),
		Status:           AcceptedFrontierStatusOK,
		AcceptedHeight:   6,
		ObservedAnchorID: "block-5",
		TargetHeight:     6,
		TargetBlockID:    "block-6",
	}
	sig, err := signer.Sign(ComputeAcceptedFrontierDigest(query, vote))
	if err != nil {
		t.Fatal(err)
	}
	vote.Signature = sig

	for i := 0; i < peerBanFailThreshold; i++ {
		sm.HandleAcceptedFrontierVote(vote)
	}
	if got := len(sm.frontierRound.responses); got != 0 {
		t.Fatalf("response after the requester's local round timeout must be ignored, got %d", got)
	}
	if sm.isPeerBanned(peer) {
		t.Fatal("a valid response delayed beyond the requester's local timeout must not count as a peer failure")
	}
	if sm.activeSession != session || session.Stage != SyncStageFrontier {
		t.Fatalf("late response must not advance or fail the active session: active=%v stage=%s", sm.activeSession, session.Stage)
	}
}

func TestFrontierTrustedHeightUsesAlphaThHighestAnchorCompatibleVote(t *testing.T) {
	round := &frontierRound{
		query: AcceptedFrontierQuery{AnchorHeight: 5, AnchorBlockID: "block-5", TargetHeight: 10},
		responses: map[types.NodeID]AcceptedFrontierVote{
			"p1": {Status: AcceptedFrontierStatusOK, AcceptedHeight: 30, ObservedAnchorID: "block-5"},
			"p2": {Status: AcceptedFrontierStatusOK, AcceptedHeight: 20, ObservedAnchorID: "block-5"},
			"p3": {Status: AcceptedFrontierStatusBelowTarget, AcceptedHeight: 9, ObservedAnchorID: "block-5"},
			"p4": {Status: AcceptedFrontierStatusAnchorMismatch, AcceptedHeight: 1000, ObservedAnchorID: "fork-5"},
		},
	}
	height, ok := frontierTrustedAcceptedHeight(round, 3)
	if !ok || height != 9 {
		t.Fatalf("trusted height must be the third-highest compatible vote: height=%d ok=%t", height, ok)
	}
}

func TestFrontierBlockRequestNeverFallsBackOutsideSupporters(t *testing.T) {
	store := newFrontierCoreTestStore("block-5", 5)
	transport := &frontierCoreTestTransport{peers: []types.NodeID{"supporter", "other"}}
	sm := newFrontierCoreSyncManager(store, transport, 2, 2)
	session := &SyncSession{
		ID:                   3,
		Stage:                SyncStageBlockCatchUp,
		FrontierAnchorHeight: 5,
		FrontierAnchorID:     "block-5",
		FrontierTargetHeight: 6,
		FrontierTargetID:     "block-6",
		frontierNonce:        bytes.Repeat([]byte{1}, AcceptedFrontierNonceSize),
		frontierSupporters:   map[types.NodeID]struct{}{"supporter": {}},
	}
	sm.activeSession = session
	sm.Syncing = true
	sm.PeerHeights["supporter"] = 6
	sm.PeerHeights["other"] = 6

	if sm.requestSyncFrom(6, 6, "other") {
		t.Fatal("non-supporter must never be accepted as a frontier download source")
	}
	for i := 0; i < peerBanFailThreshold; i++ {
		sm.recordPeerFailure("supporter", "test ban")
	}
	if sm.requestSyncFrom(6, 6, "supporter") {
		t.Fatal("banned supporter must not fall back to an arbitrary peer")
	}
	if len(transport.sends) != 0 {
		t.Fatalf("unexpected sync sends: %v", transport.sends)
	}
}

func TestBlockCatchUpPrefersVerifiedServiceProviderWithoutConsensusAuthority(t *testing.T) {
	store := newFrontierCoreTestStore("block-5", 5)
	transport := &frontierCoreTestTransport{}
	serviceStore, serviceID := newVerifiedFrontierServicePeer(t, "8.8.8.8:19000", 1, 100, 3)
	sm := newFrontierCoreSyncManager(store, transport, 3, 2)
	sm.SetServicePeerStore(serviceStore)
	sm.PeerHeights["supporter"] = 6
	session := &SyncSession{
		ID: 13, Stage: SyncStageBlockCatchUp,
		TargetHeight: 6, PeerAcceptedHeight: 6,
		FrontierAnchorHeight: 5, FrontierAnchorID: "block-5",
		FrontierTargetHeight: 6, FrontierTargetID: "block-6",
		frontierNonce:      bytes.Repeat([]byte{0x13}, AcceptedFrontierNonceSize),
		frontierSupporters: map[types.NodeID]struct{}{"supporter": {}},
	}
	sm.activeSession = session
	sm.Syncing = true

	if serr := sm.execBlockCatchUpStage(session); serr != nil {
		t.Fatalf("verified service provider did not dispatch: %v", serr)
	}
	if len(transport.sends) != 1 || transport.sends[0] != serviceID {
		t.Fatalf("service provider was not preferred: sends=%v service=%s", transport.sends, serviceID)
	}
	if _, gainedConsensusRole := session.frontierSupporters[serviceID]; gainedConsensusRole {
		t.Fatal("service provider was inserted into frontierSupporters")
	}
	if _, gainedHeightRole := sm.PeerHeights[serviceID]; gainedHeightRole {
		t.Fatal("service provider was inserted into PeerHeights")
	}
	var syncID uint32
	var meta syncRequestMeta
	for id, candidate := range sm.SyncRequestMeta {
		syncID, meta = id, candidate
	}
	if meta.Peer != serviceID || meta.ProviderRole != syncProviderService ||
		meta.FrontierTarget != 6 || meta.FrontierTargetID != "block-6" {
		t.Fatalf("request metadata lost provider/frontier binding: %+v", meta)
	}

	sm.HandleSyncResponse(types.Message{
		Type:       types.MsgSyncResponse,
		From:       serviceID,
		SyncID:     syncID,
		FromHeight: 6,
		ToHeight:   6,
		Blocks: []*types.Block{{
			ID: "block-6",
			Header: types.BlockHeader{
				Height:   6,
				ParentID: "block-5",
			},
		}},
	})
	peer, ok := serviceStore.Get(string(serviceID))
	if !ok || peer.SuccessfulDataResponses != 2 || peer.LastDataSuccessHeight != 6 {
		t.Fatalf("successful service download was not recorded: %+v ok=%t", peer, ok)
	}
	if gotID, gotHeight := store.GetLastAccepted(); gotID != "block-6" || gotHeight != 6 {
		t.Fatalf("verified service batch did not finalize exact target: got=%d/%s", gotHeight, gotID)
	}
}

func TestServiceProviderFailureDoesNotPenalizeSameNodeIDMiner(t *testing.T) {
	store := newFrontierCoreTestStore("block-5", 5)
	transport := &frontierCoreTestTransport{}
	serviceStore, sharedID := newVerifiedFrontierServicePeer(t, "8.8.4.4:19000", 1, 100, 100)
	sm := newFrontierCoreSyncManager(store, transport, 3, 2)
	sm.SetServicePeerStore(serviceStore)
	meta := syncRequestMeta{
		Peer:         sharedID,
		ProviderRole: syncProviderService,
		FromHeight:   6,
		ToHeight:     6,
	}

	                                                                            
	                                                                          
	                                                                   
	for i := 0; i < peerBanFailThreshold+2; i++ {
		sm.markServiceProviderFailure(meta, uint64(6+i))
	}
	if sm.isPeerBanned(sharedID) {
		t.Fatal("service provider failures changed the same NodeID's miner peerRep")
	}
	peer, ok := serviceStore.Get(string(sharedID))
	if !ok || peer.FailedDataResponses != peerBanFailThreshold+2 {
		t.Fatalf("service failures did not reach the service health table: peer=%+v ok=%t", peer, ok)
	}

	for i := 0; i < peerBanFailThreshold-1; i++ {
		sm.recordPeerFailure(sharedID, "miner route failure")
	}
	if sm.isPeerBanned(sharedID) {
		t.Fatal("miner was banned before its own peerRep reached the threshold")
	}
	sm.recordPeerFailure(sharedID, "miner route failure")
	if !sm.isPeerBanned(sharedID) {
		t.Fatal("miner peerRep did not independently ban at its own threshold")
	}
}

func TestServiceProviderSuccessDoesNotResetSameNodeIDMinerFailures(t *testing.T) {
	store := newFrontierCoreTestStore("block-5", 5)
	transport := &frontierCoreTestTransport{}
	serviceStore, sharedID := newVerifiedFrontierServicePeer(t, "1.0.0.1:19000", 1, 100, 3)
	sm := newFrontierCoreSyncManager(store, transport, 3, 2)
	sm.SetServicePeerStore(serviceStore)

	for i := 0; i < peerBanFailThreshold-1; i++ {
		sm.recordPeerFailure(sharedID, "miner route failure")
	}
	meta := syncRequestMeta{
		Peer:         sharedID,
		ProviderRole: syncProviderService,
		FromHeight:   6,
		ToHeight:     6,
	}
	sm.markServiceProviderSuccess(meta, 6)
	peer, ok := serviceStore.Get(string(sharedID))
	if !ok || peer.SuccessfulDataResponses != 2 || peer.LastDataSuccessHeight != 6 {
		t.Fatalf("service success did not update the service health table: peer=%+v ok=%t", peer, ok)
	}

	                                                                        
	                                                                          
	sm.recordPeerFailure(sharedID, "miner route failure")
	if !sm.isPeerBanned(sharedID) {
		t.Fatal("service success reset the same NodeID's existing miner failures")
	}
}

func TestBlockCatchUpFallsBackFromServiceProviderToFrontierSupporter(t *testing.T) {
	store := newFrontierCoreTestStore("block-5", 5)
	serviceStore, serviceID := newVerifiedFrontierServicePeer(t, "1.1.1.1:19000", 1, 100, 3)
	transport := &frontierCoreTestTransport{failServiceSend: map[types.NodeID]bool{serviceID: true}}
	sm := newFrontierCoreSyncManager(store, transport, 3, 2)
	sm.SetServicePeerStore(serviceStore)
	session := &SyncSession{
		ID: 14, Stage: SyncStageBlockCatchUp,
		FrontierAnchorHeight: 5, FrontierAnchorID: "block-5",
		FrontierTargetHeight: 6, FrontierTargetID: "block-6",
		frontierNonce:      bytes.Repeat([]byte{0x14}, AcceptedFrontierNonceSize),
		frontierSupporters: map[types.NodeID]struct{}{serviceID: {}},
	}
	sm.activeSession = session
	sm.Syncing = true

	if serr := sm.execBlockCatchUpStage(session); serr != nil {
		t.Fatalf("frontier supporter fallback did not dispatch: %v", serr)
	}
	want := []types.NodeID{serviceID, serviceID}
	if fmt.Sprint(transport.sends) != fmt.Sprint(want) {
		t.Fatalf("provider fallback order=%v want=%v", transport.sends, want)
	}
	if len(transport.sent) != 2 || !transport.sent[0].ServiceSync || transport.sent[1].ServiceSync {
		t.Fatalf("same NodeID did not switch from service route to miner route: %+v", transport.sent)
	}
	if _, triedAsService := session.serviceTried[serviceID]; !triedAsService {
		t.Fatal("failed service route was not recorded in serviceTried")
	}
	if _, triedAsSupporter := session.frontierTried[serviceID]; !triedAsSupporter {
		t.Fatal("miner fallback was not independently recorded in frontierTried")
	}
	peer, _ := serviceStore.Get(string(serviceID))
	if peer.FailedDataResponses != 1 || peer.LastDataFailureHeight != 5 {
		t.Fatalf("failed service dispatch was not recorded: %+v", peer)
	}
	for _, meta := range sm.SyncRequestMeta {
		if meta.Peer != serviceID || meta.ProviderRole != syncProviderFrontierSupporter {
			t.Fatalf("fallback request not bound to frontier supporter: %+v", meta)
		}
	}
}

func TestAsyncServiceTerminalFailureIsBoundAndFallsBackImmediately(t *testing.T) {
	store := newFrontierCoreTestStore("block-5", 5)
	serviceStore, serviceID := newVerifiedFrontierServicePeer(t, "4.2.2.2:19000", 1, 100, 3)
	transport := &frontierCoreTestTransport{}
	sm := newFrontierCoreSyncManager(store, transport, 3, 2)
	sm.SetServicePeerStore(serviceStore)
	session := &SyncSession{
		ID: 17, Stage: SyncStageBlockCatchUp,
		FrontierAnchorHeight: 5, FrontierAnchorID: "block-5",
		FrontierTargetHeight: 6, FrontierTargetID: "block-6",
		frontierNonce:      bytes.Repeat([]byte{0x17}, AcceptedFrontierNonceSize),
		frontierSupporters: map[types.NodeID]struct{}{"supporter": {}},
	}
	sm.activeSession = session
	sm.Syncing = true
	if serr := sm.execBlockCatchUpStage(session); serr != nil {
		t.Fatalf("service request did not dispatch: %v", serr)
	}

	var syncID uint32
	for id, meta := range sm.SyncRequestMeta {
		if meta.ProviderRole == syncProviderService {
			syncID = id
		}
	}
	if syncID == 0 {
		t.Fatal("missing service request metadata")
	}
	                                                                        
	sm.HandleSyncResponse(types.Message{
		Type: types.MsgSyncResponse, From: serviceID, SyncID: syncID + 1,
		FromHeight: 6, ToHeight: 6, SyncFailed: true, SyncError: "stale",
	})
	if _, ok := sm.SyncRequestMeta[syncID]; !ok {
		t.Fatal("stale failure callback consumed the active request")
	}

	sm.HandleSyncResponse(types.Message{
		Type: types.MsgSyncResponse, From: serviceID, SyncID: syncID,
		FromHeight: 6, ToHeight: 6, SyncFailed: true, SyncError: "http status 503",
	})
	deadline := time.Now().Add(time.Second)
	for {
		transport.mu.Lock()
		sends := append([]types.NodeID(nil), transport.sends...)
		messages := append([]types.Message(nil), transport.sent...)
		transport.mu.Unlock()
		if len(sends) >= 2 {
			if sends[0] != serviceID || sends[1] != "supporter" || !messages[0].ServiceSync || messages[1].ServiceSync {
				t.Fatalf("terminal failure fallback order/messages invalid: sends=%v messages=%+v", sends, messages)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("service terminal failure waited for timeout instead of falling back: sends=%v", sends)
		}
		time.Sleep(time.Millisecond)
	}
	if _, ok := sm.SyncRequestMeta[syncID]; ok {
		t.Fatal("terminal failure retained the completed service request")
	}
	peer, ok := serviceStore.Get(string(serviceID))
	if !ok || peer.FailedDataResponses != 1 || peer.LastDataFailureHeight != 5 {
		t.Fatalf("terminal data failure was not recorded exactly once: %+v ok=%t", peer, ok)
	}
}

func TestBlockCatchUpSkipsServiceProviderWithoutFullArchiveRange(t *testing.T) {
	store := newFrontierCoreTestStore("block-5", 5)
	serviceStore, serviceID := newVerifiedFrontierServicePeer(t, "9.9.9.9:19000", 7, 100, 3)
	transport := &frontierCoreTestTransport{}
	sm := newFrontierCoreSyncManager(store, transport, 3, 2)
	sm.SetServicePeerStore(serviceStore)
	session := &SyncSession{
		ID: 16, Stage: SyncStageBlockCatchUp,
		FrontierAnchorHeight: 5, FrontierAnchorID: "block-5",
		FrontierTargetHeight: 6, FrontierTargetID: "block-6",
		frontierNonce:      bytes.Repeat([]byte{0x16}, AcceptedFrontierNonceSize),
		frontierSupporters: map[types.NodeID]struct{}{"supporter": {}},
	}
	sm.activeSession = session

	if serr := sm.execBlockCatchUpStage(session); serr != nil {
		t.Fatalf("supporter fallback failed: %v", serr)
	}
	if len(transport.sends) != 1 || transport.sends[0] != "supporter" {
		t.Fatalf("non-covering service peer %s was selected: sends=%v", serviceID, transport.sends)
	}
}

func TestBlockCatchUpTriesAllSupportersBeforeStageFailure(t *testing.T) {
	store := newFrontierCoreTestStore("block-5", 5)
	transport := &frontierCoreTestTransport{failSend: map[types.NodeID]bool{
		"peer-1": true, "peer-2": true, "peer-3": true,
	}}
	sm := newFrontierCoreSyncManager(store, transport, 5, 4)
	session := &SyncSession{
		ID: 15, Stage: SyncStageBlockCatchUp,
		FrontierAnchorHeight: 5, FrontierAnchorID: "block-5",
		FrontierTargetHeight: 6, FrontierTargetID: "block-6",
		frontierNonce: bytes.Repeat([]byte{1}, AcceptedFrontierNonceSize),
		frontierSupporters: map[types.NodeID]struct{}{
			"peer-1": {}, "peer-2": {}, "peer-3": {}, "peer-4": {},
		},
	}
	sm.activeSession = session
	sm.Syncing = true

	if serr := sm.execBlockCatchUpStage(session); serr != nil {
		t.Fatalf("fourth supporter should dispatch after three send failures, got %v", serr)
	}
	transport.mu.Lock()
	attempts := append([]types.NodeID(nil), transport.sends...)
	transport.mu.Unlock()
	want := []types.NodeID{"peer-1", "peer-2", "peer-3", "peer-4"}
	if fmt.Sprint(attempts) != fmt.Sprint(want) {
		t.Fatalf("supporter attempts were truncated or reordered: got=%v want=%v", attempts, want)
	}
	if len(sm.SyncRequests) != 1 {
		t.Fatalf("expected only the successful fourth request to remain in flight, got %d", len(sm.SyncRequests))
	}
	for _, meta := range sm.SyncRequestMeta {
		if meta.Peer != "peer-4" {
			t.Fatalf("in-flight request bound to wrong supporter: %+v", meta)
		}
	}
}

func TestLightBlockCatchUpYieldsToCandidateBeforeDownload(t *testing.T) {
	baseStore := newFrontierCoreTestStore("block-5", 5)
	baseStore.blocks["local-6"] = &types.Block{
		ID: "local-6",
		Header: types.BlockHeader{
			Height:   6,
			ParentID: "block-5",
		},
	}
	store := &frontierCandidateTestStore{frontierCoreTestStore: baseStore}
	transport := &frontierCoreTestTransport{}
	sm := newFrontierCoreSyncManager(store, transport, 3, 2)
	session := &SyncSession{
		ID:                   43,
		Stage:                SyncStageBlockCatchUp,
		Light:                true,
		FrontierAnchorHeight: 5,
		FrontierAnchorID:     "block-5",
		FrontierTargetHeight: 6,
		FrontierTargetID:     "remote-6",
		frontierNonce:        bytes.Repeat([]byte{0x43}, AcceptedFrontierNonceSize),
		frontierSupporters: map[types.NodeID]struct{}{
			"peer-1": {},
			"peer-2": {},
		},
	}
	sm.activeSession = session

	if serr := sm.execBlockCatchUpStage(session); serr != nil {
		t.Fatalf("local candidate should end light download cleanly: %v", serr)
	}
	if session.Stage != SyncStageDone {
		t.Fatalf("light session stage=%s, want done", session.Stage)
	}
	if len(transport.sends) != 0 {
		t.Fatalf("light sync downloaded despite a local Snowman candidate: %v", transport.sends)
	}
}

func TestBlockCatchUpResamplesWhenLocalTipAdvancesPastFrontierAnchor(t *testing.T) {
	store := newFrontierCoreTestStore("block-5", 5)
	block6 := &types.Block{ID: "block-6", Header: types.BlockHeader{Height: 6, ParentID: "block-5"}}
	store.blocks[block6.ID] = block6
	store.finalized[6] = block6
	store.lastID = block6.ID
	store.lastHeight = 6
	transport := &frontierCoreTestTransport{}
	sm := newFrontierCoreSyncManager(store, transport, 3, 2)
	session := &SyncSession{
		ID: 17, Stage: SyncStageBlockCatchUp,
		TargetHeight: 7, PeerAcceptedHeight: 7,
		FrontierAnchorHeight: 5, FrontierAnchorID: "block-5",
		FrontierTargetHeight: 7, FrontierTargetID: "block-7",
		frontierNonce:      bytes.Repeat([]byte{1}, AcceptedFrontierNonceSize),
		frontierSupporters: map[types.NodeID]struct{}{"peer-1": {}, "peer-2": {}},
	}
	sm.activeSession = session
	sm.Syncing = true

	if serr := sm.execBlockCatchUpStage(session); serr != nil {
		t.Fatalf("advanced local prefix should trigger a fresh frontier round, got %v", serr)
	}
	if session.Stage != SyncStageFrontier || session.TargetHeight != 7 {
		t.Fatalf("expected frontier resample toward height 7, got stage=%s target=%d", session.Stage, session.TargetHeight)
	}
	if session.FrontierTargetID != "" || len(session.frontierSupporters) != 0 || len(transport.sends) != 0 {
		t.Fatalf("old frontier authorization must be discarded before resampling: target=%q supporters=%v sends=%v",
			session.FrontierTargetID, session.frontierSupporters, transport.sends)
	}
}

func TestBlockCatchUpFinishesWhenSampledTargetAlreadyFinalizedLocally(t *testing.T) {
	store := newFrontierCoreTestStore("block-5", 5)
	block6 := &types.Block{ID: "block-6", Header: types.BlockHeader{Height: 6, ParentID: "block-5"}}
	store.blocks[block6.ID] = block6
	store.finalized[6] = block6
	store.lastID = block6.ID
	store.lastHeight = 6
	sm := newFrontierCoreSyncManager(store, &frontierCoreTestTransport{}, 3, 2)
	session := &SyncSession{
		ID: 18, Stage: SyncStageBlockCatchUp,
		FrontierAnchorHeight: 5, FrontierAnchorID: "block-5",
		FrontierTargetHeight: 6, FrontierTargetID: "block-6",
	}
	sm.activeSession = session
	sm.Syncing = true

	sm.runStateMachine(session)
	if sm.activeSession != nil || sm.Syncing {
		t.Fatalf("already-finalized sampled target should complete the session: active=%v syncing=%t", sm.activeSession, sm.Syncing)
	}
	if sm.lastSession == nil || sm.lastSession.Stage != SyncStageDone {
		t.Fatalf("completed session not recorded: %+v", sm.lastSession)
	}
}

func TestBlockCatchUpStopsOnLocallyFinalizedTargetConflict(t *testing.T) {
	store := newFrontierCoreTestStore("block-5", 5)
	fork6 := &types.Block{ID: "fork-6", Header: types.BlockHeader{Height: 6, ParentID: "block-5"}}
	store.blocks[fork6.ID] = fork6
	store.finalized[6] = fork6
	store.lastID = fork6.ID
	store.lastHeight = 6
	sm := newFrontierCoreSyncManager(store, &frontierCoreTestTransport{}, 3, 2)
	session := &SyncSession{
		ID: 19, Stage: SyncStageBlockCatchUp,
		FrontierAnchorHeight: 5, FrontierAnchorID: "block-5",
		FrontierTargetHeight: 6, FrontierTargetID: "block-6",
	}
	sm.activeSession = session
	sm.Syncing = true
	safetyCalled := false
	sm.SetSafetyFaultHandler(func(error) { safetyCalled = true })

	serr := sm.execBlockCatchUpStage(session)
	if serr == nil || serr.Kind != SyncErrDeterministic || !safetyCalled {
		t.Fatalf("local target conflict must stop as a safety fault: err=%v safetyCalled=%t", serr, safetyCalled)
	}
}

func TestFrontierBatchValidatesWholeChainThenFinalizesSequentially(t *testing.T) {
	baseStore := newFrontierCoreTestStore("genesis", 0)
	baseStore.blocks["ordinary-local-1"] = &types.Block{
		ID: "ordinary-local-1",
		Header: types.BlockHeader{
			Height:   1,
			ParentID: "genesis",
		},
	}
	store := &frontierCandidateTestStore{frontierCoreTestStore: baseStore}
	events := &frontierCoreTestEventBus{}
	sm := newFrontierCoreSyncManager(store, nil, 1, 1)
	sm.events = events
	b1 := &types.Block{ID: "block-1", Header: types.BlockHeader{Height: 1, ParentID: "genesis"}}
	b2 := &types.Block{ID: "block-2", Header: types.BlockHeader{Height: 2, ParentID: "block-1"}}
	meta := syncRequestMeta{FrontierAnchor: 0, FrontierAnchorID: "genesis", FrontierTarget: 2, FrontierTargetID: "block-2"}

	badMeta := meta
	badMeta.FrontierTargetID = "other-block-2"
	if _, err := sm.validateFrontierBatch(types.Message{Blocks: []*types.Block{b1, b2}}, badMeta); err == nil {
		t.Fatal("batch with a terminal ID different from the sampled frontier must fail")
	}
	if len(baseStore.calls) != 0 {
		t.Fatalf("rejected batch mutated block store: %v", baseStore.calls)
	}

	blocks, err := sm.validateFrontierBatch(types.Message{Blocks: []*types.Block{b2, b1}}, meta)
	if err != nil {
		t.Fatalf("valid contiguous frontier batch rejected: %v", err)
	}
	finalized, height, err := sm.finalizeFrontierBatch(blocks, meta, false)
	if err != nil || finalized != 2 || height != 2 {
		t.Fatalf("finalize result finalized=%d height=%d err=%v", finalized, height, err)
	}
	wantCalls := []string{"add:block-1", "finalize:block-1", "add:block-2", "finalize:block-2"}
	if fmt.Sprint(baseStore.calls) != fmt.Sprint(wantCalls) {
		t.Fatalf("ordinary frontier was blocked by a pending candidate or finalized out of order: got=%v want=%v",
			baseStore.calls, wantCalls)
	}
	events.mu.Lock()
	defer events.mu.Unlock()
	for _, event := range events.events {
		if event.Type() == types.EventBlockFinalized {
			t.Fatal("frontier sync manager must not republish EventBlockFinalized")
		}
	}
}

func TestLightFrontierResponseYieldsWhenCandidateArrivesDuringDownload(t *testing.T) {
	baseStore := newFrontierCoreTestStore("block-5", 5)
	baseStore.blocks["local-6"] = &types.Block{
		ID: "local-6",
		Header: types.BlockHeader{
			Height:   6,
			ParentID: "block-5",
		},
	}
	store := &frontierCandidateTestStore{frontierCoreTestStore: baseStore}
	sm := newFrontierCoreSyncManager(store, nil, 3, 2)
	nonce := bytes.Repeat([]byte{0x44}, AcceptedFrontierNonceSize)
	peer := types.NodeID("peer-1")
	session := &SyncSession{
		ID:                   44,
		Stage:                SyncStageBlockCatchUp,
		Light:                true,
		FrontierAnchorHeight: 5,
		FrontierAnchorID:     "block-5",
		FrontierTargetHeight: 6,
		FrontierTargetID:     "remote-6",
		frontierNonce:        append([]byte(nil), nonce...),
		frontierSupporters: map[types.NodeID]struct{}{
			peer: {},
		},
	}
	sm.activeSession = session
	meta := syncRequestMeta{
		Peer:              peer,
		FromHeight:        6,
		ToHeight:          6,
		FrontierSessionID: session.ID,
		FrontierNonce:     append([]byte(nil), nonce...),
		FrontierAnchor:    5,
		FrontierAnchorID:  "block-5",
		FrontierTarget:    6,
		FrontierTargetID:  "remote-6",
	}
	remote := &types.Block{
		ID: "remote-6",
		Header: types.BlockHeader{
			Height:   6,
			ParentID: "block-5",
		},
	}

	sm.handleFrontierSyncResponse(types.Message{
		From:       peer,
		FromHeight: 6,
		ToHeight:   6,
		Blocks:     []*types.Block{remote},
	}, meta)

	if len(baseStore.calls) != 0 {
		t.Fatalf("yielded light commit mutated block store: %v", baseStore.calls)
	}
	if gotID, gotHeight := store.GetLastAccepted(); gotID != "block-5" || gotHeight != 5 {
		t.Fatalf("yielded light commit changed tip: got=%d/%s", gotHeight, gotID)
	}
	if sm.activeSession != nil || sm.lastSession == nil || sm.lastSession.Stage != SyncStageDone {
		t.Fatalf("yielded light response must finish without retry: active=%+v last=%+v",
			sm.activeSession, sm.lastSession)
	}
}

func TestLightFrontierCommitRechecksTipAfterCandidateValidation(t *testing.T) {
	baseStore := newFrontierCoreTestStore("block-5", 5)
	for _, block := range []*types.Block{
		{
			ID: "local-6",
			Header: types.BlockHeader{
				Height:   6,
				ParentID: "block-5",
			},
		},
		{
			ID: "ordinary-finalized-6",
			Header: types.BlockHeader{
				Height:   6,
				ParentID: "block-5",
			},
		},
	} {
		baseStore.blocks[block.ID] = block
	}
	advanced := false
	store := &frontierCandidateValidationStore{
		frontierCandidateTestStore: &frontierCandidateTestStore{
			frontierCoreTestStore: baseStore,
		},
		validate: func(*types.Block) error {
			if advanced {
				return nil
			}
			advanced = true
			return baseStore.SetFinalized(6, "ordinary-finalized-6")
		},
	}
	sm := newFrontierCoreSyncManager(store, nil, 3, 2)
	meta := syncRequestMeta{
		FrontierAnchor:   5,
		FrontierAnchorID: "block-5",
		FrontierTarget:   6,
		FrontierTargetID: "remote-6",
	}
	remote := &types.Block{
		ID: "remote-6",
		Header: types.BlockHeader{
			Height:   6,
			ParentID: "block-5",
		},
	}

	finalized, height, err := sm.finalizeFrontierBatch([]*types.Block{remote}, meta, true)
	if err == nil || !strings.Contains(err.Error(), "conflicts at height 6") {
		t.Fatalf("ordinary commit completed during candidate validation was hidden: finalized=%d height=%d err=%v",
			finalized, height, err)
	}
	if finalized != 0 || height != 6 {
		t.Fatalf("conflicting ordinary commit returned finalized=%d height=%d, want 0/6", finalized, height)
	}
}

func TestRejectedFrontierBatchDoesNotInstallPayloadInBlockCache(t *testing.T) {
	payload := commitmentTestBlock(t)
	clearCachedBlockForTest(payload.BlockHash)
	defer clearCachedBlockForTest(payload.BlockHash)
	block, err := (&ConsensusAdapter{}).DBBlockToConsensus(payload)
	if err != nil {
		t.Fatalf("convert payload: %v", err)
	}
	raw, err := proto.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	sm := newFrontierCoreSyncManager(newFrontierCoreTestStore(payload.Header.PrevBlockHash, payload.Header.Height-1), nil, 1, 1)
	meta := syncRequestMeta{
		FrontierAnchor: payload.Header.Height - 1, FrontierAnchorID: payload.Header.PrevBlockHash,
		FrontierTarget: payload.Header.Height, FrontierTargetID: "different-quorum-target",
	}
	msg := types.Message{
		Blocks: []*types.Block{block}, SyncBlocksRaw: map[string][]byte{block.ID: raw},
	}
	if _, err := sm.validateFrontierBatch(msg, meta); err == nil || !strings.Contains(err.Error(), "terminal block mismatch") {
		t.Fatalf("expected terminal mismatch after payload validation, got %v", err)
	}
	if _, exists := GetCachedBlock(payload.BlockHash); exists {
		t.Fatal("a payload from a rejected frontier batch must not enter the global block cache")
	}
}

func TestValidateFrontierBlockPayloadRejectsNestedRepeatedBloat(t *testing.T) {
	payload := commitmentTestBlock(t)
	payload.Body = make([]*pb.AnyTx, 10_001)
	clearCachedBlockForTest(payload.BlockHash)
	defer clearCachedBlockForTest(payload.BlockHash)

	block, err := (&ConsensusAdapter{}).DBBlockToConsensus(payload)
	if err != nil {
		t.Fatalf("convert payload: %v", err)
	}
	raw, err := proto.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	sm := newFrontierCoreSyncManager(
		newFrontierCoreTestStore(payload.Header.PrevBlockHash, payload.Header.Height-1),
		nil,
		1,
		1,
	)

	_, err = sm.validateFrontierBlockPayload(types.Message{
		SyncBlocksRaw: map[string][]byte{block.ID: raw},
	}, block)
	if !errors.Is(err, pb.ErrDataBloat) {
		t.Fatalf("nested block repeated bloat error = %v, want %v", err, pb.ErrDataBloat)
	}
	if _, exists := GetCachedBlock(payload.BlockHash); exists {
		t.Fatal("rejected frontier payload was cached")
	}
}

func TestHandleSyncRequestServesOnlyContinuousFinalizedBlocks(t *testing.T) {
	store := newFrontierCoreTestStore("genesis", 0)
	b1 := &types.Block{ID: "block-1", Header: types.BlockHeader{Height: 1, ParentID: "genesis"}}
	b2 := &types.Block{ID: "block-2", Header: types.BlockHeader{Height: 2, ParentID: "block-1"}}
	fork2 := &types.Block{ID: "fork-2", Header: types.BlockHeader{Height: 2, ParentID: "block-1"}}
	if _, err := store.Add(b1); err != nil {
		t.Fatal(err)
	}
	if err := store.SetFinalized(1, b1.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Add(b2); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Add(fork2); err != nil {
		t.Fatal(err)
	}
	if err := store.SetFinalized(2, b2.ID); err != nil {
		t.Fatal(err)
	}
	transport := &frontierCoreTestTransport{}
	sm := newFrontierCoreSyncManager(store, transport, 1, 1)

	sm.HandleSyncRequest(types.Message{From: "requester", SyncID: 8, FromHeight: 1, ToHeight: 2})
	transport.mu.Lock()
	if len(transport.sent) != 1 {
		transport.mu.Unlock()
		t.Fatalf("expected one response, got %d", len(transport.sent))
	}
	response := transport.sent[0]
	transport.mu.Unlock()
	if len(response.Blocks) != 2 || response.Blocks[0].ID != b1.ID || response.Blocks[1].ID != b2.ID {
		t.Fatalf("response must contain only the finalized chain, got %+v", response.Blocks)
	}

	sm.HandleSyncRequest(types.Message{From: "requester", SyncID: 9, FromHeight: 1, ToHeight: 1 + AcceptedFrontierMaxSpan})
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if len(transport.sent) != 1 {
		t.Fatal("over-span request must not receive a partial response")
	}
}

func TestHandleSyncRequestAtMaxUint64DoesNotWrapHeight(t *testing.T) {
	maxHeight := ^uint64(0)
	store := newFrontierCoreTestStore("block-max", maxHeight)
	transport := &frontierCoreTestTransport{}
	sm := newFrontierCoreSyncManager(store, transport, 1, 1)

	sm.HandleSyncRequest(types.Message{From: "requester", SyncID: 10, FromHeight: maxHeight, ToHeight: maxHeight})
	transport.mu.Lock()
	if len(transport.sent) != 1 || len(transport.sent[0].Blocks) != 1 || transport.sent[0].Blocks[0].ID != "block-max" {
		got := append([]types.Message(nil), transport.sent...)
		transport.mu.Unlock()
		t.Fatalf("expected one max-height finalized block response, got %+v", got)
	}
	transport.mu.Unlock()
	store.mu.Lock()
	reads := append([]uint64(nil), store.finalizedReads...)
	store.mu.Unlock()
	if len(reads) != 1 || reads[0] != maxHeight {
		t.Fatalf("height iteration wrapped past MaxUint64: reads=%v", reads)
	}
}

func TestFrontierAnchorMismatchInvokesSafetyHandlerBeforeSessionFinish(t *testing.T) {
	for _, withHandler := range []bool{false, true} {
		t.Run(fmt.Sprintf("handler=%t", withHandler), func(t *testing.T) {
			signer1 := newAcceptedFrontierTestSigner(t)
			signer2 := newAcceptedFrontierTestSigner(t)
			peer1 := types.NodeID(signer1.GetAddress())
			peer2 := types.NodeID(signer2.GetAddress())
			query := AcceptedFrontierQuery{
				Requester:     "self",
				Nonce:         bytes.Repeat([]byte{3}, AcceptedFrontierNonceSize),
				AnchorHeight:  5,
				AnchorBlockID: "block-5",
				TargetHeight:  6,
			}
			session := &SyncSession{ID: 4, Stage: SyncStageFrontier}
			sm := newFrontierCoreSyncManager(newFrontierCoreTestStore("block-5", 5), nil, 2, 2)
			sm.activeSession = session
			sm.Syncing = true
			sm.frontierRound = &frontierRound{
				sessionID:  session.ID,
				query:      query,
				k:          2,
				alpha:      2,
				sampled:    map[types.NodeID]struct{}{peer1: {}, peer2: {}},
				dispatched: map[types.NodeID]struct{}{peer1: {}, peer2: {}},
				responses:  make(map[types.NodeID]AcceptedFrontierVote),
			}
			called := 0
			handlerSawActive := false
			if withHandler {
				sm.SetSafetyFaultHandler(func(err error) {
					called++
					if err == nil {
						t.Fatal("safety handler received nil error")
					}
					sm.Mu.RLock()
					handlerSawActive = sm.activeSession == session
					sm.Mu.RUnlock()
				})
			}
			votes := []struct {
				vote   AcceptedFrontierVote
				signer interfaces.NodeSigner
			}{
				{vote: AcceptedFrontierVote{Peer: peer1, RequestDigest: ComputeAcceptedFrontierRequestDigest(query), Status: AcceptedFrontierStatusAnchorMismatch, ObservedAnchorID: "fork-block-5", TargetHeight: 6}, signer: signer1},
				{vote: AcceptedFrontierVote{Peer: peer2, RequestDigest: ComputeAcceptedFrontierRequestDigest(query), Status: AcceptedFrontierStatusAnchorMismatch, ObservedAnchorID: "another-fork-block-5", TargetHeight: 6}, signer: signer2},
			}
			for i := range votes {
				sig, err := votes[i].signer.Sign(ComputeAcceptedFrontierDigest(query, votes[i].vote))
				if err != nil {
					t.Fatalf("sign vote: %v", err)
				}
				votes[i].vote.Signature = sig
				sm.HandleAcceptedFrontierVote(votes[i].vote)
			}
			if sm.activeSession != nil || sm.lastSession == nil || sm.lastSession.Stage != SyncStageFailed ||
				!strings.Contains(sm.lastSession.LastError, "conflicts with local finalized anchor") {
				t.Fatalf("anchor safety fault must remain observable in failed session: active=%v last=%+v", sm.activeSession, sm.lastSession)
			}
			if withHandler && (called != 1 || !handlerSawActive) {
				t.Fatalf("handler must run once before finish: called=%d sawActive=%t", called, handlerSawActive)
			}
		})
	}
}
