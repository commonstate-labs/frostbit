package consensus

import (
	"context"
	"dex/config"
	"dex/interfaces"
	"dex/logs"
	"dex/types"
	"fmt"
	"sync"
	"testing"
	"time"
)

type proposalTriggerCall struct {
	parentID string
	height   uint64
	window   int
}

type proposalTriggerProbe struct {
	mu          sync.Mutex
	calls       int
	shouldCalls int
	inFlight    int
	maxInFlight int
	callCh      chan proposalTriggerCall
	release     <-chan struct{}
	failures    int
}

func newProposalTriggerProbe() *proposalTriggerProbe {
	return &proposalTriggerProbe{callCh: make(chan proposalTriggerCall, 16)}
}

func (p *proposalTriggerProbe) ShouldPropose(
	types.NodeID,
	int,
	int,
	int,
	int,
	time.Time,
	string,
) bool {
	p.mu.Lock()
	p.shouldCalls++
	p.mu.Unlock()
	return true
}

func (p *proposalTriggerProbe) ProposeBlock(
	parentID string,
	height uint64,
	_ types.NodeID,
	window int,
) (*types.Block, error) {
	p.mu.Lock()
	p.calls++
	callNumber := p.calls
	shouldFail := p.failures > 0
	if shouldFail {
		p.failures--
	}
	p.inFlight++
	if p.inFlight > p.maxInFlight {
		p.maxInFlight = p.inFlight
	}
	p.mu.Unlock()

	select {
	case p.callCh <- proposalTriggerCall{parentID: parentID, height: height, window: window}:
	default:
	}
	if p.release != nil {
		<-p.release
	}

	p.mu.Lock()
	p.inFlight--
	p.mu.Unlock()

	if shouldFail {
		return nil, fmt.Errorf("injected proposal failure")
	}
	return &types.Block{
		ID: fmt.Sprintf("proposal-%d-%d", height, callNumber),
		Header: types.BlockHeader{
			Height:   height,
			ParentID: parentID,
			Window:   window,
		},
	}, nil
}

func (p *proposalTriggerProbe) snapshot() (calls, maxInFlight int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls, p.maxInFlight
}

func (p *proposalTriggerProbe) shouldCallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.shouldCalls
}

func newProposalTriggerManager(
	t *testing.T,
	store interfaces.BlockStore,
	proposer interfaces.BlockProposer,
	interval time.Duration,
	delay time.Duration,
) (*ProposalManager, interfaces.EventBus, context.CancelFunc) {
	t.Helper()
	events := NewEventBus()
	pm := mustNewProposalManagerWithProposer(t,
		"proposal-trigger-test",
		nil,
		store,
		&NodeConfig{ProposalInterval: interval},
		events,
		proposer,
		config.WindowConfig{},
		logs.NewNodeLogger(t.Name(), 0),
	)
	pm.proposalDelay = delay
	ctx, cancel := context.WithCancel(context.Background())
	pm.Start(ctx)
	t.Cleanup(cancel)
	return pm, events, cancel
}

func finalizeProposalTriggerBlock(t *testing.T, store interfaces.BlockStore, height uint64, parentID string) *types.Block {
	t.Helper()
	block := &types.Block{
		ID: fmt.Sprintf("finalized-%d", height),
		Header: types.BlockHeader{
			Height:   height,
			ParentID: parentID,
		},
	}
	added, err := store.Add(block)
	if err != nil || !added {
		t.Fatalf("add finalized fixture height %d: added=%t err=%v", height, added, err)
	}
	if err := store.SetFinalized(height, block.ID); err != nil {
		t.Fatalf("finalize fixture height %d: %v", height, err)
	}
	return block
}

func waitProposalTriggerCall(t *testing.T, ch <-chan proposalTriggerCall) proposalTriggerCall {
	t.Helper()
	select {
	case call := <-ch:
		return call
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for proposal")
		return proposalTriggerCall{}
	}
}

func TestProposalManagerFinalizationTriggersNextHeight(t *testing.T) {
	store := NewMemoryBlockStore()
	probe := newProposalTriggerProbe()
	_, events, _ := newProposalTriggerManager(t, store, probe, time.Hour, time.Millisecond)

	block := finalizeProposalTriggerBlock(t, store, 1, "genesis")
	events.Publish(types.BaseEvent{EventType: types.EventBlockFinalized, EventData: block})

	call := waitProposalTriggerCall(t, probe.callCh)
	if call.parentID != block.ID || call.height != 2 || call.window != 0 {
		t.Fatalf("unexpected proposal call: %+v", call)
	}
}

