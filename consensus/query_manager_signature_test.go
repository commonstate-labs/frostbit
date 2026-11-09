package consensus

import (
	"bytes"
	"context"
	"testing"
	"time"

	"dex/pb"
	"dex/types"
	"dex/utils"
)

func queryManagerTestNonce(value byte) []byte {
	return bytes.Repeat([]byte{value}, SnowmanQueryNonceSize)
}

func queryManagerTestChitDigest(requestID uint32, requester, responder, preferred string, height uint64, accepted string, acceptedHeight uint64, nonce []byte) []byte {
	return ComputeChitDigest(ChitDigestContext{
		ChainID:        pb.GetDefaultChainID(),
		RequestID:      requestID,
		Requester:      requester,
		Responder:      responder,
		PreferredBlock: preferred,
		QueryHeight:    height,
		AcceptedBlock:  accepted,
		AcceptedHeight: acceptedHeight,
		VRFSeed:        []byte("seed"),
		SeqID:          2,
		QueryNonce:     nonce,
	})
}

type queryManagerSigEngine struct {
	submitted            int
	lastSignature        []byte
	preference           string
	finalizingPreference string
}

func (e *queryManagerSigEngine) Start(context.Context) error { return nil }
func (e *queryManagerSigEngine) RegisterQuery(types.NodeID, uint32, string, uint64, uint32, []byte, map[types.NodeID]int) string {
	return "query-key"
}
func (e *queryManagerSigEngine) SubmitChit(_ types.NodeID, _ string, _ string, sig []byte) {
	e.submitted++
	e.lastSignature = sig
}
func (e *queryManagerSigEngine) GetActiveQueryCount() int    { return 0 }
func (e *queryManagerSigEngine) GetPreference(uint64) string { return e.preference }
func (e *queryManagerSigEngine) finalizingBlockID(uint64) string {
	return e.finalizingPreference
}

type queryManagerSigStore struct {
	blocks         map[string]*types.Block
	finalized      map[uint64]*types.Block
	acceptedID     string
	acceptedHeight uint64
	addCalls       int
	getCalls       int
}

func (s *queryManagerSigStore) Add(*types.Block) (bool, error) {
	s.addCalls++
	return true, nil
}
func (s *queryManagerSigStore) Get(id string) (*types.Block, bool) {
	s.getCalls++
	b, ok := s.blocks[id]
	return b, ok
}
func (s *queryManagerSigStore) GetByHeight(uint64) []*types.Block { return nil }
func (s *queryManagerSigStore) GetLastAccepted() (string, uint64) {
	return s.acceptedID, s.acceptedHeight
}
func (s *queryManagerSigStore) GetFinalizedAtHeight(height uint64) (*types.Block, bool) {
	b, ok := s.finalized[height]
	return b, ok
}
func (s *queryManagerSigStore) GetBlocksFromHeight(uint64, uint64) []*types.Block { return nil }
func (s *queryManagerSigStore) GetCurrentHeight() uint64                          { return 0 }
func (s *queryManagerSigStore) GetPendingBlocksCount() int                        { return 0 }
func (s *queryManagerSigStore) GetPendingBlocks() []*types.Block                  { return nil }
func (s *queryManagerSigStore) SetFinalized(uint64, string) error                 { return nil }

type captureTransport struct {
	sentTo types.NodeID
	sent   types.Message
}

func (t *captureTransport) Send(to types.NodeID, msg types.Message) error {
	t.sentTo = to
	t.sent = msg
	return nil
}
func (t *captureTransport) Receive() <-chan types.Message                { return nil }
func (t *captureTransport) Broadcast(types.Message, []types.NodeID)      {}
func (t *captureTransport) SamplePeers(types.NodeID, int) []types.NodeID { return nil }
func (t *captureTransport) GetAllPeers(types.NodeID) []types.NodeID      { return nil }

