package consensus

import (
	"strings"
	"sync"
	"testing"
	"time"

	"dex/types"
)

func newTraceTestEngine(t *testing.T, k, alpha int) (*SnowmanEngine, *types.Block) {
	t.Helper()
	block := &types.Block{ID: "block-1-peer-a-w0-trace", Header: types.BlockHeader{Height: 1, ParentID: "genesis"}}
	engine, _ := newProductionSnowballTestEngine(t, ConsensusConfig{K: k, AlphaPreference: alpha, AlphaConfidence: alpha, Beta: 100}, block)
	return engine, block
}

func tracedTestQuery(t *testing.T, engine *SnowmanEngine, block *types.Block, seq uint32, weights map[types.NodeID]int) *QueryContext {
	t.Helper()
	key := engine.RegisterQuery("self", seq+1, block.ID, 1, seq, nil, weights)
	if key == "" {
		t.Fatal("query registration failed")
	}
	engine.mu.Lock()
	ctx := engine.activeQueries[key]
	ctx.trace = newPollTrace()
	engine.mu.Unlock()
	return ctx
}

func TestPollTraceSeparatesNetworkCompletionFromOrderedSettlement(t *testing.T) {
	engine, block := newTraceTestEngine(t, 1, 1)
	first := tracedTestQuery(t, engine, block, 0, map[types.NodeID]int{"peer-a": 1})
	second := tracedTestQuery(t, engine, block, 1, map[types.NodeID]int{"peer-a": 1})
	engine.SubmitChit("peer-a", second.queryKey, block.ID, nil)
	doneAt := second.trace.networkDoneAt
	if doneAt.IsZero() || !second.trace.settleStartedAt.IsZero() {
		t.Fatalf("network completion must not mean ordered settlement: %+v", second.trace)
	}
	                                                                             
	                                                                    
	time.Sleep(20 * time.Millisecond)
	if !engine.expireQuery(first.queryKey, first) {
		t.Fatal("first poll did not expire")
	}
	if second.trace.networkDoneAt != doneAt {
		t.Fatal("ordered settlement overwrote network completion time")
	}
	if wait := second.trace.settleStartedAt.Sub(doneAt); wait < 20*time.Millisecond {
		t.Fatalf("order wait = %v, want at least 20ms", wait)
	}
	if first.trace.hard || !second.trace.hard || second.trace.confidenceBefore != 0 || second.trace.confidenceAfter != 1 {
		t.Fatalf("diagnostics changed or misreported ordered votes: first=%+v second=%+v", first.trace, second.trace)
	}
	if first.trace.settledAt.After(second.trace.settleStartedAt) {
		t.Fatal("tracing changed sequential application order")
	}
}

func TestPollTraceRecordsActualAcceptedReplyTimes(t *testing.T) {
	engine, block := newTraceTestEngine(t, 2, 2)
	ctx := tracedTestQuery(t, engine, block, 0, map[types.NodeID]int{"peer-a": 1, "peer-b": 1})
	ctx.startTime = time.Now().Add(-time.Second)
	before := time.Now()
	engine.SubmitChit("peer-a", ctx.queryKey, block.ID, []byte("verified-by-query-manager"))
	first := ctx.trace.replies["peer-a"]
	if first.Before(before) || !first.After(ctx.startTime) || first.After(time.Now()) {
		t.Fatalf("reply timestamp = %v, start = %v, want actual SubmitChit entry time", first, ctx.startTime)
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			engine.SubmitChit("peer-a", ctx.queryKey, block.ID, nil)
			engine.SubmitChit("unsampled", ctx.queryKey, block.ID, nil)
		}()
	}
	wg.Wait()
	engine.SubmitChit("peer-b", ctx.queryKey, block.ID, []byte("verified-by-query-manager"))
	if len(ctx.trace.replies) != 2 || ctx.trace.replies["peer-a"] != first || ctx.trace.firstReplyAt != first {
		t.Fatalf("duplicate/unsampled response changed trace: %+v", ctx.trace)
	}
	if ctx.trace.lastReplyAt != ctx.trace.replies["peer-b"] || ctx.trace.networkDoneAt.Before(ctx.trace.lastReplyAt) {
		t.Fatal("reply/completion timing is not monotonic")
	}
}

