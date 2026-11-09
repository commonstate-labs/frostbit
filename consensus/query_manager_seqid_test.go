package consensus

import (
	"bytes"
	"context"
	"dex/logs"
	"dex/types"
	"fmt"
	"sync"
	"testing"
	"time"
)

type querySeqStore struct {
	block *types.Block
}

func (s *querySeqStore) Add(*types.Block) (bool, error) { return false, nil }
func (s *querySeqStore) Get(id string) (*types.Block, bool) {
	if s.block != nil && s.block.ID == id {
		return s.block, true
	}
	return nil, false
}
func (s *querySeqStore) GetByHeight(height uint64) []*types.Block {
	if s.block != nil && s.block.Header.Height == height {
		return []*types.Block{s.block}
	}
	return nil
}
func (s *querySeqStore) GetLastAccepted() (string, uint64) { return "genesis", 0 }
func (s *querySeqStore) GetFinalizedAtHeight(height uint64) (*types.Block, bool) {
	if height == 0 {
		return &types.Block{ID: "genesis", Header: types.BlockHeader{Height: 0}}, true
	}
	return nil, false
}
func (s *querySeqStore) GetBlocksFromHeight(uint64, uint64) []*types.Block { return nil }
func (s *querySeqStore) GetCurrentHeight() uint64                          { return 0 }
func (s *querySeqStore) GetPendingBlocksCount() int                        { return 0 }
func (s *querySeqStore) GetPendingBlocks() []*types.Block                  { return nil }
func (s *querySeqStore) SetFinalized(uint64, string) error                 { return nil }

type querySeqEngine struct {
	mu            sync.Mutex
	blockID       string
	seqIDs        []uint32
	registeredAt  []time.Time
	sampleWeights []map[types.NodeID]int
	submitted     []querySeqSubmittedChit
}

type querySeqSubmittedChit struct {
	peer        types.NodeID
	queryKey    string
	preferredID string
}

type queryPipelineSeqEngine struct {
	querySeqEngine
	networkActive    int
	totalOutstanding int
}

func (e *queryPipelineSeqEngine) GetQueryPipelineCounts(uint64) (int, int) {
	return e.networkActive, e.totalOutstanding
}

func (e *querySeqEngine) Start(context.Context) error { return nil }
func (e *querySeqEngine) RegisterQuery(_ types.NodeID, requestID uint32, _ string, _ uint64, seqID uint32, _ []byte, weights map[types.NodeID]int) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seqIDs = append(e.seqIDs, seqID)
	e.registeredAt = append(e.registeredAt, time.Now())
	weightCopy := make(map[types.NodeID]int, len(weights))
	for peer, weight := range weights {
		weightCopy[peer] = weight
	}
	e.sampleWeights = append(e.sampleWeights, weightCopy)
	return fmt.Sprintf("q-%d", requestID)
}

func TestPollSchedulerUsesSingleConfiguredInterval(t *testing.T) {
	const interval = 40 * time.Millisecond
	block := &types.Block{
		ID: "block-1-peer-a-w0-scheduler",
		Header: types.BlockHeader{
			Height:   1,
			ParentID: "genesis",
			Proposer: "peer-a",
			Window:   0,
		},
	}
	store := &querySeqStore{block: block}
	engine := &querySeqEngine{blockID: block.ID}
	transport := &querySeqTransport{}
	qm := NewQueryManager(
		"self",
		transport,
		store,
		engine,
		&ConsensusConfig{K: 2, AlphaPreference: 1, AlphaConfidence: 1, Beta: 15, PollInterval: interval, MaxConcurrentQueries: 8},
		NewEventBus(),
		logs.NewNodeLogger("query-scheduler-test", 0),
	)
	qm.SetLivenessTracker(verifiedParticipantTrackerForTest())

	ctx, cancel := context.WithCancel(context.Background())
	qm.Start(ctx)
	qm.Start(ctx)                                                          
	for i := 0; i < 100; i++ {
		qm.tryIssueQuery()
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		engine.mu.Lock()
		count := len(engine.registeredAt)
		engine.mu.Unlock()
		if count >= 4 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("scheduler registered fewer than 4 polls before timeout: %d", count)
		}
		time.Sleep(5 * time.Millisecond)
	}

	engine.mu.Lock()
	times := append([]time.Time(nil), engine.registeredAt[:4]...)
	engine.mu.Unlock()
	for i := 1; i < len(times); i++ {
		if gap := times[i].Sub(times[i-1]); gap < interval-10*time.Millisecond {
			t.Fatalf("polls bypassed configured interval: gap[%d]=%v, interval=%v", i, gap, interval)
		}
	}
	time.Sleep(4 * interval)
	engine.mu.Lock()
	pipelined := len(engine.registeredAt)
	engine.mu.Unlock()
	if pipelined != maxPerHeightPollPipeline {
		t.Fatalf("per-height pipeline registered %d polls, want %d", pipelined, maxPerHeightPollPipeline)
	}

	cancel()
	time.Sleep(2 * interval)
	engine.mu.Lock()
	stoppedAt := len(engine.registeredAt)
	engine.mu.Unlock()
	time.Sleep(2 * interval)
	engine.mu.Lock()
	afterStop := len(engine.registeredAt)
	engine.mu.Unlock()
	if afterStop != stoppedAt {
		t.Fatalf("scheduler kept registering after cancellation: before=%d after=%d", stoppedAt, afterStop)
	}
}