func TestProposalManagerDeduplicatesFinalizationPublishers(t *testing.T) {
	store := NewMemoryBlockStore()
	probe := newProposalTriggerProbe()
	pm, events, _ := newProposalTriggerManager(t, store, probe, time.Hour, time.Millisecond)

	block := finalizeProposalTriggerBlock(t, store, 1, "genesis")
	events.Publish(types.BaseEvent{EventType: types.EventBlockFinalized, EventData: block})
	pm.mu.Lock()
	firstFinalizedAt := pm.lastBlockTime
	pm.mu.Unlock()
	events.Publish(types.BaseEvent{
		EventType: types.EventBlockFinalized,
		EventData: &types.BlockFinalizedData{Block: block},
	})
	events.Publish(types.BaseEvent{
		EventType: types.EventBlockFinalized,
		EventData: &types.Block{ID: "stale-genesis", Header: types.BlockHeader{Height: 0}},
	})

	waitProposalTriggerCall(t, probe.callCh)
	select {
	case call := <-probe.callCh:
		t.Fatalf("duplicate finalization triggered another proposal: %+v", call)
	case <-time.After(30 * time.Millisecond):
	}

	pm.mu.Lock()
	lastFinalizedAt := pm.lastBlockTime
	finalizedHeight := pm.finalizedHeight
	pm.mu.Unlock()
	if !lastFinalizedAt.Equal(firstFinalizedAt) || finalizedHeight != 1 {
		t.Fatalf("duplicate/stale event changed finalization state: height=%d first=%v last=%v",
			finalizedHeight, firstFinalizedAt, lastFinalizedAt)
	}
}

func TestProposalManagerTickerCannotBypassFinalizationDelay(t *testing.T) {
	store := NewMemoryBlockStore()
	probe := newProposalTriggerProbe()
	events := NewEventBus()
	pm := mustNewProposalManagerWithProposer(t,
		"proposal-delay-test",
		nil,
		store,
		&NodeConfig{ProposalInterval: time.Hour},
		events,
		probe,
		config.WindowConfig{},
		logs.NewNodeLogger(t.Name(), 0),
	)
	pm.proposalDelay = time.Hour

	block := finalizeProposalTriggerBlock(t, store, 1, "genesis")
	pm.handleBlockFinalized(types.BaseEvent{EventType: types.EventBlockFinalized, EventData: block})
	pm.proposeBlock()
	if calls, _ := probe.snapshot(); calls != 0 {
		t.Fatalf("periodic proposal bypassed finalization delay: calls=%d", calls)
	}

	pm.mu.Lock()
	pm.proposalNotBefore = time.Time{}
	pm.mu.Unlock()
	pm.proposeBlock()
	if calls, _ := probe.snapshot(); calls != 1 {
		t.Fatalf("proposal did not recover after delay elapsed: calls=%d", calls)
	}
}

func TestProposalManagerReconcilesMissedFinalizationEvent(t *testing.T) {
	store := NewMemoryBlockStore()
	probe := newProposalTriggerProbe()
	events := NewEventBus()
	pm := mustNewProposalManagerWithProposer(t,
		"proposal-reconcile-test",
		nil,
		store,
		&NodeConfig{ProposalInterval: time.Hour},
		events,
		probe,
		config.WindowConfig{},
		logs.NewNodeLogger(t.Name(), 0),
	)
	pm.proposalDelay = time.Hour

	block := finalizeProposalTriggerBlock(t, store, 1, "genesis")
	pm.proposeBlock()
	if calls, _ := probe.snapshot(); calls != 0 {
		t.Fatalf("missed finalization event bypassed grace period: calls=%d", calls)
	}

	pm.mu.Lock()
	finalizedHeight := pm.finalizedHeight
	pm.proposalNotBefore = time.Time{}
	pm.mu.Unlock()
	if finalizedHeight != block.Header.Height {
		t.Fatalf("local finalized tip was not reconciled: got=%d want=%d", finalizedHeight, block.Header.Height)
	}

	pm.proposeBlock()
	if calls, _ := probe.snapshot(); calls != 1 {
		t.Fatalf("proposal did not recover after reconciled grace period: calls=%d", calls)
	}
}