func TestHandleChitRejectsMissingPublicKey(t *testing.T) {
	ClearNodePublicKeys()
	SetNodePublicKeyResolver(nil)
	defer func() {
		ClearNodePublicKeys()
		SetNodePublicKeyResolver(nil)
	}()

	engine := &queryManagerSigEngine{}
	qm := &QueryManager{
		store:  &queryManagerSigStore{blocks: map[string]*types.Block{"block-1": {ID: "block-1"}}},
		engine: engine,
	}
	qm.activePolls.Store(uint32(7), &Poll{
		requestID:  7,
		queryKey:   "query-key",
		height:     1,
		vrfSeed:    []byte("seed"),
		seqID:      2,
		queryNonce: queryManagerTestNonce(1),
		sampledSet: map[types.NodeID]struct{}{"node-1": {}},
	})

	qm.HandleChit(types.Message{
		From:              "node-1",
		RequestID:         7,
		PreferredID:       "block-1",
		PreferredIDHeight: 1,
		QueryNonce:        queryManagerTestNonce(1),
		ChitSignature:     []byte("fake"),
	})

	if engine.submitted != 0 {
		t.Fatalf("missing public key chit should not be submitted, got %d submissions", engine.submitted)
	}
}

func TestHandleChitRejectsUnsignedAbstention(t *testing.T) {
	ClearNodePublicKeys()
	SetNodePublicKeyResolver(nil)
	defer func() {
		ClearNodePublicKeys()
		SetNodePublicKeyResolver(nil)
	}()

	engine := &queryManagerSigEngine{}
	qm := &QueryManager{
		store:  &queryManagerSigStore{blocks: map[string]*types.Block{}},
		engine: engine,
	}
	qm.activePolls.Store(uint32(7), &Poll{
		requestID:  7,
		queryKey:   "query-key",
		height:     1,
		vrfSeed:    []byte("seed"),
		seqID:      2,
		queryNonce: queryManagerTestNonce(2),
		sampledSet: map[types.NodeID]struct{}{"node-1": {}},
	})

	qm.HandleChit(types.Message{
		From:              "node-1",
		RequestID:         7,
		PreferredIDHeight: 1,
		QueryNonce:        queryManagerTestNonce(2),
	})

	if engine.submitted != 0 {
		t.Fatalf("unsigned abstention should not be submitted, got %d submissions", engine.submitted)
	}
}

func TestHandleChitVerifiesSignedAbstention(t *testing.T) {
	ClearNodePublicKeys()
	SetNodePublicKeyResolver(nil)
	defer func() {
		ClearNodePublicKeys()
		SetNodePublicKeyResolver(nil)
	}()

	km := newTestKeyManager(t)
	SetNodePublicKeyResolver(func(nodeID string) ([]byte, bool) {
		if nodeID != "node-1" {
			return nil, false
		}
		return km.PublicKeyBytes(), true
	})
	nonce := queryManagerTestNonce(3)
	digest := queryManagerTestChitDigest(7, "requester", "node-1", "", 1, "", 0, nonce)
	sig, err := km.Sign(digest)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	engine := &queryManagerSigEngine{}
	qm := &QueryManager{
		nodeID: "requester",
		store:  &queryManagerSigStore{blocks: map[string]*types.Block{}},
		engine: engine,
	}
	qm.activePolls.Store(uint32(7), &Poll{
		requestID:  7,
		queryKey:   "query-key",
		height:     1,
		vrfSeed:    []byte("seed"),
		seqID:      2,
		queryNonce: nonce,
		sampledSet: map[types.NodeID]struct{}{"node-1": {}},
	})

	qm.HandleChit(types.Message{
		From:              "node-1",
		RequestID:         7,
		PreferredIDHeight: 1,
		QueryNonce:        nonce,
		ChitSignature:     sig,
	})

	if engine.submitted != 1 {
		t.Fatalf("signed abstention should be submitted once, got %d", engine.submitted)
	}
	if string(engine.lastSignature) != string(sig) {
		t.Fatal("signed abstention signature should be preserved")
	}
}

