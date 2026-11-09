package consensus

import (
	"dex/logs"
	"dex/types"
	"sync"
	"testing"
	"time"
)

type finalizingLivenessRecorder struct {
	mu      sync.Mutex
	sampled int
	offline int
}

func (r *finalizingLivenessRecorder) ReportSampled(addresses []string) {
	r.mu.Lock()
	r.sampled += len(addresses)
	r.mu.Unlock()
}

func (r *finalizingLivenessRecorder) ReportOffline(addresses []string) {
	r.mu.Lock()
	r.offline += len(addresses)
	r.mu.Unlock()
}

func countActivePolls(qm *QueryManager) int {
	count := 0
	qm.activePolls.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	return count
}

func TestFinalizingGateCancelsPollsWithoutConsumingNextSequence(t *testing.T) {
	block := &types.Block{
		ID: "block-1-peer-a-w0-finalizing",
		Header: types.BlockHeader{
			Height:   1,
			ParentID: "genesis",
			Proposer: "peer-a",
			Window:   0,
		},
	}
	store := &querySeqStore{block: block}
	events := NewEventBus()
	engine := NewSnowmanEngine(
		"self",
		store,
		&ConsensusConfig{K: 2, AlphaPreference: 1, AlphaConfidence: 1, Beta: 15, PollInterval: 125 * time.Millisecond, QueryTimeout: time.Second, MaxConcurrentQueries: 8},
		events,
		logs.NewNodeLogger("finalizing-gate-engine-test", 0),
	).(*SnowmanEngine)
	transport := &querySeqTransport{}
	qm := NewQueryManager(
		"self",
		transport,
		store,
		engine,
		engine.config,
		events,
		logs.NewNodeLogger("finalizing-gate-query-test", 0),
	)
	recorder := &finalizingLivenessRecorder{}
	qm.SetLivenessReporter(recorder)

	qm.issueQuery()
	if got := engine.GetActiveQueryCount(); got != 1 {
		t.Fatalf("active engine queries before finalizing = %d, want 1", got)
	}
	if got := countActivePolls(qm); got != 1 {
		t.Fatalf("active query-manager polls before finalizing = %d, want 1", got)
	}
	qm.activePolls.Range(func(_, value interface{}) bool {
		value.(*Poll).respondedPeers.Store(types.NodeID("peer-a"), struct{}{})
		return false
	})

	reserved, canceledKeys := engine.reserveFinalization(1, block.ID)
	if !reserved || len(canceledKeys) != 1 {
		t.Fatalf("reserve finalization = %v, canceled=%v; want one canceled poll", reserved, canceledKeys)
	}
	engine.publishFinalizingCancellations(canceledKeys)
	if !engine.IsFinalizing(1) {
		t.Fatal("height must report finalizing after reservation")
	}
	if got := engine.GetActiveQueryCount(); got != 0 {
		t.Fatalf("active engine queries during finalizing = %d, want 0", got)
	}
	if got := countActivePolls(qm); got != 0 {
		t.Fatalf("active query-manager polls during finalizing = %d, want 0", got)
	}

	qm.issueQuery()
	transport.mu.Lock()
	messageCount := len(transport.msgs)
	transport.mu.Unlock()
	if messageCount != 1 {
		t.Fatalf("query broadcast during finalizing: messages=%d, want 1", messageCount)
	}
	if qm.seqID != 1 {
		t.Fatalf("rejected finalizing query consumed seqID: got %d, want 1", qm.seqID)
	}

	recorder.mu.Lock()
	sampled, offline := recorder.sampled, recorder.offline
	recorder.mu.Unlock()
	if sampled != 0 || offline != 0 {
		t.Fatalf("locally canceled polls polluted liveness: sampled=%d offline=%d", sampled, offline)
	}

	engine.releaseFinalization(1, block.ID)
	qm.issueQuery()
	transport.mu.Lock()
	messages := append([]types.Message(nil), transport.msgs...)
	transport.mu.Unlock()
	if len(messages) != 2 {
		t.Fatalf("query did not resume after finalization release: messages=%d, want 2", len(messages))
	}
	if messages[1].SeqID != 1 {
		t.Fatalf("resumed query seqID = %d, want 1", messages[1].SeqID)
	}
}

func TestFinalizingGateOnlyCancelsItsHeight(t *testing.T) {
	engine := NewSnowmanEngine(
		"self",
		&querySeqStore{},
		&ConsensusConfig{K: 2, AlphaPreference: 1, AlphaConfidence: 1, Beta: 15, QueryTimeout: time.Second, MaxConcurrentQueries: 8},
		NewEventBus(),
		logs.NewNodeLogger("finalizing-height-test", 0),
	).(*SnowmanEngine)
	key1, key2 := "self-1", "self-2"
	engine.activeQueries[key1] = &QueryContext{queryKey: key1, height: 1}
	engine.activeQueries[key2] = &QueryContext{queryKey: key2, height: 2}
	reserved, canceled := engine.reserveFinalization(1, "block-1")
	if !reserved || len(canceled) != 1 || canceled[0] != key1 {
		t.Fatalf("unexpected canceled queries: reserved=%v canceled=%v want=[%s]", reserved, canceled, key1)
	}
	if got := engine.GetActiveQueryCount(); got != 1 {
		t.Fatalf("queries at other heights were canceled: active=%d, want 1", got)
	}
	if got := engine.RegisterQuery("self", 3, "block-1", 1, 1, nil, nil); got != "" {
		t.Fatalf("registered query during finalizing: %q", got)
	}
	engine.mu.RLock()
	_, kept := engine.activeQueries[key2]
	engine.mu.RUnlock()
	if !kept {
		t.Fatal("query at a different height was not preserved")
	}
}
