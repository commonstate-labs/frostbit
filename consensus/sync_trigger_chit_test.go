package consensus

import (
	"dex/logs"
	"dex/types"
	"sync"
	"testing"
	"time"
)

type captureSyncTransport struct {
	mu              sync.Mutex
	to              []types.NodeID
	sent            []types.Message
	frontierTo      []types.NodeID
	frontierQueries []AcceptedFrontierQuery
}

func (t *captureSyncTransport) Send(to types.NodeID, msg types.Message) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.to = append(t.to, to)
	t.sent = append(t.sent, msg)
	return nil
}

func (t *captureSyncTransport) Receive() <-chan types.Message { return nil }
func (t *captureSyncTransport) Broadcast(types.Message, []types.NodeID) {
}
func (t *captureSyncTransport) SamplePeers(types.NodeID, int) []types.NodeID {
	return []types.NodeID{"peer-1"}
}
func (t *captureSyncTransport) GetAllPeers(types.NodeID) []types.NodeID {
	return []types.NodeID{"peer-1"}
}
func (t *captureSyncTransport) SendAcceptedFrontierQuery(peer types.NodeID, query AcceptedFrontierQuery, _ func(AcceptedFrontierVote)) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.frontierTo = append(t.frontierTo, peer)
	t.frontierQueries = append(t.frontierQueries, query)
	return nil
}

func (t *captureSyncTransport) snapshot() ([]types.NodeID, []types.Message) {
	t.mu.Lock()
	defer t.mu.Unlock()
	to := append([]types.NodeID(nil), t.to...)
	sent := append([]types.Message(nil), t.sent...)
	return to, sent
}

func (t *captureSyncTransport) frontierSnapshot() ([]types.NodeID, []AcceptedFrontierQuery) {
	t.mu.Lock()
	defer t.mu.Unlock()
	to := append([]types.NodeID(nil), t.frontierTo...)
	queries := append([]AcceptedFrontierQuery(nil), t.frontierQueries...)
	return to, queries
}