func TestHandleChitUsesSignedAbstentionAcceptedHeightForSync(t *testing.T) {
	ClearNodePublicKeys()
	SetNodePublicKeyResolver(nil)
	defer func() {
		ClearNodePublicKeys()
		SetNodePublicKeyResolver(nil)
	}()

	km := newTestKeyManager(t)
	SetNodePublicKeyResolver(func(nodeID string) ([]byte, bool) {
		if nodeID != "node-1" {
			return nil, false
		}
		return km.PublicKeyBytes(), true
	})
	nonce := queryManagerTestNonce(4)
	digest := queryManagerTestChitDigest(7, "requester", "node-1", "", 1, "", 2, nonce)
	sig, err := km.Sign(digest)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	store := &queryManagerSigStore{blocks: map[string]*types.Block{}}
	sm := &SyncManager{
		store:              store,
		config:             &SyncConfig{ChitSoftGap: 100, ChitHardGap: 100},
		PeerHeights:        map[types.NodeID]uint64{},
		SyncRequests:       map[uint32]time.Time{},
		InFlightSyncRanges: map[string]time.Time{},
	}
	qm := &QueryManager{
		nodeID:      "requester",
		store:       store,
		engine:      &queryManagerSigEngine{},
		syncManager: sm,
	}
	qm.activePolls.Store(uint32(7), &Poll{
		requestID:  7,
		queryKey:   "query-key",
		height:     1,
		vrfSeed:    []byte("seed"),
		seqID:      2,
		queryNonce: nonce,
		sampledSet: map[types.NodeID]struct{}{"node-1": {}},
	})

	qm.HandleChit(types.Message{
		From:              "node-1",
		RequestID:         7,
		PreferredIDHeight: 1,
		AcceptedHeight:    2,
		QueryNonce:        nonce,
		ChitSignature:     sig,
	})

	if got := sm.PeerHeights["node-1"]; got != 2 {
		t.Fatalf("signed abstention accepted height should feed sync hint, got %d", got)
	}
}

func TestHandleChitAcceptsOnlyFirstSignedResponsePerSampledPeer(t *testing.T) {
	ClearNodePublicKeys()
	SetNodePublicKeyResolver(nil)
	defer func() {
		ClearNodePublicKeys()
		SetNodePublicKeyResolver(nil)
	}()

	km := newTestKeyManager(t)
	RegisterSignerPublicKey("node-1", km)
	nonce := queryManagerTestNonce(14)
	firstDigest := queryManagerTestChitDigest(7, "requester", "node-1", "", 1, "", 2, nonce)
	firstSignature, err := km.Sign(firstDigest)
	if err != nil {
		t.Fatalf("sign first chit: %v", err)
	}
	secondDigest := queryManagerTestChitDigest(7, "requester", "node-1", "", 1, "", 3, nonce)
	secondSignature, err := km.Sign(secondDigest)
	if err != nil {
		t.Fatalf("sign second chit: %v", err)
	}

	engine := &queryManagerSigEngine{}
	store := &queryManagerSigStore{blocks: map[string]*types.Block{}}
	sm := &SyncManager{
		store:              store,
		config:             &SyncConfig{ChitSoftGap: 100, ChitHardGap: 100},
		PeerHeights:        map[types.NodeID]uint64{},
		SyncRequests:       map[uint32]time.Time{},
		InFlightSyncRanges: map[string]time.Time{},
	}
	qm := &QueryManager{
		nodeID:      "requester",
		store:       store,
		engine:      engine,
		syncManager: sm,
	}
	qm.activePolls.Store(uint32(7), &Poll{
		requestID:  7,
		queryKey:   "query-key",
		height:     1,
		vrfSeed:    []byte("seed"),
		seqID:      2,
		queryNonce: nonce,
		sampledSet: map[types.NodeID]struct{}{"node-1": {}},
	})

	qm.HandleChit(types.Message{
		From:              "node-1",
		RequestID:         7,
		PreferredIDHeight: 1,
		AcceptedHeight:    2,
		QueryNonce:        nonce,
		ChitSignature:     firstSignature,
	})
	qm.HandleChit(types.Message{
		From:              "node-1",
		RequestID:         7,
		PreferredIDHeight: 1,
		AcceptedHeight:    3,
		QueryNonce:        nonce,
		ChitSignature:     secondSignature,
	})

	if engine.submitted != 1 {
		t.Fatalf("same sampled peer entered consensus %d times, want 1", engine.submitted)
	}
	if got := sm.PeerHeights["node-1"]; got != 2 {
		t.Fatalf("duplicate response changed sync height hint to %d, want first hint 2", got)
	}
}