func TestQuerySchedulerUsesEngineNetworkSlotsInsteadOfUnsettledPollObjects(t *testing.T) {
	block := &types.Block{
		ID: "block-1-peer-a-w0-pipeline-state",
		Header: types.BlockHeader{
			Height:   1,
			ParentID: "genesis",
			Proposer: "peer-a",
			Window:   0,
		},
	}
	store := &querySeqStore{block: block}
	engine := &queryPipelineSeqEngine{
		querySeqEngine:   querySeqEngine{blockID: block.ID},
		networkActive:    maxPerHeightPollPipeline - 1,
		totalOutstanding: maxPerHeightPollOutstanding - 1,
	}
	qm := NewQueryManager(
		"self",
		&querySeqTransport{},
		store,
		engine,
		&ConsensusConfig{
			K: 2, AlphaPreference: 1, AlphaConfidence: 1, Beta: 15,
			PollInterval: time.Second, MaxConcurrentQueries: 16,
		},
		NewEventBus(),
		logs.NewNodeLogger("query-pipeline-state-test", 0),
	)
	qm.SetLivenessTracker(verifiedParticipantTrackerForTest())

	                                                                          
	                                                                         
	                                                      
	for i := 0; i < maxPerHeightPollPipeline; i++ {
		qm.activePolls.Store(uint32(i+1), &Poll{height: 1})
	}

	qm.issueQueryIfReady()
	engine.mu.Lock()
	registered := len(engine.seqIDs)
	engine.mu.Unlock()
	if registered != 1 {
		t.Fatalf("scheduler registered %d polls, want 1 from authoritative free network slot", registered)
	}

	engine.totalOutstanding = maxPerHeightPollOutstanding
	qm.issueQueryIfReady()
	engine.mu.Lock()
	registered = len(engine.seqIDs)
	engine.mu.Unlock()
	if registered != 1 {
		t.Fatalf("scheduler exceeded total outstanding bound: registered=%d", registered)
	}
}

