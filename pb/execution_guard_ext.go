package pb

import "fmt"

                                                                             
                                                                           
                                                                            
                             
  
                                                                   
                                                                          
                                                 
func ValidateTxExecutionGuard(tx *AnyTx, blockHeight uint64, blockProposer string) error {
	if !TxRequiresAccountNonce(tx) {
		return nil
	}
	base := tx.GetBase()
	if base == nil {
		return fmt.Errorf("missing base message")
	}
	if base.TargetHeight != 0 && base.TargetHeight != blockHeight {
		return fmt.Errorf("target_height %d does not match block height %d", base.TargetHeight, blockHeight)
	}
	if base.OnlyProposer != "" && base.OnlyProposer != blockProposer {
		return fmt.Errorf("only_proposer %q does not match block proposer %q", base.OnlyProposer, blockProposer)
	}
	return nil
}