func TestPollTracePeerSummaryIsSortedAndIncludesAbstentions(t *testing.T) {
	engine, block := newTraceTestEngine(t, 4, 3)
	ctx := tracedTestQuery(t, engine, block, 0, map[types.NodeID]int{"peer-c": 1, "peer-a": 2, "peer-b": 1})
	engine.SubmitChit("peer-b", ctx.queryKey, "", nil)
	engine.SubmitChit("peer-a", ctx.queryKey, block.ID, nil)
	engine.expireQuery(ctx.queryKey, ctx)
	sample, received, missing, votes := pollTracePeers(ctx)
	if sample != "peer-a:2,peer-b:1,peer-c:1" || missing != "peer-c:1" || votes != block.ID+":2" {
		t.Fatalf("unexpected summary: sample=%s received=%s missing=%s votes=%s", sample, received, missing, votes)
	}
	if !strings.HasPrefix(received, "peer-a:2@") || !strings.Contains(received, "="+block.ID+"/signed:false,peer-b:1@") || !strings.HasSuffix(received, "=/signed:false") {
		t.Fatalf("received summary must sort peers and keep abstention: %s", received)
	}
	if ctx.trace.hard || ctx.trace.confidenceAfter != 0 || ctx.trace.maxValidVoteWeight != 2 {
		t.Fatalf("timeout incorrectly counted hard majority: %+v", ctx.trace)
	}
}

func TestPollTraceSamplingAndAbsentTimes(t *testing.T) {
	start := time.Now()
	ctx := &QueryContext{startTime: start, seqID: 1}
	if pollTraceSelected(ctx, "success", true, start.Add(100*time.Millisecond)) {
		t.Fatal("fast successful non-sampled poll should not log")
	}
	for _, test := range []struct {
		reason  string
		hard    bool
		elapsed time.Duration
	}{
		{"timeout", false, time.Millisecond},
		{"success", false, time.Millisecond},
		{"success", true, 500 * time.Millisecond},
	} {
		if !pollTraceSelected(ctx, test.reason, test.hard, start.Add(test.elapsed)) {
			t.Fatalf("slow/failed poll was sampled out: %+v", test)
		}
	}
	ctx.seqID = 16
	if !pollTraceSelected(ctx, "success", true, start.Add(time.Millisecond)) {
		t.Fatal("one in 16 fast successful polls must be sampled")
	}
	if got := pollTraceMillis(start, time.Time{}); got != -1 {
		t.Fatalf("missing reply time = %v, want -1", got)
	}
	trace := newPollTrace()
	trace.recordReply("later-lock-winner", start.Add(20*time.Millisecond), true)
	trace.recordReply("earlier-arrival", start.Add(10*time.Millisecond), true)
	if trace.firstReplyAt != start.Add(10*time.Millisecond) || trace.lastReplyAt != start.Add(20*time.Millisecond) {
		t.Fatal("concurrent lock acquisition order overwrote first/last arrival time")
	}
	trace.recordReply("local-send-failure", start.Add(30*time.Millisecond), false)
	if trace.lastReplyAt != start.Add(20*time.Millisecond) || len(trace.replies) != 3 {
		t.Fatal("synthetic abstention must be recorded separately from actual reply timing")
	}
}

func TestPollTraceDisabledHasNoPerQueryAllocation(t *testing.T) {
	t.Setenv("DEX_CONSENSUS_TRACE", "0")
	engine, block := newTraceTestEngine(t, 1, 1)
	key := engine.RegisterQuery("self", 1, block.ID, 1, 0, nil, map[types.NodeID]int{"peer-a": 1})
	ctx := engine.activeQueries[key]
	if ctx == nil || ctx.trace != nil {
		t.Fatal("disabled diagnostics allocated per-query trace")
	}
	engine.SubmitChit("peer-a", key, block.ID, nil)
	if engine.GetHeightState(1).Confidence != 1 {
		t.Fatal("disabled diagnostics changed vote processing")
	}
}