func TestCompletedPollsSettleStrictlyBySeqID(t *testing.T) {
	store := NewMemoryBlockStore()
	block := &types.Block{ID: "block-1-peer-a-w0-ordered", Header: types.BlockHeader{Height: 1, ParentID: "genesis"}}
	if added, err := store.Add(block); err != nil || !added {
		t.Fatalf("add candidate: added=%v err=%v", added, err)
	}
	engine := NewSnowmanEngine(
		"self",
		store,
		&ConsensusConfig{K: 2, AlphaPreference: 1, AlphaConfidence: 1, Beta: 15, QueryTimeout: time.Second},
		NewEventBus(),
		logs.NewNodeLogger("ordered-poll-test", 0),
	).(*SnowmanEngine)

	successPoll, err := NewPollAccumulator(
		2,
		1,
		1,
		map[types.NodeID]int{"peer-a": 1, "peer-b": 1},
		map[string]struct{}{block.ID: {}},
	)
	if err != nil {
		t.Fatalf("create success poll: %v", err)
	}
	successPoll.Record("peer-a", block.ID)
	success := &QueryContext{
		queryKey: "q-1", height: 1, seqID: 1,
		poll: successPoll, voters: map[types.NodeID]string{"peer-a": block.ID},
	}
	if settled := engine.enqueueCompletedQuery(success); len(settled) != 0 {
		t.Fatalf("seq 1 settled before seq 0: %+v", settled)
	}
	if state := engine.GetHeightState(1); state != nil && state.Confidence != 0 {
		t.Fatalf("out-of-order success changed confidence: %d", state.Confidence)
	}

	failurePoll, err := NewPollAccumulator(
		2,
		1,
		1,
		map[types.NodeID]int{"peer-a": 1, "peer-b": 1},
		map[string]struct{}{block.ID: {}},
	)
	if err != nil {
		t.Fatalf("create failure poll: %v", err)
	}
	failurePoll.Close()
	failure := &QueryContext{
		queryKey: "q-0", height: 1, seqID: 0,
		poll: failurePoll, voters: map[types.NodeID]string{},
	}
	settled := engine.enqueueCompletedQuery(failure)
	if len(settled) != 2 || settled[0].key != "q-0" || settled[1].key != "q-1" {
		t.Fatalf("settlement order = %+v, want q-0 then q-1", settled)
	}
	if got := engine.GetHeightState(1).Confidence; got != 1 {
		t.Fatalf("confidence after ordered failure/success = %d, want 1", got)
	}
}

func TestCompletedPollsReleaseNetworkSlotsWithBoundedOutstanding(t *testing.T) {
	store := NewMemoryBlockStore()
	block := &types.Block{
		ID:     "block-1-peer-a-w0-bounded-pipeline",
		Header: types.BlockHeader{Height: 1, ParentID: "genesis"},
	}
	if added, err := store.Add(block); err != nil || !added {
		t.Fatalf("add candidate: added=%v err=%v", added, err)
	}
	engine := NewSnowmanEngine(
		"self",
		store,
		&ConsensusConfig{
			K: 1, AlphaPreference: 1, AlphaConfidence: 1, Beta: 100,
		},
		NewEventBus(),
		logs.NewNodeLogger("bounded-poll-pipeline-test", 0),
	).(*SnowmanEngine)

	register := func(seq uint32) string {
		t.Helper()
		return engine.RegisterQuery(
			"self",
			seq+1,
			block.ID,
			1,
			seq,
			nil,
			map[types.NodeID]int{"peer-a": 1},
		)
	}
	complete := func(key string) {
		t.Helper()
		engine.SubmitChit("peer-a", key, block.ID, nil)
	}

	keys := make(map[uint32]string)
	for seq := uint32(0); seq < maxPerHeightPollPipeline; seq++ {
		keys[seq] = register(seq)
		if keys[seq] == "" {
			t.Fatalf("register seq %d failed", seq)
		}
		if seq == 0 {
			if duplicate := register(seq); duplicate != "" {
				t.Fatalf("registered duplicate active seq %d: %s", seq, duplicate)
			}
			if collision := engine.RegisterQuery(
				"self",
				1,
				block.ID,
				1,
				1,
				nil,
				map[types.NodeID]int{"peer-a": 1},
			); collision != "" {
				t.Fatalf("overwrote active query after request ID collision: %s", collision)
			}
		}
	}
	if key := register(4); key != "" {
		t.Fatalf("registered a fifth network-active poll: %s", key)
	}
	for seq := uint32(1); seq < maxPerHeightPollPipeline; seq++ {
		complete(keys[seq])
	}
	if duplicate := register(1); duplicate != "" {
		t.Fatalf("registered duplicate ordered-pending seq 1: %s", duplicate)
	}
	if collision := engine.RegisterQuery(
		"self",
		2,
		block.ID,
		1,
		4,
		nil,
		map[types.NodeID]int{"peer-a": 1},
	); collision != "" {
		t.Fatalf("reused request ID while its prior poll was ordered-pending: %s", collision)
	}
	if active, outstanding := engine.GetQueryPipelineCounts(1); active != 1 || outstanding != 4 {
		t.Fatalf("after out-of-order completion counts=(active=%d outstanding=%d), want (1,4)", active, outstanding)
	}

	for seq := uint32(4); seq <= 6; seq++ {
		keys[seq] = register(seq)
		if keys[seq] == "" {
			t.Fatalf("completed ordered-pending polls still consumed a network slot at seq %d", seq)
		}
		complete(keys[seq])
	}
	keys[7] = register(7)
	if keys[7] == "" {
		t.Fatal("register seq 7 failed before outstanding bound")
	}
	if active, outstanding := engine.GetQueryPipelineCounts(1); active != 2 || outstanding != maxPerHeightPollOutstanding {
		t.Fatalf("at bound counts=(active=%d outstanding=%d), want (2,%d)",
			active, outstanding, maxPerHeightPollOutstanding)
	}
	if key := register(8); key != "" {
		t.Fatalf("registered seq 8 beyond outstanding bound: %s", key)
	}

	complete(keys[0])
	if active, outstanding := engine.GetQueryPipelineCounts(1); active != 1 || outstanding != 1 {
		t.Fatalf("after closing seq gap counts=(active=%d outstanding=%d), want (1,1)", active, outstanding)
	}
	if key := register(8); key == "" {
		t.Fatal("outstanding capacity was not released after ordered settlement")
	} else {
		complete(keys[7])
		complete(key)
	}
}

