package consensus

import (
	"fmt"
	"testing"

	"dex/config"
	"dex/interfaces"
	"dex/logs"
	"dex/types"
)

func forwardDeferredLiveBlockValidation(
	store interfaces.BlockStore,
	block *types.Block,
	acceptedHeight uint64,
) error {
	validator, ok := store.(DeferredLiveBlockValidator)
	if !ok {
		return fmt.Errorf("test store %T does not implement DeferredLiveBlockValidator", store)
	}
	return validator.PrevalidateDeferredLiveBlock(block, acceptedHeight)
}

func mustNewProposalManagerWithProposer(
	t *testing.T,
	nodeID types.NodeID,
	transport interfaces.Transport,
	store interfaces.BlockStore,
	nodeConfig *NodeConfig,
	events interfaces.EventBus,
	proposer interfaces.BlockProposer,
	windowConfig config.WindowConfig,
	logger logs.Logger,
) *ProposalManager {
	t.Helper()
	pm, err := NewProposalManagerWithProposer(
		nodeID,
		transport,
		store,
		nodeConfig,
		events,
		proposer,
		windowConfig,
		logger,
	)
	if err != nil {
		t.Fatalf("create proposal manager: %v", err)
	}
	return pm
}
