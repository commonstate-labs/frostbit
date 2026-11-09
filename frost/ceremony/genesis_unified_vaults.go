package ceremony

import (
	"errors"
	"fmt"
)

                                                                             
                                                                               
                                                                                  
func validateUnifiedGenesisVaults(statement *GenesisDeploymentStatement) error {
	unified := false
	for _, vault := range statement.Vaults {
		if vault.DepositProtocol != "" || vault.DEXChainID != 0 {
			unified = true
			break
		}
	}
	if !unified {
		if statement.DeferTRXVault {
			return errors.New("deferred TRON Vault requires unified EVM deposits")
		}
		return nil
	}
	seenTRX := false
	for _, vault := range statement.Vaults {
		if vault.Chain == "btc" {
			if vault.DepositProtocol != "" || vault.DEXChainID != 0 {
				return errors.New("BTC cannot use account-chain deposit protocol")
			}
			continue
		}
		if vault.Chain == "trx" {
			seenTRX = true
			if statement.DeferTRXVault {
				if vault.DepositProtocol != "" || vault.DEXChainID != 0 {
					return errors.New("deferred TRON Vault must be withdrawal-only")
				}
				continue
			}
		}
		if vault.DepositProtocol != "DEX_VAULT_DEPOSIT_V1" || vault.DEXChainID != statement.NetworkChainID {
			return fmt.Errorf("unified deposit protocol or DEX domain missing for %s/%d", vault.Chain, vault.VaultID)
		}
	}
	if statement.DeferTRXVault && !seenTRX {
		return errors.New("deferred TRON statement is missing the real TRON Vault proof")
	}
	return nil
}