func TestHandleChitRejectsAcceptedHeightAndQueryHeightTampering(t *testing.T) {
	ClearNodePublicKeys()
	SetNodePublicKeyResolver(nil)
	defer func() {
		ClearNodePublicKeys()
		SetNodePublicKeyResolver(nil)
	}()

	key := newTestKeyManager(t)
	RegisterSignerPublicKey("node-1", key)
	nonce := queryManagerTestNonce(10)
	digest := queryManagerTestChitDigest(7, "requester", "node-1", "", 1, "accepted-2", 2, nonce)
	signature, err := key.Sign(digest)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	engine := &queryManagerSigEngine{}
	store := &queryManagerSigStore{blocks: map[string]*types.Block{}}
	sm := &SyncManager{
		store:              store,
		config:             &SyncConfig{ChitSoftGap: 100, ChitHardGap: 100},
		PeerHeights:        map[types.NodeID]uint64{},
		SyncRequests:       map[uint32]time.Time{},
		InFlightSyncRanges: map[string]time.Time{},
	}
	qm := &QueryManager{
		nodeID:      "requester",
		store:       store,
		engine:      engine,
		syncManager: sm,
	}
	qm.activePolls.Store(uint32(7), &Poll{
		requestID: 7, queryKey: "query-key", height: 1,
		vrfSeed: []byte("seed"), seqID: 2, queryNonce: nonce,
		sampledSet: map[types.NodeID]struct{}{"node-1": {}},
	})

	qm.HandleChit(types.Message{
		From: "node-1", RequestID: 7, PreferredIDHeight: 1,
		AcceptedID: "accepted-2", AcceptedHeight: 3,
		QueryNonce: nonce, ChitSignature: signature,
	})
	qm.HandleChit(types.Message{
		From: "node-1", RequestID: 7, PreferredIDHeight: 2,
		AcceptedID: "accepted-2", AcceptedHeight: 2,
		QueryNonce: nonce, ChitSignature: signature,
	})
	if engine.submitted != 0 {
		t.Fatalf("tampered Chits entered consensus: submissions=%d", engine.submitted)
	}
	if len(sm.PeerHeights) != 0 {
		t.Fatalf("tampered Chits triggered sync hints: %+v", sm.PeerHeights)
	}
}

func TestHandleChitUsesPublicKeyResolver(t *testing.T) {
	ClearNodePublicKeys()
	SetNodePublicKeyResolver(nil)
	defer func() {
		ClearNodePublicKeys()
		SetNodePublicKeyResolver(nil)
	}()

	km := newTestKeyManager(t)
	SetNodePublicKeyResolver(func(nodeID string) ([]byte, bool) {
		if nodeID != "node-1" {
			return nil, false
		}
		return km.PublicKeyBytes(), true
	})
	nonce := queryManagerTestNonce(5)
	digest := queryManagerTestChitDigest(7, "requester", "node-1", "block-1", 1, "", 0, nonce)
	sig, err := km.Sign(digest)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	engine := &queryManagerSigEngine{}
	qm := &QueryManager{
		nodeID: "requester",
		store:  &queryManagerSigStore{blocks: map[string]*types.Block{"block-1": {ID: "block-1"}}},
		engine: engine,
	}
	qm.activePolls.Store(uint32(7), &Poll{
		requestID:  7,
		queryKey:   "query-key",
		height:     1,
		vrfSeed:    []byte("seed"),
		seqID:      2,
		queryNonce: nonce,
		sampledSet: map[types.NodeID]struct{}{"node-1": {}},
	})

	qm.HandleChit(types.Message{
		From:              "node-1",
		RequestID:         7,
		PreferredID:       "block-1",
		PreferredIDHeight: 1,
		QueryNonce:        nonce,
		ChitSignature:     sig,
	})

	if engine.submitted != 1 {
		t.Fatalf("valid resolver-backed chit should be submitted once, got %d", engine.submitted)
	}
	if len(engine.lastSignature) == 0 {
		t.Fatal("verified signature should be preserved for consensus evidence")
	}
}