func TestExternalFinalizationDrainsActiveAndOrderedPendingPolls(t *testing.T) {
	store := NewMemoryBlockStore()
	block := &types.Block{
		ID:     "block-1-peer-a-w0-external-finalization",
		Header: types.BlockHeader{Height: 1, ParentID: "genesis"},
	}
	if added, err := store.Add(block); err != nil || !added {
		t.Fatalf("add candidate: added=%v err=%v", added, err)
	}
	engine := NewSnowmanEngine(
		"self",
		store,
		&ConsensusConfig{
			K: 1, AlphaPreference: 1, AlphaConfidence: 1, Beta: 100,
		},
		NewEventBus(),
		logs.NewNodeLogger("external-finalization-pipeline-test", 0),
	).(*SnowmanEngine)

	keys := make(map[uint32]string)
	for seq := uint32(0); seq < 4; seq++ {
		keys[seq] = engine.RegisterQuery(
			"self",
			seq+1,
			block.ID,
			1,
			seq,
			nil,
			map[types.NodeID]int{"peer-a": 1},
		)
		if keys[seq] == "" {
			t.Fatalf("register seq %d failed", seq)
		}
	}
	engine.SubmitChit("peer-a", keys[1], block.ID, nil)
	engine.SubmitChit("peer-a", keys[2], block.ID, nil)
	if active, outstanding := engine.GetQueryPipelineCounts(1); active != 2 || outstanding != 4 {
		t.Fatalf("before external finalization counts=(active=%d outstanding=%d), want (2,4)",
			active, outstanding)
	}

	if err := store.SetFinalized(1, block.ID); err != nil {
		t.Fatalf("externally finalize block: %v", err)
	}
	                                                                           
	                                                             
	engine.SubmitChit("peer-a", keys[0], block.ID, nil)

	if active, outstanding := engine.GetQueryPipelineCounts(1); active != 0 || outstanding != 0 {
		t.Fatalf("after external finalization counts=(active=%d outstanding=%d), want (0,0)",
			active, outstanding)
	}
	if got := engine.GetActiveQueryCount(); got != 0 {
		t.Fatalf("external finalization left %d active queries", got)
	}
	engine.processVotesMu.Lock()
	_, hasOrderState := engine.pollOrder[1]
	engine.processVotesMu.Unlock()
	if hasOrderState {
		t.Fatal("external finalization left ordered-pending state")
	}
}
func (e *querySeqEngine) SubmitChit(peer types.NodeID, queryKey string, preferredID string, _ []byte) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.submitted = append(e.submitted, querySeqSubmittedChit{
		peer:        peer,
		queryKey:    queryKey,
		preferredID: preferredID,
	})
}
func (e *querySeqEngine) GetActiveQueryCount() int    { return 0 }
func (e *querySeqEngine) GetPreference(uint64) string { return e.blockID }

type querySeqTransport struct {
	mu         sync.Mutex
	msgs       []types.Message
	recipients [][]types.NodeID
	allPeers   []types.NodeID
	failAll    bool
}

