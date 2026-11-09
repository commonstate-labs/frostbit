package vm

import (
	"fmt"
	"math"

	frostchain "dex/frost/chain"
	"dex/keys"
	"dex/pb"
)

const frostWithdrawHandoverError = "withdraw unavailable: vault authority handover in progress"

                                                                           
                                                                               
                                                                               
                                                                     
  
                                                                              
                                                                          
                               
func rejectFrostWithdrawDuringHandover(sv StateView, chainName string) error {
	chainName = frostchain.NormalizeChain(chainName)
	cfg, err := loadFrostVaultConfig(sv, chainName)
	if err != nil {
		return err
	}

	activeVaults := 0
	drainingVaults := 0
	for vaultID := uint32(0); vaultID < cfg.VaultCount; vaultID++ {
		vaultData, exists, err := sv.Get(keys.KeyFrostVaultState(chainName, vaultID))
		if err != nil {
			return fmt.Errorf("read vault %d state for handover check: %w", vaultID, err)
		}
		if !exists || len(vaultData) == 0 {
			continue
		}

		var vault pb.FrostVaultState
		if err := unmarshalProtoCompat(vaultData, &vault); err != nil {
			return fmt.Errorf("invalid vault %d state for handover check", vaultID)
		}
		if frostchain.NormalizeChain(vault.Chain) != chainName || vault.VaultId != vaultID {
			return fmt.Errorf("vault %d state identity mismatch for handover check", vaultID)
		}
		if vault.Status != VaultStatusActive {
			continue
		}
		activeVaults++
		if vault.KeyEpoch == math.MaxUint64 {
			return fmt.Errorf("vault %d epoch overflow for handover check", vaultID)
		}

		nextEpoch := vault.KeyEpoch + 1
		transitionData, exists, err := sv.Get(keys.KeyFrostVaultTransition(chainName, vaultID, nextEpoch))
		if err != nil {
			return fmt.Errorf("read vault %d handover state: %w", vaultID, err)
		}
		if !exists || len(transitionData) == 0 {
			return nil
		}

		var transition pb.VaultTransitionState
		if err := unmarshalProtoCompat(transitionData, &transition); err != nil {
			return fmt.Errorf("invalid vault %d handover state", vaultID)
		}
		if frostchain.NormalizeChain(transition.Chain) != chainName || transition.VaultId != vaultID || transition.EpochId != nextEpoch {
			return fmt.Errorf("vault %d handover state identity mismatch", vaultID)
		}
		if transition.Lifecycle != VaultLifecycleDraining {
			return nil
		}
		drainingVaults++
	}

	if activeVaults > 0 && drainingVaults == activeVaults {
		return fmt.Errorf("%s for chain=%s", frostWithdrawHandoverError, chainName)
	}
	return nil
}