func TestSendChitsSignsAbstention(t *testing.T) {
	ClearNodePublicKeys()
	SetNodePublicKeyResolver(nil)
	defer func() {
		ClearNodePublicKeys()
		SetNodePublicKeyResolver(nil)
	}()

	km := newTestKeyManager(t)
	RegisterSignerPublicKey("node-1", km)
	transport := &captureTransport{}
	handler := &MessageHandler{
		nodeID:          "node-1",
		store:           &queryManagerSigStore{blocks: map[string]*types.Block{}},
		engine:          &queryManagerSigEngine{},
		transport:       transport,
		signer:          km,
		livenessTracker: verifiedParticipantTrackerForTest(),
	}

	nonce := queryManagerTestNonce(6)
	handler.sendChits("node-2", 7, 1, []byte("seed"), 2, nonce)

	if transport.sentTo != "node-2" {
		t.Fatalf("chits target mismatch: got %s", transport.sentTo)
	}
	if transport.sent.PreferredID != "" {
		t.Fatalf("expected abstention preferred ID, got %q", transport.sent.PreferredID)
	}
	if len(transport.sent.ChitSignature) == 0 {
		t.Fatal("abstention chits must carry an ECDSA signature")
	}
	digest := queryManagerTestChitDigest(7, "node-2", "node-1", "", 1, "", 0, nonce)
	if !VerifyChitSignature("node-1", digest, transport.sent.ChitSignature) {
		t.Fatal("abstention signature should verify against empty preferred ID")
	}
}

func TestSendChitsSignsExactFinalizingPreferenceWithoutAdvancingAccepted(t *testing.T) {
	km := newTestKeyManager(t)
	transport := &captureTransport{}
	genesis := &types.Block{ID: "genesis", Header: types.BlockHeader{Height: 0}}
	finalizing := &types.Block{ID: "finalizing-1", Header: types.BlockHeader{Height: 1, ParentID: genesis.ID}}
	store := &queryManagerSigStore{
		blocks:         map[string]*types.Block{finalizing.ID: finalizing},
		finalized:      map[uint64]*types.Block{0: genesis},
		acceptedID:     genesis.ID,
		acceptedHeight: 0,
	}
	engine := &queryManagerSigEngine{
		preference:           "other-preference",
		finalizingPreference: finalizing.ID,
	}
	handler := &MessageHandler{
		nodeID:          "node-1",
		store:           store,
		engine:          engine,
		transport:       transport,
		signer:          km,
		livenessTracker: verifiedParticipantTrackerForTest(),
	}

	nonce := queryManagerTestNonce(10)
	handler.sendChits("node-2", 16, 1, []byte("seed"), 2, nonce)

	if transport.sent.PreferredID != finalizing.ID {
		t.Fatalf("finalizing preference = %q, want %q", transport.sent.PreferredID, finalizing.ID)
	}
	if transport.sent.AcceptedID != genesis.ID || transport.sent.AcceptedHeight != 0 {
		t.Fatalf("accepted tip advanced early: id=%q height=%d", transport.sent.AcceptedID, transport.sent.AcceptedHeight)
	}
	digest := queryManagerTestChitDigest(16, "node-2", "node-1", finalizing.ID, 1, genesis.ID, 0, nonce)
	if !utils.VerifyECDSASignature(km.PublicKeyBytes(), digest, transport.sent.ChitSignature) {
		t.Fatal("finalizing Chit signature does not bind exact preference and persisted accepted tip")
	}
}