func TestHandleHeightResponseTriggersCatchUpWithoutPeriodicTick(t *testing.T) {
	store := &stubBlockStore{lastID: "blk-100", lastHeight: 100, blocks: map[string]*types.Block{}}
	transport := &captureSyncTransport{}
	sm := &SyncManager{
		nodeID:             "node-self",
		transport:          transport,
		store:              store,
		config:             &SyncConfig{Timeout: time.Second, ChitGracePeriod: time.Nanosecond},
		Logger:             logs.NewNodeLogger("height-hint-test", 0),
		PeerHeights:        map[types.NodeID]uint64{},
		SyncRequests:       map[uint32]time.Time{},
		InFlightSyncRanges: map[string]time.Time{},
		frontierK:          1,
		frontierAlpha:      1,
	}
	sm.peerRep = sm.newPeerBanTracker()

	sm.HandleHeightResponse(types.Message{From: "peer-1", Height: 101})

	deadline := time.Now().Add(time.Second)
	var to []types.NodeID
	var queries []AcceptedFrontierQuery
	for time.Now().Before(deadline) {
		to, queries = transport.frontierSnapshot()
		if len(queries) > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if len(queries) != 1 {
		t.Fatalf("expected height response to trigger one frontier query, got %d", len(queries))
	}
	if to[0] != "peer-1" {
		t.Fatalf("expected catch-up to prefer peer-1, got %s", to[0])
	}
	if queries[0].AnchorHeight != 100 || queries[0].TargetHeight != 101 {
		t.Fatalf("unexpected frontier query from height response: %+v", queries[0])
	}
}

func TestShouldPauseConsensusForCatchUpIgnoresHeightHints(t *testing.T) {
	store := &stubBlockStore{lastID: "blk-100", lastHeight: 100, blocks: map[string]*types.Block{}}
	sm := &SyncManager{
		store:       store,
		config:      &SyncConfig{ChitSoftGap: 1},
		PeerHeights: map[types.NodeID]uint64{"peer-1": 102},
	}
	if sm.ShouldPauseConsensusForCatchUp() {
		t.Fatal("a peer height beyond the soft gap must not pause consensus before verification")
	}

	sm.PeerHeights["peer-1"] = 101
	if sm.ShouldPauseConsensusForCatchUp() {
		t.Fatal("consensus should not pause for a one-block soft gap without active sync")
	}
}

func TestTriggerSyncFromChitOneBlockUsesLightCatchUp(t *testing.T) {
	store := &stubBlockStore{lastID: "blk-100", lastHeight: 100, blocks: map[string]*types.Block{}}
	transport := &captureSyncTransport{}
	sm := &SyncManager{
		nodeID:             "node-self",
		transport:          transport,
		store:              store,
		config:             &SyncConfig{Timeout: time.Second, ChitGracePeriod: time.Nanosecond},
		Logger:             logs.NewNodeLogger("sync-light-test", 0),
		PeerHeights:        map[types.NodeID]uint64{},
		SyncRequests:       map[uint32]time.Time{},
		InFlightSyncRanges: map[string]time.Time{},
		frontierK:          1,
		frontierAlpha:      1,
	}
	sm.peerRep = sm.newPeerBanTracker()

	sm.TriggerSyncFromChit(101, "peer-1")

	deadline := time.Now().Add(time.Second)
	var to []types.NodeID
	var queries []AcceptedFrontierQuery
	for time.Now().Before(deadline) {
		to, queries = transport.frontierSnapshot()
		if len(queries) > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if len(queries) != 1 {
		t.Fatalf("expected one light frontier query, got %d", len(queries))
	}
	if sm.Syncing {
		t.Fatal("one-block catch-up must not set Syncing=true")
	}
	if sm.activeSession == nil || !sm.activeSession.Light || sm.activeSession.Stage != SyncStageFrontier {
		t.Fatalf("one-block catch-up must use a light frontier session, got %+v", sm.activeSession)
	}
	if to[0] != "peer-1" {
		t.Fatalf("expected preferred peer peer-1, got %s", to[0])
	}
	if queries[0].AnchorHeight != 100 || queries[0].TargetHeight != 101 {
		t.Fatalf("unexpected light frontier query: %+v", queries[0])
	}
}

func TestSingleShallowHeightHintStartsFrontierAfterGrace(t *testing.T) {
	store := &stubBlockStore{lastID: "blk-100", lastHeight: 100, blocks: map[string]*types.Block{}}
	transport := &captureSyncTransport{}
	sm := &SyncManager{
		nodeID: "node-self", transport: transport, store: store,
		config: &SyncConfig{
			Timeout: time.Second, FrontierPollTimeout: time.Second,
			ChitSoftGap: 1, ChitHardGap: 3, ChitGracePeriod: time.Nanosecond,
			DeepLagStateSyncThreshold: 10, DeepLagMinAdditionalPeers: 2,
		},
		Logger:      logs.NewNodeLogger("single-shallow-hint-test", 0),
		PeerHeights: map[types.NodeID]uint64{}, SyncRequests: map[uint32]time.Time{},
		InFlightSyncRanges: map[string]time.Time{}, frontierK: 1, frontierAlpha: 1,
	}
	sm.peerRep = sm.newPeerBanTracker()

	sm.TriggerSyncFromChit(102, "peer-1")
	deadline := time.Now().Add(time.Second)
	var queries []AcceptedFrontierQuery
	for time.Now().Before(deadline) {
		_, queries = transport.frontierSnapshot()
		if len(queries) > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if len(queries) != 1 {
		t.Fatalf("one shallow hint must reach frontier without extra unsigned confirmations, got %d queries", len(queries))
	}
	if queries[0].AnchorHeight != 100 || queries[0].TargetHeight != 102 {
		t.Fatalf("unexpected shallow frontier query: %+v", queries[0])
	}
}

func TestUnconfirmedDeepHintCannotMaskBoundedFrontierHint(t *testing.T) {
	store := &stubBlockStore{lastID: "blk-100", lastHeight: 100, blocks: map[string]*types.Block{}}
	sm := &SyncManager{
		store:  store,
		config: &SyncConfig{DeepLagStateSyncThreshold: 10, DeepLagMinAdditionalPeers: 2},
	}
	sm.Mu.Lock()
	sm.markPendingChitLocked(1000, "deep-peer")
	sm.markPendingChitLocked(102, "bounded-peer")
	if sm.chitPendingHeight != 102 || sm.chitPendingFrom != "bounded-peer" {
		sm.Mu.Unlock()
		t.Fatalf("bounded hint must displace unconfirmed deep hint: height=%d from=%s", sm.chitPendingHeight, sm.chitPendingFrom)
	}
	sm.markPendingChitLocked(2000, "other-deep-peer")
	if sm.chitPendingHeight != 102 || sm.chitPendingFrom != "bounded-peer" {
		sm.Mu.Unlock()
		t.Fatalf("unconfirmed deep hint must not replace bounded pending hint: height=%d from=%s", sm.chitPendingHeight, sm.chitPendingFrom)
	}
	sm.Mu.Unlock()
}

func newDeepHintTestManager(store *stubBlockStore, transport *captureSyncTransport) *SyncManager {
	sm := &SyncManager{
		nodeID: "node-self", transport: transport, store: store,
		config: &SyncConfig{
			BehindThreshold: 1, BatchSize: 50, Timeout: time.Second, FrontierPollTimeout: time.Second,
			ChitSoftGap: 1, ChitHardGap: 3, ChitGracePeriod: time.Nanosecond,
			DeepLagStateSyncThreshold: 10, DeepLagMinAdditionalPeers: 2,
		},
		Logger:      logs.NewNodeLogger("deep-hint-test", 0),
		PeerHeights: map[types.NodeID]uint64{}, SyncRequests: map[uint32]time.Time{},
		SyncRequestMeta: map[uint32]syncRequestMeta{}, InFlightSyncRanges: map[string]time.Time{},
		frontierK: 1, frontierAlpha: 1,
	}
	sm.peerRep = sm.newPeerBanTracker()
	return sm
}

func TestDeepHintsSelectConfirmedLowerHeightAfterByzantineMaximum(t *testing.T) {
	store := &stubBlockStore{lastID: "blk-100", lastHeight: 100, blocks: map[string]*types.Block{}}
	transport := &captureSyncTransport{}
	sm := newDeepHintTestManager(store, transport)
	maxHeight := ^uint64(0)

	sm.TriggerSyncFromChit(maxHeight, "byzantine-max")
	sm.TriggerSyncFromChit(120, "honest-1")
	if sm.activeSession != nil {
		t.Fatal("two deep sources must not satisfy a three-source deep-hint threshold")
	}
	sm.TriggerSyncFromChit(120, "honest-2")

	_, queries := transport.frontierSnapshot()
	if len(queries) != 1 {
		t.Fatalf("confirmed lower deep hint must start the deep pipeline (stub-store fallback frontier), got %+v", queries)
	}
	sm.Mu.RLock()
	activePeerHeight := uint64(0)
	if sm.activeSession != nil {
		activePeerHeight = sm.activeSession.PeerAcceptedHeight
	}
	sm.Mu.RUnlock()
	if activePeerHeight != 120 {
		t.Fatalf("deep session must use required-th highest confirmed height 120, not the Byzantine maximum: %d", activePeerHeight)
	}
	if sm.chitPending {
		t.Fatal("confirmed lower deep height must retire the pending maximum hint")
	}
}

func TestPeriodicSyncIgnoresUnconfirmedDeepMaximumThenUsesConfirmedHeight(t *testing.T) {
	store := &stubBlockStore{lastID: "blk-100", lastHeight: 100, blocks: map[string]*types.Block{}}
	transport := &captureSyncTransport{}
	sm := newDeepHintTestManager(store, transport)
	sm.PeerHeights["byzantine-max"] = ^uint64(0)

	sm.tickCheckAndSync()
	if sm.activeSession != nil {
		t.Fatal("periodic sync must not enter certificate sync from one unconfirmed deep maximum")
	}
	if _, queries := transport.frontierSnapshot(); len(queries) != 0 {
		t.Fatalf("unconfirmed deep maximum unexpectedly started sync: %+v", queries)
	}

	sm.PeerHeights["honest-1"] = 120
	sm.PeerHeights["honest-2"] = 120
	sm.tickCheckAndSync()
	_, queries := transport.frontierSnapshot()
	if len(queries) != 1 {
		t.Fatalf("periodic confirmed deep lag must start sync, got %+v", queries)
	}
	sm.Mu.RLock()
	activePeerHeight := uint64(0)
	if sm.activeSession != nil {
		activePeerHeight = sm.activeSession.PeerAcceptedHeight
	}
	sm.Mu.RUnlock()
	if activePeerHeight != 120 {
		t.Fatalf("periodic deep sync must select the third-highest confirmed height 120: %d", activePeerHeight)
	}
}

func TestCatchUpPauseRequiresVerifiedEvidenceBeyondHeightHints(t *testing.T) {
	store := &stubBlockStore{lastID: "blk-100", lastHeight: 100, blocks: map[string]*types.Block{}}
	sm := newDeepHintTestManager(store, &captureSyncTransport{})
	sm.PeerHeights["byzantine-max"] = ^uint64(0)
	if sm.ShouldPauseConsensusForCatchUp() {
		t.Fatal("one unconfirmed deep maximum must not pause consensus")
	}
	sm.PeerHeights["honest-1"] = 120
	sm.PeerHeights["honest-2"] = 120
	if sm.ShouldPauseConsensusForCatchUp() {
		t.Fatal("independent height hints permit certificate discovery, but must not pause consensus")
	}

	sm.PeerHeights = map[types.NodeID]uint64{"shallow": 102}
	if sm.ShouldPauseConsensusForCatchUp() {
		t.Fatal("bounded lag beyond the soft gap must first acquire signed frontier quorum")
	}
}
