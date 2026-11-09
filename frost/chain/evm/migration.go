package evm

import (
	"fmt"

	"dex/frost/chain"
)

                                                                              
                                                                          
                                                                               
                                      
func BuildMigrationTemplate(chainName string, chainID uint64, contractAddr string, vaultID uint32, oldEpoch, newEpoch uint64, newGroupPubkey []byte) (*chain.TemplateResult, *EVMTemplate, error) {
	return nil, nil, fmt.Errorf("single-authority migration is not supported; use the dual-authority canonical migration")
}
