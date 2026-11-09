package config

import (
	"dex/frost/chain"
	"dex/frost/chain/evm"
	"dex/protocol"
	"fmt"
	"sort"
)

                                                                                 
                                                                              
                                  
func ValidateDepositConfig(chainName string, version uint32, dexChainID uint64, vaultRefs []string) error {
	switch version {
	case 0:
		if dexChainID != 0 {
			return fmt.Errorf("disabled deposits must not configure a DEX chain ID")
		}
		return nil
	case protocol.DepositProtocolVault:
		switch chain.NormalizeChain(chainName) {
		case chain.ChainETH, chain.ChainBNB, chain.ChainBASE, chain.ChainARB, chain.ChainTRX:
		default:
			return fmt.Errorf("unified Vault deposits require a supported account chain")
		}
		if dexChainID == 0 || len(vaultRefs) == 0 {
			return fmt.Errorf("Vault deposits require a nonzero DEX chain ID and vault references")
		}
		for i, ref := range vaultRefs {
			if _, err := evm.NormalizeAccountAddress(chainName, ref, false); err != nil {
				return fmt.Errorf("invalid deposit vault %d: %w", i, err)
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported deposit protocol %d", version)
	}
}

                                                                                         
                                                                               
func ValidateDeposits(cfg FrostConfig, dexChainID uint64) error {
	names := make([]string, 0, len(cfg.Chains))
	for name := range cfg.Chains {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		c := cfg.Chains[name]
		if err := ValidateAuthorityProtocol(name, c.AuthorityProtocol, c.DepositProtocol, c.DepositDexChainID); err != nil {
			return err
		}
		if err := ValidateDepositConfig(name, c.DepositProtocol, c.DepositDexChainID, c.VaultRefs); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if c.DepositProtocol != 0 && dexChainID != 0 && c.DepositDexChainID != dexChainID {
			return fmt.Errorf("%s: depositDexChainId %d does not match DEX chain %d", name, c.DepositDexChainID, dexChainID)
		}
	}
	return nil
}

                                                                               
                                                                            
func ValidateAuthorityProtocol(name string, version, depositVersion uint32, dexChainID uint64) error {
	switch chain.NormalizeChain(name) {
	case chain.ChainBTC:
		if version != 0 {
			return fmt.Errorf("BTC cannot configure account authority protocol")
		}
		return nil
	case chain.ChainETH, chain.ChainBNB, chain.ChainBASE, chain.ChainARB, chain.ChainTRX:
	default:
		return fmt.Errorf("unsupported authority chain %q", name)
	}
	if version != FrostDualCustodyProtocol {
		return fmt.Errorf("%s: dual-custody authority protocol 3 is required", name)
	}
	if depositVersion == 0 && dexChainID == 0 {
		return nil
	}
	if depositVersion != protocol.DepositProtocolVault || dexChainID == 0 {
		return fmt.Errorf("dual-custody authority requires a receiver-bound Vault and explicit DEX chain ID")
	}
	return nil
}
