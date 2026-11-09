package consensus

import (
	"dex/logs"
	"dex/types"
	"sync"
	"testing"
	"time"
)

type timeoutRetryTransport struct {
	mu   sync.Mutex
	sent []types.Message
	to   []types.NodeID
}

func (t *timeoutRetryTransport) Send(to types.NodeID, msg types.Message) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.to = append(t.to, to)
	t.sent = append(t.sent, msg)
	return nil
}

func (t *timeoutRetryTransport) snapshot() ([]types.NodeID, []types.Message) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]types.NodeID(nil), t.to...), append([]types.Message(nil), t.sent...)
}

func (t *timeoutRetryTransport) Receive() <-chan types.Message           { return nil }
func (t *timeoutRetryTransport) Broadcast(types.Message, []types.NodeID) {}
func (t *timeoutRetryTransport) SamplePeers(types.NodeID, int) []types.NodeID {
	return []types.NodeID{"peer-a", "peer-b"}
}
func (t *timeoutRetryTransport) GetAllPeers(types.NodeID) []types.NodeID {
	return []types.NodeID{"peer-a", "peer-b"}
}

func TestProcessTimeouts_DoesNotFailSnapshotStageOnStaleSyncRequest(t *testing.T) {
	session := &SyncSession{ID: 1, Stage: SyncStageStateSnapshot}
	sm := &SyncManager{
		nodeID:             "node-test",
		config:             &SyncConfig{Timeout: time.Millisecond},
		Logger:             logs.NewNodeLogger("sync-timeout-test", 0),
		SyncRequests:       map[uint32]time.Time{7: time.Now().Add(-time.Second)},
		InFlightSyncRanges: map[string]time.Time{},
		activeSession:      session,
		Syncing:            true,
	}

	sm.processTimeouts()

	if sm.activeSession != session {
		t.Fatal("state snapshot session must not be finished by stale block sync request timeout")
	}
	if !sm.Syncing {
		t.Fatal("state snapshot session must keep Syncing=true")
	}
	if len(sm.SyncRequests) != 0 {
		t.Fatalf("stale sync request should be cleared, got %d", len(sm.SyncRequests))
	}
}

func TestProcessTimeouts_FailsFrontierCatchUpWhenSupportersExhausted(t *testing.T) {
	rangeKey := syncRangeKey(10, 20)
	session := &SyncSession{
		ID: 2, Stage: SyncStageBlockCatchUp,
		FrontierAnchorHeight: 9, FrontierAnchorID: "block-9",
		FrontierTargetHeight: 20, FrontierTargetID: "block-20",
		frontierNonce:      []byte{1},
		frontierSupporters: map[types.NodeID]struct{}{"peer-a": {}},
		frontierTried:      map[types.NodeID]struct{}{"peer-a": {}},
	}
	sm := &SyncManager{
		nodeID:       "node-test",
		store:        &stubBlockStore{lastID: "block-9", lastHeight: 9, blocks: map[string]*types.Block{}},
		config:       &SyncConfig{Timeout: time.Millisecond},
		Logger:       logs.NewNodeLogger("sync-timeout-test", 0),
		SyncRequests: map[uint32]time.Time{8: time.Now().Add(-time.Second)},
		SyncRequestMeta: map[uint32]syncRequestMeta{8: {
			Peer: "peer-a", FromHeight: 10, ToHeight: 20, RangeKey: rangeKey, FrontierSessionID: 2,
		}},
		InFlightSyncRanges: map[string]time.Time{rangeKey: time.Now().Add(-time.Second)},
		activeSession:      session,
		Syncing:            true,
	}
	sm.peerRep = sm.newPeerBanTracker()

	sm.processTimeouts()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		sm.Mu.RLock()
		active := sm.activeSession
		sm.Mu.RUnlock()
		if active == nil {
			break
		}
		time.Sleep(time.Millisecond)
	}

	sm.Mu.RLock()
	active := sm.activeSession
	syncing := sm.Syncing
	last := cloneSyncSession(sm.lastSession)
	sm.Mu.RUnlock()
	if active != nil {
		t.Fatal("block catch-up session should finish after all requests timeout")
	}
	if syncing {
		t.Fatal("block catch-up timeout should clear Syncing")
	}
	if last == nil || last.Stage != SyncStageFailed {
		t.Fatalf("last session should record failure, got %+v", last)
	}
}