func TestSendChitsUsesImmutableFinalizedBlockForFinalizedHeight(t *testing.T) {
	km := newTestKeyManager(t)
	transport := &captureTransport{}
	finalized := &types.Block{ID: "finalized-1", Header: types.BlockHeader{Height: 1, ParentID: "genesis"}}
	store := &queryManagerSigStore{
		blocks: map[string]*types.Block{
			"losing-candidate": {ID: "losing-candidate", Header: types.BlockHeader{Height: 1, ParentID: "genesis"}},
		},
		finalized:      map[uint64]*types.Block{1: finalized},
		acceptedID:     "finalized-2",
		acceptedHeight: 2,
	}
	handler := &MessageHandler{
		nodeID:          "node-1",
		store:           store,
		engine:          &queryManagerSigEngine{},
		transport:       transport,
		signer:          km,
		livenessTracker: verifiedParticipantTrackerForTest(),
	}
	nonce := queryManagerTestNonce(11)
	handler.sendChits("node-2", 17, 1, []byte("seed"), 2, nonce)

	if transport.sent.PreferredID != finalized.ID {
		t.Fatalf("finalized height preference = %q, want immutable %q", transport.sent.PreferredID, finalized.ID)
	}
	digest := queryManagerTestChitDigest(17, "node-2", "node-1", finalized.ID, 1, "finalized-2", 2, nonce)
	if !utils.VerifyECDSASignature(km.PublicKeyBytes(), digest, transport.sent.ChitSignature) {
		t.Fatal("finalized-height Chit signature does not bind the immutable finalized block")
	}
}

func TestHandlePullQueryAtFinalizedHeightDoesNotFetchUnknownCandidate(t *testing.T) {
	km := newTestKeyManager(t)
	transport := &captureTransport{}
	finalized := &types.Block{ID: "finalized-1", Header: types.BlockHeader{Height: 1, ParentID: "genesis"}}
	store := &queryManagerSigStore{
		blocks:         map[string]*types.Block{},
		finalized:      map[uint64]*types.Block{1: finalized},
		acceptedID:     "finalized-2",
		acceptedHeight: 2,
	}
	handler := &MessageHandler{
		nodeID:          "node-1",
		store:           store,
		engine:          &queryManagerSigEngine{},
		transport:       transport,
		signer:          km,
		livenessTracker: verifiedParticipantTrackerForTest(),
	}
	handler.handlePullQuery(types.Message{
		Type:       types.MsgPullQuery,
		From:       "node-2",
		RequestID:  18,
		BlockID:    "unknown-losing-candidate",
		Height:     1,
		VRFSeed:    []byte("seed"),
		SeqID:      2,
		QueryNonce: queryManagerTestNonce(12),
	})

	if store.getCalls != 0 {
		t.Fatalf("finalized PullQuery fetched candidate body %d times", store.getCalls)
	}
	if transport.sent.Type != types.MsgChits || transport.sent.PreferredID != finalized.ID {
		t.Fatalf("finalized PullQuery reply = type %s preferred %q, want Chits/%q", transport.sent.Type, transport.sent.PreferredID, finalized.ID)
	}
}

func TestHandlePushQueryAtFinalizedHeightRepliesWithFinalizedBlock(t *testing.T) {
	km := newTestKeyManager(t)
	transport := &captureTransport{}
	finalized := &types.Block{ID: "finalized-1", Header: types.BlockHeader{Height: 1, ParentID: "genesis"}}
	store := &queryManagerSigStore{
		blocks:         map[string]*types.Block{},
		finalized:      map[uint64]*types.Block{1: finalized},
		acceptedID:     "finalized-2",
		acceptedHeight: 2,
	}
	handler := &MessageHandler{
		nodeID:          "node-1",
		store:           store,
		engine:          &queryManagerSigEngine{},
		transport:       transport,
		signer:          km,
		livenessTracker: verifiedParticipantTrackerForTest(),
	}
	handler.handlePushQuery(types.Message{
		Type:      types.MsgPushQuery,
		From:      "node-2",
		RequestID: 19,
		Block: &types.Block{
			ID: "losing-candidate",
			Header: types.BlockHeader{
				Height: 1, ParentID: "genesis", Proposer: "node-2", Miner: "node-2",
			},
		},
		VRFSeed:    []byte("seed"),
		SeqID:      2,
		QueryNonce: queryManagerTestNonce(13),
	})

	if store.addCalls != 0 {
		t.Fatalf("finalized PushQuery inserted an old candidate %d times", store.addCalls)
	}
	if transport.sent.Type != types.MsgChits || transport.sent.PreferredID != finalized.ID {
		t.Fatalf("finalized PushQuery reply = type %s preferred %q, want Chits/%q", transport.sent.Type, transport.sent.PreferredID, finalized.ID)
	}
}

