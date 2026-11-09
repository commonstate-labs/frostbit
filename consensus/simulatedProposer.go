package consensus

import (
	"dex/interfaces"
	"dex/pb"
	"dex/txpool"
	"dex/types"
	"time"
)

                                                                              
type DefaultBlockProposer struct {
	maxBlocksPerHeight int
	proposalDenom      int
}

func NewDefaultBlockProposer() interfaces.BlockProposer {
	return &DefaultBlockProposer{
		maxBlocksPerHeight: 3,
		proposalDenom:      33,
	}
}

func (p *DefaultBlockProposer) ProposeBlock(parentID string, height uint64, proposer types.NodeID, window int) (*types.Block, error) {
	txsHash := txpool.ComputeTxsHash(nil)
	header := types.BlockHeader{
		Height:   height,
		ParentID: parentID,
		Proposer: string(proposer),
		Miner:    string(proposer),
		Window:   window,
		TxHash:   txsHash,
	}
	blockID := header.ComputeIDForChain(pb.GetDefaultChainID())
	block := &types.Block{
		ID:     blockID,
		Header: header,
	}

	                                                               
	                                                         
	pbBlock := &pb.Block{
		BlockHash: blockID,
		Header: &pb.BlockHeader{
			Height:        height,
			PrevBlockHash: parentID,
			TxsHash:       txsHash,
			Miner:         string(proposer),
			Window:        int32(window),
		},
		Body: []*pb.AnyTx{},                         
	}
	CacheBlock(pbBlock)

	return block, nil
}

func (p *DefaultBlockProposer) ShouldPropose(nodeID types.NodeID, window int, currentBlocks int, currentHeight int, proposeHeight int, lastBlockTime time.Time, parentID string) bool {
	                                                                         
	if currentHeight != proposeHeight-1 {
		                                                       
		return false
	}

	                                                            
	if currentBlocks >= p.maxBlocksPerHeight {
		return false
	}

	                                                                    
	denom := p.proposalDenom
	if denom <= 0 {
		denom = 1
	}

	return int(nodeID.Last2Mod100()+window)%denom == 0
}