func TestProcessTimeouts_RetriesFrontierRangeWithAnotherSupporter(t *testing.T) {
	session := &SyncSession{
		ID: 3, Stage: SyncStageBlockCatchUp,
		FrontierAnchorHeight: 9, FrontierAnchorID: "block-9",
		FrontierTargetHeight: 20, FrontierTargetID: "block-20",
		frontierNonce:      []byte{1},
		frontierSupporters: map[types.NodeID]struct{}{"peer-a": {}, "peer-b": {}},
		frontierTried:      map[types.NodeID]struct{}{"peer-a": {}},
	}
	tr := &timeoutRetryTransport{}
	rangeKey := syncRangeKey(10, 20)
	sm := &SyncManager{
		nodeID:       "node-test",
		transport:    tr,
		store:        &stubBlockStore{lastID: "block-9", lastHeight: 9, blocks: map[string]*types.Block{}},
		config:       &SyncConfig{Timeout: time.Millisecond},
		Logger:       logs.NewNodeLogger("sync-timeout-test", 0),
		SyncRequests: map[uint32]time.Time{9: time.Now().Add(-time.Second)},
		SyncRequestMeta: map[uint32]syncRequestMeta{9: {
			Peer: "peer-a", FromHeight: 10, ToHeight: 20, RangeKey: rangeKey, FrontierSessionID: 3,
		}},
		InFlightSyncRanges: map[string]time.Time{rangeKey: time.Now().Add(-time.Second)},
		PeerHeights:        map[types.NodeID]uint64{"peer-a": 20, "peer-b": 20},
		activeSession:      session,
		Syncing:            true,
	}
	sm.peerRep = sm.newPeerBanTracker()

	sm.processTimeouts()
	deadline := time.Now().Add(time.Second)
	var destinations []types.NodeID
	var sent []types.Message
	for time.Now().Before(deadline) {
		destinations, sent = tr.snapshot()
		if len(sent) > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}

	sm.Mu.RLock()
	active := sm.activeSession
	requestCount := len(sm.SyncRequests)
	sm.Mu.RUnlock()
	if active != session {
		t.Fatal("block catch-up session should stay active when alternate peer retry is scheduled")
	}
	if len(sent) != 1 {
		t.Fatalf("expected one retry send, got %d", len(sent))
	}
	if destinations[0] != "peer-b" {
		t.Fatalf("retry should exclude timed-out peer-a and use peer-b, got %s", destinations[0])
	}
	if sent[0].FromHeight != 10 || sent[0].ToHeight != 20 {
		t.Fatalf("retry range mismatch: %d-%d", sent[0].FromHeight, sent[0].ToHeight)
	}
	if requestCount != 1 {
		t.Fatalf("expected replacement sync request, got %d", requestCount)
	}
}

func TestServiceProviderResponseTimeoutExceedsControlPlaneTimeout(t *testing.T) {
	base := 10 * time.Second
	serviceMeta := syncRequestMeta{ProviderRole: syncProviderService}
	if got := syncRequestResponseTimeout(base, serviceMeta, true); got != serviceSyncProviderResponseTimeout {
		t.Fatalf("service response timeout=%s want=%s", got, serviceSyncProviderResponseTimeout)
	}
	if got := syncRequestResponseTimeout(base, syncRequestMeta{ProviderRole: syncProviderFrontierSupporter}, true); got != base {
		t.Fatalf("miner response timeout changed: got=%s want=%s", got, base)
	}
}
