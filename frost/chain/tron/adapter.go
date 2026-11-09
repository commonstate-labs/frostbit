package tron

import (
	"dex/frost/chain"
	"dex/frost/chain/evm"
)

                                                                         
                                                                             
                                                                         
func NewAdapter(chainID uint64) *evm.EVMAdapter {
	return evm.NewEVMAdapter(chain.ChainTRX, chainID)
}
