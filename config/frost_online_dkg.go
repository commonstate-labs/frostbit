package config

import (
	"fmt"

	"dex/frost/chain"
	"dex/frost/chain/evm"
)

                                                                             
                                                                               
                                                                         
func ValidateOnlineDKGVaultRefs(cfg FrostConfig) error {
	for chainName, chainCfg := range cfg.Chains {
		if len(chainCfg.OnlineDKGVaultRefs) == 0 {
			continue
		}
		name := chain.NormalizeChain(chainName)
		if name == chain.ChainBTC {
			return fmt.Errorf("chain %s cannot configure online-DKG contract references", chainName)
		}
		if !chain.IsValidChain(name) {
			return fmt.Errorf("unsupported chain %q", chainName)
		}
		if chainCfg.VaultsPerChain <= 0 || len(chainCfg.OnlineDKGVaultRefs) != chainCfg.VaultsPerChain {
			return fmt.Errorf("chain %s onlineDkgVaultRefs count mismatch: have %d want %d", chainName, len(chainCfg.OnlineDKGVaultRefs), chainCfg.VaultsPerChain)
		}
		seen := make(map[string]struct{}, len(chainCfg.OnlineDKGVaultRefs))
		for i, ref := range chainCfg.OnlineDKGVaultRefs {
			canonical, err := evm.NormalizeAccountAddress(name, ref, false)
			if err != nil {
				return fmt.Errorf("chain %s onlineDkgVaultRefs[%d] is invalid: %w", chainName, i, err)
			}
			if _, exists := seen[canonical]; exists {
				return fmt.Errorf("chain %s onlineDkgVaultRefs[%d] duplicates another vault", chainName, i)
			}
			seen[canonical] = struct{}{}
		}
	}
	return nil
}