func (t *querySeqTransport) Send(types.NodeID, types.Message) error { return nil }
func (t *querySeqTransport) Receive() <-chan types.Message          { return nil }
func (t *querySeqTransport) Broadcast(msg types.Message, peers []types.NodeID) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.msgs = append(t.msgs, msg)
	t.recipients = append(t.recipients, append([]types.NodeID(nil), peers...))
}
func (t *querySeqTransport) BroadcastQuery(msg types.Message, peers []types.NodeID, onFailure func(types.NodeID, error)) {
	t.Broadcast(msg, peers)
	if !t.failAll || onFailure == nil {
		return
	}
	for _, peer := range peers {
		onFailure(peer, fmt.Errorf("injected query send failure"))
	}
}
func (t *querySeqTransport) SamplePeers(types.NodeID, int) []types.NodeID { return nil }
func (t *querySeqTransport) GetAllPeers(exclude types.NodeID) []types.NodeID {
	if t.allPeers != nil {
		return append([]types.NodeID(nil), t.allPeers...)
	}
	peers := []types.NodeID{"peer-a", "peer-b", "peer-c"}
	out := make([]types.NodeID, 0, len(peers))
	for _, p := range peers {
		if p != exclude {
			out = append(out, p)
		}
	}
	return out
}

func TestIssueQueryAggregatesReplacementSlotsAndSendsOncePerPeer(t *testing.T) {
	block := &types.Block{
		ID: "block-1-peer-a-w0-replacement",
		Header: types.BlockHeader{
			Height:   1,
			ParentID: "genesis",
			Proposer: "peer-a",
			Window:   0,
		},
	}
	store := &querySeqStore{block: block}
	engine := &querySeqEngine{blockID: block.ID}
	transport := &querySeqTransport{
		allPeers: []types.NodeID{"peer-c", "self", "peer-a", "", "peer-b", "peer-a"},
	}
	qm := NewQueryManager(
		"self",
		transport,
		store,
		engine,
		&ConsensusConfig{
			K: 15, AlphaPreference: 8, AlphaConfidence: 12, Beta: 15,
			MaxConcurrentQueries: 8,
		},
		NewEventBus(),
		logs.NewNodeLogger("query-replacement-test", 0),
	)

	qm.issueQuery()

	engine.mu.Lock()
	if len(engine.sampleWeights) != 1 {
		engine.mu.Unlock()
		t.Fatalf("registered weighted samples = %d, want 1", len(engine.sampleWeights))
	}
	weights := engine.sampleWeights[0]
	engine.mu.Unlock()

	totalSlots := 0
	hasRepeatedPeer := false
	for peer, weight := range weights {
		if peer == "" || peer == "self" {
			t.Fatalf("invalid peer %q present in sample weights: %v", peer, weights)
		}
		totalSlots += weight
		hasRepeatedPeer = hasRepeatedPeer || weight > 1
	}
	if totalSlots != 15 {
		t.Fatalf("sample slot total = %d, want K=15 (weights=%v)", totalSlots, weights)
	}
	if !hasRepeatedPeer {
		t.Fatalf("15 slots over 3 peers did not aggregate a repeated peer: %v", weights)
	}

	transport.mu.Lock()
	if len(transport.recipients) != 1 {
		transport.mu.Unlock()
		t.Fatalf("broadcasts = %d, want 1", len(transport.recipients))
	}
	recipients := append([]types.NodeID(nil), transport.recipients[0]...)
	transport.mu.Unlock()
	if len(recipients) != len(weights) {
		t.Fatalf("network recipients = %v, weights=%v", recipients, weights)
	}
	seen := make(map[types.NodeID]struct{}, len(recipients))
	for _, peer := range recipients {
		if _, duplicate := seen[peer]; duplicate {
			t.Fatalf("peer %s received duplicate network requests: %v", peer, recipients)
		}
		seen[peer] = struct{}{}
		if weights[peer] <= 0 {
			t.Fatalf("recipient %s has no registered slot weight: %v", peer, weights)
		}
	}
}