func TestProposalManagerSerializesTickerAndFinalizationTrigger(t *testing.T) {
	store := NewMemoryBlockStore()
	release := make(chan struct{})
	probe := newProposalTriggerProbe()
	probe.release = release
	_, events, cancel := newProposalTriggerManager(t, store, probe, 2*time.Millisecond, 0)

	block := finalizeProposalTriggerBlock(t, store, 1, "genesis")
	events.Publish(types.BaseEvent{EventType: types.EventBlockFinalized, EventData: block})
	waitProposalTriggerCall(t, probe.callCh)

	select {
	case call := <-probe.callCh:
		close(release)
		t.Fatalf("proposal sources ran concurrently while first call was blocked: %+v", call)
	case <-time.After(30 * time.Millisecond):
	}
	calls, maxInFlight := probe.snapshot()
	if calls != 1 || maxInFlight != 1 {
		close(release)
		t.Fatalf("proposal execution was not serialized: calls=%d max_in_flight=%d", calls, maxInFlight)
	}

	cancel()
	close(release)
}

func TestProposalManagerDiscardsProposalWhenFinalizedTipAdvancesDuringBuild(t *testing.T) {
	store := NewMemoryBlockStore()
	block1 := finalizeProposalTriggerBlock(t, store, 1, "genesis")
	release := make(chan struct{})
	probe := newProposalTriggerProbe()
	probe.release = release
	events := NewEventBus()
	pm := mustNewProposalManagerWithProposer(t,
		"proposal-stale-build-test",
		nil,
		store,
		&NodeConfig{ProposalInterval: time.Hour},
		events,
		probe,
		config.WindowConfig{},
		logs.NewNodeLogger(t.Name(), 0),
	)
	pm.proposalDelay = 0

	done := make(chan struct{})
	go func() {
		pm.proposeBlock()
		close(done)
	}()
	call := waitProposalTriggerCall(t, probe.callCh)
	if call.height != 2 || call.parentID != block1.ID {
		t.Fatalf("unexpected in-flight proposal: %+v", call)
	}

	block2 := finalizeProposalTriggerBlock(t, store, 2, block1.ID)
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stale proposal attempt did not finish")
	}

	blocks := store.GetByHeight(2)
	if len(blocks) != 1 || blocks[0].ID != block2.ID {
		t.Fatalf("stale proposal entered candidate store: %+v", blocks)
	}
	pm.mu.Lock()
	finalizedHeight := pm.finalizedHeight
	pm.mu.Unlock()
	if finalizedHeight != 2 {
		t.Fatalf("proposal manager did not reconcile advanced tip: got=%d want=2", finalizedHeight)
	}
}

func TestProposalManagerFinalizationTriggerHonorsCatchUpPause(t *testing.T) {
	store := NewMemoryBlockStore()
	probe := newProposalTriggerProbe()
	events := NewEventBus()
	syncManager := &SyncManager{
		store:        store,
		config:       &SyncConfig{ChitSoftGap: 1},
		PeerHeights:  make(map[types.NodeID]uint64),
		SyncRequests: make(map[uint32]time.Time),
		Syncing:      true,
		activeSession: &SyncSession{
			Stage: SyncStageStateSnapshot, CertifiedHeight: 10,
			CertStateRoot: make([]byte, 32), CertBlockHash: "verified-checkpoint",
		},
	}
	pm := mustNewProposalManagerWithProposer(t,
		"proposal-catch-up-test",
		nil,
		store,
		&NodeConfig{ProposalInterval: time.Hour},
		events,
		probe,
		config.WindowConfig{},
		logs.NewNodeLogger(t.Name(), 0),
	)
	pm.proposalDelay = 0
	pm.node = &Node{SyncManager: syncManager, Stats: NewNodeStats(events)}
	ctx, cancel := context.WithCancel(context.Background())
	pm.Start(ctx)
	t.Cleanup(cancel)

	block1 := finalizeProposalTriggerBlock(t, store, 1, "genesis")
	events.Publish(types.BaseEvent{EventType: types.EventBlockFinalized, EventData: block1})
	select {
	case call := <-probe.callCh:
		t.Fatalf("catch-up pause was bypassed: %+v", call)
	case <-time.After(30 * time.Millisecond):
	}

	syncManager.Mu.Lock()
	syncManager.Syncing = false
	syncManager.activeSession = nil
	syncManager.Mu.Unlock()
	call := waitProposalTriggerCall(t, probe.callCh)
	if call.height != 2 || call.parentID != block1.ID {
		t.Fatalf("proposal did not recover after catch-up: %+v", call)
	}
}

