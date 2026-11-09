package vm

import (
	"dex/config"
	"dex/witness"
)

                                                               
                                                      
                                                              
func RegisterDefaultHandlers(reg *HandlerRegistry, args ...interface{}) error {
	var cfg *config.Config
	var witnessSvc *witness.Service

	for _, arg := range args {
		switch v := arg.(type) {
		case *config.Config:
			cfg = v
		case *witness.Service:
			witnessSvc = v
		}
	}

	handlers := []TxHandler{
		&IssueTokenTxHandler{},                
		&FreezeTxHandler{},                                
		&TransferTxHandler{},                  
		&OrderTxHandler{},                     
		&MinerTxHandler{},                     
		&MinerInfoTxHandler{},                                        
		                                
		&WitnessStakeTxHandler{},                                   
		&WitnessRequestTxHandler{},                          
		&WitnessVoteTxHandler{},                       
		&WitnessChallengeTxHandler{},            
		&ArbitrationVoteTxHandler{},                   
		&WitnessClaimRewardTxHandler{},                
		&WitnessClaimStakeTxHandler{},                                         
		                              
		&FrostWithdrawRequestTxHandler{},                      
		&FrostWithdrawCancelTxHandler{},                                            
		&FrostWithdrawClaimTxHandler{},                                 
		&FrostWithdrawSignedTxHandler{},                             
		                                        
		&FrostVaultDkgCommitTxHandler{},                                 
		&FrostVaultDkgShareTxHandler{},                                      
		&FrostVaultDkgComplaintTxHandler{},                     
		&FrostVaultDkgRevealTxHandler{},                        
		&FrostVaultDkgValidationSignedTxHandler{},                    
		&FrostVaultTransitionSignedTxHandler{},                         
		                                    
		&FutureOrderTxHandler{},                             
		&FutureAddMarginTxHandler{},                      
		&FutureRemoveMarginTxHandler{},                   
		&FutureOrderCancelTxHandler{},                                
		                              
		&MintTokenTxHandler{},                  
		&BurnTokenTxHandler{},                  
		&TransferOwnershipTxHandler{},                   
		                          
		&CertSignTxHandler{},                    
		               
		&SetReferrerTxHandler{},                   
	}

	for _, h := range handlers {
		                                                   
		if awareness, ok := h.(WitnessServiceAware); ok && witnessSvc != nil {
			awareness.SetWitnessService(witnessSvc)
		}

		                                                        
		if wh, ok := h.(*WitnessRequestTxHandler); ok && cfg != nil {
			wh.VaultCount = uint32(cfg.Frost.Vault.Count)
		} else if wh, ok := h.(*WitnessRequestTxHandler); ok {
			                           
			wh.VaultCount = 100
		}

		                                 
		if csh, ok := h.(*WitnessClaimStakeTxHandler); ok && cfg != nil {
			csh.UnstakeLockBlocks = cfg.Witness.UnstakeLockBlocks
		}

		if err := reg.Register(h); err != nil {
			return err
		}
	}

	return nil
}