func TestHandleChitDoesNotTriggerSyncWithoutVerifiedPoll(t *testing.T) {
	sm := &SyncManager{
		store:              &queryManagerSigStore{blocks: map[string]*types.Block{}},
		PeerHeights:        map[types.NodeID]uint64{},
		SyncRequests:       map[uint32]time.Time{},
		InFlightSyncRanges: map[string]time.Time{},
	}
	qm := &QueryManager{
		store:       &queryManagerSigStore{blocks: map[string]*types.Block{}},
		engine:      &queryManagerSigEngine{},
		syncManager: sm,
	}

	qm.HandleChit(types.Message{From: "node-1", RequestID: 999, AcceptedHeight: 10})

	if len(sm.PeerHeights) != 0 {
		t.Fatalf("unsampled or stale chit must not update sync peer heights: %+v", sm.PeerHeights)
	}
}

func TestHandleChitFromOldRequestIDCannotEnterActivePoll(t *testing.T) {
	ClearNodePublicKeys()
	SetNodePublicKeyResolver(nil)
	defer func() {
		ClearNodePublicKeys()
		SetNodePublicKeyResolver(nil)
	}()

	key := newTestKeyManager(t)
	RegisterSignerPublicKey("node-1", key)
	oldNonce := queryManagerTestNonce(7)
	newNonce := queryManagerTestNonce(8)
	digest := queryManagerTestChitDigest(7, "requester", "node-1", "block-1", 1, "", 0, oldNonce)
	signature, err := key.Sign(digest)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	engine := &queryManagerSigEngine{}
	qm := &QueryManager{
		nodeID: "requester",
		store:  &queryManagerSigStore{blocks: map[string]*types.Block{"block-1": {ID: "block-1"}}},
		engine: engine,
	}
	qm.activePolls.Store(uint32(8), &Poll{
		requestID:  8,
		queryKey:   "active-query",
		height:     1,
		vrfSeed:    []byte("seed"),
		seqID:      2,
		queryNonce: newNonce,
		sampledSet: map[types.NodeID]struct{}{"node-1": {}},
	})

	                                                                            
	                                                                 
	qm.HandleChit(types.Message{
		From:              "node-1",
		RequestID:         8,
		PreferredID:       "block-1",
		PreferredIDHeight: 1,
		QueryNonce:        oldNonce,
		ChitSignature:     signature,
	})
	                                                                         
	                                                   
	qm.HandleChit(types.Message{
		From:              "node-1",
		RequestID:         8,
		PreferredID:       "block-1",
		PreferredIDHeight: 1,
		QueryNonce:        newNonce,
		ChitSignature:     signature,
	})
	if engine.submitted != 0 {
		t.Fatalf("old-round chit entered active poll: submissions=%d", engine.submitted)
	}
}

func TestSetLocalIdentityWiresRealPathSigner(t *testing.T) {
	ClearNodePublicKeys()
	SetNodePublicKeyResolver(nil)
	defer func() {
		ClearNodePublicKeys()
		SetNodePublicKeyResolver(nil)
	}()

	km := newTestKeyManager(t)
	rt := &RealTransport{nodeID: "bootstrap-id", address: "bootstrap-id"}
	mh := &MessageHandler{}
	m := &ConsensusNodeManager{
		Node:      &Node{messageHandler: mh},
		Transport: rt,
	}
	m.SetLocalIdentity(km, "node-addr")

	if mh.signer != km {
		t.Fatal("message handler must use local signer for Chits")
	}
	if rt.signer != km {
		t.Fatal("real transport must use local signer for PushQuery")
	}
	digest := queryManagerTestChitDigest(1, "requester", "node-addr", "block-1", 1, "", 0, queryManagerTestNonce(9))
	sig, err := km.Sign(digest)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if !VerifyChitSignature("node-addr", digest, sig) {
		t.Fatal("SetLocalIdentity should register the local public key under the runtime address")
	}
}
