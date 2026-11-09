package consensus

import (
	"dex/pb"
	"dex/txpool"
	"fmt"
)

                                                                              
                                                               
func ValidateBlockCommitment(block *pb.Block) error {
	if block == nil || block.Header == nil {
		return fmt.Errorf("nil block/header")
	}
	if block.Header.Height == 0 && block.BlockHash == "genesis" {
		return nil
	}
	for i, tx := range block.Body {
		if tx == nil || tx.GetTxId() == "" {
			return fmt.Errorf("tx %d missing canonical tx_id", i)
		}
	}

	actualTxsHash := txpool.ComputeTxsHash(block.Body)
	if block.Header.TxsHash != actualTxsHash {
		return fmt.Errorf("txs_hash mismatch: header=%s actual=%s", block.Header.TxsHash, actualTxsHash)
	}

	converted, err := (&ConsensusAdapter{}).DBBlockToConsensus(block)
	if err != nil {
		return fmt.Errorf("convert block header: %w", err)
	}
	expectedID := converted.Header.ComputeIDForChain(pb.GetDefaultChainID())
	if block.BlockHash != expectedID {
		return fmt.Errorf("block_hash mismatch: header=%s actual=%s", block.BlockHash, expectedID)
	}
	return nil
}