func TestIssueQueryConsumesSeqIDBeforeAsyncCompletion(t *testing.T) {
	block := &types.Block{
		ID: "block-1-self-w0-test",
		Header: types.BlockHeader{
			Height:   1,
			ParentID: "genesis",
			Proposer: "peer-a",
			Window:   0,
		},
	}
	store := &querySeqStore{block: block}
	engine := &querySeqEngine{blockID: block.ID}
	transport := &querySeqTransport{}
	qm := NewQueryManager(
		"self",
		transport,
		store,
		engine,
		&ConsensusConfig{K: 2, AlphaPreference: 1, AlphaConfidence: 1, Beta: 1, MaxConcurrentQueries: 8},
		NewEventBus(),
		logs.NewNodeLogger("query-seq-test", 0),
	)

	qm.issueQuery()
	qm.issueQuery()

	if got, want := engine.seqIDs, []uint32{0, 1}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("registered seq IDs = %v, want %v", got, want)
	}
	if len(transport.msgs) != 2 {
		t.Fatalf("broadcast messages = %d, want 2", len(transport.msgs))
	}
	if got := []uint32{transport.msgs[0].SeqID, transport.msgs[1].SeqID}; got[0] != 0 || got[1] != 1 {
		t.Fatalf("broadcast seq IDs = %v, want [0 1]", got)
	}
	if len(transport.msgs[0].QueryNonce) != SnowmanQueryNonceSize || len(transport.msgs[1].QueryNonce) != SnowmanQueryNonceSize {
		t.Fatalf("query nonce lengths = %d/%d, want %d", len(transport.msgs[0].QueryNonce), len(transport.msgs[1].QueryNonce), SnowmanQueryNonceSize)
	}
	if bytes.Equal(transport.msgs[0].QueryNonce, transport.msgs[1].QueryNonce) {
		t.Fatal("distinct polls reused the same random query nonce")
	}
}

func TestProposerPushesFullBlockOnlyOnFirstPoll(t *testing.T) {
	block := &types.Block{
		ID: "block-1-self-w0-first-push",
		Header: types.BlockHeader{
			Height:   1,
			ParentID: "genesis",
			Proposer: "self",
		},
	}
	store := &querySeqStore{block: block}
	engine := &querySeqEngine{blockID: block.ID}
	transport := &querySeqTransport{}
	qm := NewQueryManager(
		"self",
		transport,
		store,
		engine,
		&ConsensusConfig{K: 2, AlphaPreference: 1, AlphaConfidence: 1, Beta: 1, MaxConcurrentQueries: 8},
		NewEventBus(),
		logs.NewNodeLogger("query-first-push-test", 0),
	)

	qm.issueQuery()
	qm.issueQuery()

	transport.mu.Lock()
	defer transport.mu.Unlock()
	if len(transport.msgs) != 2 {
		t.Fatalf("broadcast messages = %d, want 2", len(transport.msgs))
	}
	if first := transport.msgs[0]; first.Type != types.MsgPushQuery || first.Block != block {
		t.Fatalf("first proposer poll = type %s block=%p, want PushQuery with block %p", first.Type, first.Block, block)
	}
	if second := transport.msgs[1]; second.Type != types.MsgPullQuery || second.Block != nil {
		t.Fatalf("second proposer poll = type %s block=%p, want ID-only PullQuery", second.Type, second.Block)
	}
}

func TestKnownQuerySendFailuresSettleSampledPeersAsAbstentions(t *testing.T) {
	block := &types.Block{
		ID: "block-1-peer-a-w0-send-failure",
		Header: types.BlockHeader{
			Height:   1,
			ParentID: "genesis",
			Proposer: "peer-a",
		},
	}
	store := &querySeqStore{block: block}
	engine := &querySeqEngine{blockID: block.ID}
	transport := &querySeqTransport{failAll: true}
	qm := NewQueryManager(
		"self",
		transport,
		store,
		engine,
		&ConsensusConfig{K: 2, AlphaPreference: 1, AlphaConfidence: 1, Beta: 1, MaxConcurrentQueries: 8},
		NewEventBus(),
		logs.NewNodeLogger("query-send-failure-test", 0),
	)

	qm.issueQuery()

	engine.mu.Lock()
	defer engine.mu.Unlock()
	if len(engine.submitted) == 0 {
		t.Fatal("known send failures did not settle any sampled peer")
	}
	for _, chit := range engine.submitted {
		if chit.preferredID != "" {
			t.Fatalf("send failure submitted preference %q, want abstention", chit.preferredID)
		}
	}
}