func TestProposalManagerExistingCandidateSuppressesLaterWindowProposal(t *testing.T) {
	store := NewMemoryBlockStore()
	candidate := &types.Block{
		ID: "remote-candidate",
		Header: types.BlockHeader{
			Height:   1,
			ParentID: "genesis",
			Window:   0,
		},
	}
	added, err := store.Add(candidate)
	if err != nil || !added {
		t.Fatalf("add remote candidate: added=%t err=%v", added, err)
	}

	probe := newProposalTriggerProbe()
	pm := mustNewProposalManagerWithProposer(t,
		"proposal-existing-candidate-test",
		nil,
		store,
		&NodeConfig{ProposalInterval: time.Hour},
		NewEventBus(),
		probe,
		config.WindowConfig{
			Enabled: true,
			Stages: []config.WindowStage{
				{Duration: time.Second},
				{Duration: time.Second},
				{Duration: 0},
			},
		},
		logs.NewNodeLogger(t.Name(), 0),
	)
	pm.proposalDelay = 0
	pm.lastBlockTime = time.Now().Add(-3 * time.Second)

	pm.proposeBlock()

	if calls, _ := probe.snapshot(); calls != 0 {
		t.Fatalf("built a new candidate despite admitted candidate: calls=%d", calls)
	}
	if calls := probe.shouldCallCount(); calls != 0 {
		t.Fatalf("evaluated local eligibility despite admitted candidate: calls=%d", calls)
	}
}

func TestProposalManagerCandidateArrivingDuringBuildSuppressesLocalAdd(t *testing.T) {
	store := NewMemoryBlockStore()
	release := make(chan struct{})
	probe := newProposalTriggerProbe()
	probe.release = release
	pm := mustNewProposalManagerWithProposer(t,
		"proposal-build-race-test",
		nil,
		store,
		&NodeConfig{ProposalInterval: time.Hour},
		NewEventBus(),
		probe,
		config.WindowConfig{},
		logs.NewNodeLogger(t.Name(), 0),
	)
	pm.proposalDelay = 0

	done := make(chan struct{})
	go func() {
		pm.proposeBlock()
		close(done)
	}()
	call := waitProposalTriggerCall(t, probe.callCh)
	remote := &types.Block{
		ID: "remote-during-build",
		Header: types.BlockHeader{
			Height:   call.height,
			ParentID: call.parentID,
			Window:   call.window,
		},
	}
	added, err := store.Add(remote)
	if err != nil || !added {
		close(release)
		t.Fatalf("add racing remote candidate: added=%t err=%v", added, err)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("proposal attempt did not finish")
	}

	blocks := store.GetByHeight(call.height)
	if len(blocks) != 1 || blocks[0].ID != remote.ID {
		t.Fatalf("local candidate entered store after remote won build race: %+v", blocks)
	}
}

type proposalCandidateHidingStore struct {
	interfaces.BlockStore
	hideHeight uint64
	hide       bool
}

func (s *proposalCandidateHidingStore) PrevalidateDeferredLiveBlock(block *types.Block, acceptedHeight uint64) error {
	return forwardDeferredLiveBlockValidation(s.BlockStore, block, acceptedHeight)
}

func (s *proposalCandidateHidingStore) GetByHeight(height uint64) []*types.Block {
	if s.hide && height == s.hideHeight {
		return nil
	}
	return s.BlockStore.GetByHeight(height)
}

