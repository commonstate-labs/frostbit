package consensus

import (
	"dex/config"
	"dex/interfaces"
	"dex/logs"
	"dex/types"
	"sync"
	"testing"
	"time"
)

type gossipRelayTestTransport struct {
	mu         sync.Mutex
	broadcasts []types.Message
}

func (t *gossipRelayTestTransport) Send(types.NodeID, types.Message) error { return nil }
func (t *gossipRelayTestTransport) Receive() <-chan types.Message          { return nil }
func (t *gossipRelayTestTransport) Broadcast(msg types.Message, _ []types.NodeID) {
	t.mu.Lock()
	t.broadcasts = append(t.broadcasts, msg)
	t.mu.Unlock()
}
func (t *gossipRelayTestTransport) SamplePeers(types.NodeID, int) []types.NodeID {
	return []types.NodeID{"next-peer"}
}
func (t *gossipRelayTestTransport) GetAllPeers(types.NodeID) []types.NodeID {
	return []types.NodeID{"next-peer"}
}
func (t *gossipRelayTestTransport) broadcastCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.broadcasts)
}

type gossipRelayTestStore struct {
	interfaces.BlockStore
	mu     sync.Mutex
	blocks map[string]*types.Block
}

func (s *gossipRelayTestStore) Add(block *types.Block) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.blocks[block.ID]; exists {
		return false, nil
	}
	s.blocks[block.ID] = block
	return true, nil
}

func (s *gossipRelayTestStore) GetLastAccepted() (string, uint64) {
	return "genesis", 0
}

func (s *gossipRelayTestStore) PrevalidateDeferredLiveBlock(block *types.Block, acceptedHeight uint64) error {
	return nil
}

type gossipRelayTestEventBus struct{}

func (*gossipRelayTestEventBus) Subscribe(types.EventType, interfaces.EventHandler) {}
func (*gossipRelayTestEventBus) Publish(interfaces.Event)                           {}
func (*gossipRelayTestEventBus) PublishAsync(interfaces.Event)                      {}

func TestHandleGossipRelaysNewBlockExactlyOnce(t *testing.T) {
	transport := &gossipRelayTestTransport{}
	store := &gossipRelayTestStore{blocks: make(map[string]*types.Block)}
	gm := NewGossipManager(
		"node-self",
		transport,
		store,
		&GossipConfig{Fanout: 1, Interval: time.Second},
		&gossipRelayTestEventBus{},
		logs.NewNodeLogger("gossip-relay-test", 0),
	)
	pm := mustNewProposalManagerWithProposer(t,
		"node-self",
		transport,
		store,
		&NodeConfig{ProposalInterval: time.Hour},
		&gossipRelayTestEventBus{},
		nil,
		config.WindowConfig{},
		logs.NewNodeLogger("gossip-relay-test", 0),
	)
	gm.SetProposalManager(pm)
	block := &types.Block{
		ID: "block-1-proposer-w1-relay-test",
		Header: types.BlockHeader{
			Height:   1,
			ParentID: "genesis",
			Proposer: "proposer",
		},
	}
	msg := types.Message{Type: types.MsgGossip, From: "source-peer", Block: block}

	gm.HandleGossip(msg)

	deadline := time.Now().Add(time.Second)
	for transport.broadcastCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := transport.broadcastCount(); got != 1 {
		t.Fatalf("new gossip block should be relayed once, got %d broadcasts", got)
	}

	gm.HandleGossip(msg)
	time.Sleep(200 * time.Millisecond)
	if got := transport.broadcastCount(); got != 1 {
		t.Fatalf("duplicate gossip block should not be relayed again, got %d broadcasts", got)
	}
}

func TestHandleGossipCacheCapacityRejectionRemainsImmediatelyRetryable(t *testing.T) {
	store := &liveWindowCountingStore{BlockStore: NewMemoryBlockStore()}
	events := &gossipRelayTestEventBus{}
	gm := NewGossipManager(
		"gossip-retry-node",
		&gossipRelayTestTransport{},
		store,
		&GossipConfig{Fanout: 1, Interval: time.Second},
		events,
		logs.NewNodeLogger(t.Name(), 0),
	)
	pm := mustNewProposalManagerWithProposer(t,
		"gossip-retry-node",
		nil,
		store,
		&NodeConfig{ProposalInterval: time.Hour},
		events,
		nil,
		config.WindowConfig{Enabled: true, Stages: []config.WindowStage{
			{Duration: time.Hour},
			{Duration: 0},
		}},
		logs.NewNodeLogger(t.Name(), 0),
	)
	gm.SetProposalManager(pm)
	pm.maxCachedProposals = 1
	fill := testFutureProposal("gossip-cache-fill", 2, "fill-proposer", 0, "parent-1")
	if _, deferred, err := pm.admitLiveBlock(fill); err != nil || !deferred {
		t.Fatalf("fill proposal cache: deferred=%t err=%v", deferred, err)
	}
	block := testFutureProposal("gossip-transient-retry", 1, "proposer", 1, "genesis")
	msg := types.Message{Type: types.MsgGossip, From: "relay", Block: block}

	gm.HandleGossip(msg)
	gm.mu.RLock()
	seenAfterFailure := gm.seenBlocks[block.ID]
	gm.mu.RUnlock()
	if seenAfterFailure {
		t.Fatal("capacity-rejected gossip was marked seen")
	}

	pm.maxCachedProposals = 2
	gm.HandleGossip(msg)
	gm.mu.RLock()
	seenAfterRetry := gm.seenBlocks[block.ID]
	gm.mu.RUnlock()
	if !seenAfterRetry {
		t.Fatal("successfully deferred gossip was not marked seen")
	}
	if count, _, _ := cachedProposalStats(pm); count != 2 {
		t.Fatalf("retried gossip cache count=%d, want 2", count)
	}
}

func TestHandleGossipDeterministicRejectionIsDeduplicated(t *testing.T) {
	store := &liveWindowCountingStore{BlockStore: NewMemoryBlockStore()}
	events := &gossipRelayTestEventBus{}
	gm := NewGossipManager(
		"gossip-invalid-node",
		&gossipRelayTestTransport{},
		store,
		&GossipConfig{Fanout: 1, Interval: time.Second},
		events,
		logs.NewNodeLogger(t.Name(), 0),
	)
	pm := mustNewProposalManagerWithProposer(t,
		"gossip-invalid-node",
		nil,
		store,
		&NodeConfig{ProposalInterval: time.Hour},
		events,
		nil,
		config.WindowConfig{Enabled: true, Stages: []config.WindowStage{
			{Duration: time.Hour},
			{Duration: 0},
		}},
		logs.NewNodeLogger(t.Name(), 0),
	)
	gm.SetProposalManager(pm)
	block := testFutureProposal("gossip-invalid-window", 1, "proposer", 2, "genesis")
	gm.HandleGossip(types.Message{Type: types.MsgGossip, From: "relay", Block: block})

	gm.mu.RLock()
	seen := gm.seenBlocks[block.ID]
	gm.mu.RUnlock()
	if !seen {
		t.Fatal("deterministically invalid gossip was left immediately retryable")
	}
	if count, _, _ := cachedProposalStats(pm); count != 0 {
		t.Fatalf("invalid gossip entered proposal cache: count=%d", count)
	}
}
