package consensus

import (
	"errors"
	"strings"
	"testing"
	"time"

	"dex/config"
	"dex/interfaces"
	"dex/logs"
	"dex/pb"
	"dex/types"
)

type blockStoreWithoutDeferredValidator struct {
	interfaces.BlockStore
}

type rejectingDeferredValidatorStore struct {
	interfaces.BlockStore
	err   error
	calls int
}

func (s *rejectingDeferredValidatorStore) PrevalidateDeferredLiveBlock(*types.Block, uint64) error {
	s.calls++
	return s.err
}

func TestProposalManagerRequiresDeferredLiveBlockValidator(t *testing.T) {
	store := &blockStoreWithoutDeferredValidator{BlockStore: NewMemoryBlockStore()}
	_, err := NewProposalManagerWithProposer(
		"missing-validator",
		nil,
		store,
		&NodeConfig{ProposalInterval: time.Hour},
		NewEventBus(),
		nil,
		config.WindowConfig{},
		logs.NewNodeLogger(t.Name(), 0),
	)
	if err == nil || !strings.Contains(err.Error(), "DeferredLiveBlockValidator") {
		t.Fatalf("constructor should reject store without deferred validation capability, got %v", err)
	}
}

func TestProposalManagerRejectsMalformedWindowConfiguration(t *testing.T) {
	store := NewMemoryBlockStore()
	_, err := NewProposalManagerWithProposer(
		"invalid-window-config",
		nil,
		store,
		&NodeConfig{ProposalInterval: time.Hour},
		NewEventBus(),
		nil,
		config.WindowConfig{Enabled: true},
		logs.NewNodeLogger(t.Name(), 0),
	)
	if err == nil || !strings.Contains(err.Error(), "no stages") {
		t.Fatalf("malformed window configuration was accepted: %v", err)
	}
}

func TestProposalManagerUsesMandatoryDeferredValidator(t *testing.T) {
	rejected := errors.New("deferred payload rejected")
	store := &rejectingDeferredValidatorStore{
		BlockStore: NewMemoryBlockStore(),
		err:        rejected,
	}
	pm := mustNewProposalManagerWithProposer(t,
		"mandatory-validator",
		nil,
		store,
		&NodeConfig{ProposalInterval: time.Hour},
		NewEventBus(),
		nil,
		config.WindowConfig{
			Enabled: true,
			Stages:  []config.WindowStage{{Duration: time.Hour}, {Duration: 0}},
		},
		logs.NewNodeLogger(t.Name(), 0),
	)
	block := &types.Block{
		ID: "block-1-proposer-w1-mandatory-validator",
		Header: types.BlockHeader{
			Height:   1,
			ParentID: "genesis",
			Proposer: "proposer",
			Window:   1,
		},
	}
	added, deferred, err := pm.admitLiveBlock(block)
	if added || deferred || !errors.Is(err, rejected) {
		t.Fatalf("deferred validator result was bypassed: added=%t deferred=%t err=%v", added, deferred, err)
	}
	if store.calls != 1 {
		t.Fatalf("deferred validator calls=%d, want 1", store.calls)
	}
}

func TestLiveNetworkEntrypointsFailClosedWithoutProposalManager(t *testing.T) {
	store := &liveWindowCountingStore{BlockStore: NewMemoryBlockStore()}
	block := &types.Block{
		ID: "block-1-proposer-w0-no-proposal-manager",
		Header: types.BlockHeader{
			Height:   1,
			ParentID: "genesis",
			Proposer: "proposer",
		},
	}

	handler := NewMessageHandler(
		"message-no-proposal-manager",
		false,
		nil,
		store,
		nil,
		NewEventBus(),
		&ConsensusConfig{},
		logs.NewNodeLogger(t.Name(), 0),
	)
	if added, deferred, err := handler.admitLiveBlock(block); added || deferred || err == nil {
		t.Fatalf("message handler failed open: added=%t deferred=%t err=%v", added, deferred, err)
	}

	gm := NewGossipManager(
		"gossip-no-proposal-manager",
		&gossipRelayTestTransport{},
		store,
		&GossipConfig{Fanout: 1, Interval: time.Second},
		&gossipRelayTestEventBus{},
		logs.NewNodeLogger(t.Name(), 0),
	)
	gm.HandleGossip(types.Message{Type: types.MsgGossip, From: "relay", Block: block})

	manager := &ConsensusNodeManager{Node: &Node{}, store: store}
	err := manager.AddBlock(&pb.Block{Header: &pb.BlockHeader{Height: 1}})
	if err == nil || !strings.Contains(err.Error(), "proposal manager") {
		t.Fatalf("HTTP AddBlock failed open: %v", err)
	}

	if calls := store.calls(); calls != 0 {
		t.Fatalf("live entrypoint bypassed ProposalManager and called store.Add %d times", calls)
	}
}