func TestProposalManagerProposerAddsAtMostOneCandidatePerHeight(t *testing.T) {
	store := &proposalCandidateHidingStore{
		BlockStore: NewMemoryBlockStore(),
		hideHeight: 1,
	}
	probe := newProposalTriggerProbe()
	pm := mustNewProposalManagerWithProposer(t,
		"proposal-one-per-height-test",
		nil,
		store,
		&NodeConfig{ProposalInterval: time.Hour},
		NewEventBus(),
		probe,
		config.WindowConfig{},
		logs.NewNodeLogger(t.Name(), 0),
	)
	pm.proposalDelay = 0

	pm.proposeBlock()
	store.hide = true                                                                 
	pm.proposeBlock()

	if calls, _ := probe.snapshot(); calls != 1 {
		t.Fatalf("same proposer built more than one candidate for a height: calls=%d", calls)
	}
	if got := pm.proposedHeights[1]; got == "" {
		t.Fatal("successful proposal height was not retained")
	}
}

func TestProposalManagerFailedBuildCanRetrySameHeight(t *testing.T) {
	store := NewMemoryBlockStore()
	probe := newProposalTriggerProbe()
	probe.failures = 1
	pm := mustNewProposalManagerWithProposer(t,
		"proposal-build-retry-test",
		nil,
		store,
		&NodeConfig{ProposalInterval: time.Hour},
		NewEventBus(),
		probe,
		config.WindowConfig{},
		logs.NewNodeLogger(t.Name(), 0),
	)
	pm.proposalDelay = 0

	pm.proposeBlock()
	pm.proposeBlock()

	if calls, _ := probe.snapshot(); calls != 2 {
		t.Fatalf("failed build left height reserved: calls=%d", calls)
	}
	blocks := store.GetByHeight(1)
	if len(blocks) != 1 {
		t.Fatalf("retry did not add exactly one candidate: %+v", blocks)
	}
}

type proposalAdmissionRaceStore struct {
	interfaces.BlockStore
	remote    *types.Block
	once      sync.Once
	discarded []string
}

func (s *proposalAdmissionRaceStore) PrevalidateDeferredLiveBlock(block *types.Block, acceptedHeight uint64) error {
	return forwardDeferredLiveBlockValidation(s.BlockStore, block, acceptedHeight)
}

func (s *proposalAdmissionRaceStore) Add(block *types.Block) (bool, error) {
	s.once.Do(func() {
		_, _ = s.BlockStore.Add(s.remote)
	})
	return s.BlockStore.Add(block)
}

func (s *proposalAdmissionRaceStore) DiscardCandidate(_ uint64, blockID string) bool {
	s.discarded = append(s.discarded, blockID)
	return true
}

func TestProposalManagerCandidateWinningAddRaceSuppressesBroadcast(t *testing.T) {
	baseStore := NewMemoryBlockStore()
	store := &proposalAdmissionRaceStore{
		BlockStore: baseStore,
		remote: &types.Block{
			ID: "remote-before-local-add",
			Header: types.BlockHeader{
				Height:   1,
				ParentID: "genesis",
			},
		},
	}
	events := NewEventBus()
	published := make(chan *types.Block, 1)
	events.Subscribe(types.EventNewBlock, func(event interfaces.Event) {
		if block, ok := event.Data().(*types.Block); ok {
			published <- block
		}
	})
	probe := newProposalTriggerProbe()
	pm := mustNewProposalManagerWithProposer(t,
		"proposal-add-race-test",
		nil,
		store,
		&NodeConfig{ProposalInterval: time.Hour},
		events,
		probe,
		config.WindowConfig{},
		logs.NewNodeLogger(t.Name(), 0),
	)
	pm.proposalDelay = 0

	pm.proposeBlock()

	if len(store.discarded) != 1 {
		t.Fatalf("locally-added race loser was not discarded: %v", store.discarded)
	}
	select {
	case block := <-published:
		t.Fatalf("broadcast local race loser %s", block.ID)
	case <-time.After(30 * time.Millisecond):
	}
}

func TestRealBlockProposerDoesNotExpandCandidateCapInLaterWindows(t *testing.T) {
	proposer := &RealBlockProposer{maxBlocksPerHeight: 2}
	for _, window := range []int{0, 2, 3} {
		if proposer.ShouldPropose(
			"candidate-cap-test",
			window,
			2,
			0,
			1,
			time.Now(),
			"genesis",
		) {
			t.Fatalf("window %d expanded fixed candidate cap", window)
		}
	}
}
