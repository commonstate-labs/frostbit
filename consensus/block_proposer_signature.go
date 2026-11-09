package consensus

import (
	"fmt"

	"dex/pb"
	"dex/utils"
)

const blockProposerSignatureDomain = "dex/block-proposer/v1"

func blockProposerSignatureMessage(chainID uint64, blockID string) string {
	return fmt.Sprintf("%s\x00%d\x00%s", blockProposerSignatureDomain, chainID, blockID)
}

func signBlockProposal(signer *utils.KeyManager, blockID string) ([]byte, error) {
	if blockID == "" {
		return nil, fmt.Errorf("empty block ID")
	}
	signature, err := signer.SignBLS([]byte(blockProposerSignatureMessage(pb.GetDefaultChainID(), blockID)))
	if err != nil {
		return nil, fmt.Errorf("sign block proposal: %w", err)
	}
	return signature, nil                                               
}

                                                                            
                                                                        
                                                                               
                                                                             
                                                                              
                                                                                 
                                                             
                                      
func validateBlockProposerSignature(block *pb.Block) error {
	if block == nil || block.Header == nil {
		return fmt.Errorf("block is missing header")
	}
	if block.Header.Height == 0 {
		return nil
	}
	if block.BlockHash == "" {
		return fmt.Errorf("block is missing block ID")
	}
	if len(block.MinerSignature) == 0 {
		return fmt.Errorf("block is missing proposer signature")
	}
	                                                                             
	                                                                       
	                                                                    
	if err := ValidateBlockCommitment(block); err != nil {
		return fmt.Errorf("validate block commitment before proposer signature: %w", err)
	}

	consensusBlock, err := (&ConsensusAdapter{}).DBBlockToConsensus(block)
	if err != nil {
		return fmt.Errorf("decode block proposer: %w", err)
	}
	if err := validateHeaderMinerBinding(consensusBlock.Header); err != nil {
		return fmt.Errorf("validate block proposer identity: %w", err)
	}
	if len(consensusBlock.Header.PublicKey) == 0 {
		return fmt.Errorf("block is missing proposer public key")
	}
	publicKey, err := utils.DeserializePublicKey(consensusBlock.Header.PublicKey)
	if err != nil {
		return fmt.Errorf("decode block header key: %w", err)
	}
	if err := utils.BLSVerifySignature(
		publicKey,
		blockProposerSignatureMessage(pb.GetDefaultChainID(), block.BlockHash),
		block.MinerSignature,
	); err != nil {
		return fmt.Errorf("invalid block proposer signature: %w", err)
	}
	return nil
}
