package vm

import (
	"dex/config"
	"dex/frost/ceremony"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"encoding/hex"
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"
)

                                                 
func InitVaultConfig(sv StateView, chain string, cfg *pb.FrostVaultConfig) error {
	if cfg == nil {
		return errors.New("vault config is nil")
	}
	guardianConfig := config.FrostConfig{Guardian: config.GuardianConfig{BTCPublicKey: hex.EncodeToString(cfg.GuardianPubkey), AccountPublicKey: hex.EncodeToString(cfg.GuardianPubkey)}}
	if _, err := guardianConfig.GuardianPublicKey(chain); err != nil {
		return err
	}
	if cfg.Chain != chain {
		return fmt.Errorf("vault config chain mismatch: config=%q requested=%q", cfg.Chain, chain)
	}
	if err := config.ValidateOnChainVaultCommitteePolicy(cfg); err != nil {
		return err
	}
	if err := config.ValidateDepositConfig(chain, cfg.DepositProtocol, cfg.DepositDexChainId, cfg.VaultRefs); err != nil {
		return err
	}
	if err := config.ValidateAuthorityProtocol(chain, cfg.AuthorityProtocol, cfg.DepositProtocol, cfg.DepositDexChainId); err != nil {
		return err
	}
	expected := proto.Clone(cfg).(*pb.FrostVaultConfig)
	vaultCfgKey := keys.KeyFrostVaultConfig(chain, 0)
	existingData, exists, err := sv.Get(vaultCfgKey)
	if err != nil {
		return fmt.Errorf("failed to check existing config: %w", err)
	}
	if exists && len(existingData) > 0 {
		var existing pb.FrostVaultConfig
		if err := proto.Unmarshal(existingData, &existing); err != nil {
			return fmt.Errorf("failed to parse existing vault config: %w", err)
		}
		if !proto.Equal(&existing, expected) {
			return fmt.Errorf("existing vault config conflicts for chain %s", chain)
		}
		return nil
	}

	cfgData, err := proto.Marshal(expected)
	if err != nil {
		return fmt.Errorf("failed to marshal vault config: %w", err)
	}
	sv.Set(vaultCfgKey, cfgData)
	return nil
}

                                                           
func InitVaultStates(sv StateView, chain string, epochID uint64) error {
	cfg, err := loadFrostVaultConfig(sv, chain)
	if err != nil {
		return err
	}
	topK, err := loadFrostActiveMiners(sv)
	if err != nil {
		return err
	}
	vaultCommittees, err := assignInitialVaultCommittees(cfg, topK, chain, epochID)
	if err != nil {
		return err
	}

	for vaultID := uint32(0); vaultID < cfg.VaultCount; vaultID++ {
		threshold, err := config.ComputeVaultCommitteeThreshold(len(vaultCommittees[vaultID]))
		if err != nil {
			return err
		}
		vaultStateKey := keys.KeyFrostVaultState(chain, vaultID)
		existingData, exists, err := sv.Get(vaultStateKey)
		if err != nil {
			logs.Warn("[InitVaultStates] failed to check existing vault state for vault %d: %v", vaultID, err)
			continue
		}
		if exists && len(existingData) > 0 {
			logs.Debug("[InitVaultStates] vault state already exists for chain=%s vault=%d", chain, vaultID)
			continue
		}

		vaultState := &pb.FrostVaultState{
			VaultId:            vaultID,
			Chain:              chain,
			KeyEpoch:           epochID,
			SignAlgo:           cfg.SignAlgo,
			Status:             VaultStatusPending,
			CommitteeMembers:   vaultCommittees[vaultID],
			CommitteeThreshold: uint32(threshold),
		}
		if err := persistVaultState(sv, vaultStateKey, vaultState, "InitVaultStates", vaultID); err != nil {
			continue
		}
		logs.Info("[InitVaultStates] initialized vault state for chain=%s vault=%d committee_size=%d",
			chain, vaultID, len(vaultState.CommitteeMembers))
	}

	return nil
}

                                                                                                                 
                                                                                
                                                                            
func InitConfiguredActiveVaultStates(sv StateView, chain string, epochID uint64, activeSinceHeight uint64, groupPubkeys [][]byte, verifiedCommittees ...[][]string) error {
	cfg, err := loadFrostVaultConfig(sv, chain)
	if err != nil {
		return err
	}
	if len(groupPubkeys) != int(cfg.VaultCount) {
		return fmt.Errorf("initial group pubkeys count mismatch for chain %s: have %d want %d", chain, len(groupPubkeys), cfg.VaultCount)
	}

	topK, err := loadFrostActiveMiners(sv)
	if err != nil {
		return err
	}
	if len(verifiedCommittees) > 1 {
		return errors.New("multiple genesis committee overrides")
	}
	var vaultCommittees [][]string
	if len(verifiedCommittees) == 1 && verifiedCommittees[0] != nil {
		if epochID != 1 || len(verifiedCommittees[0]) != int(cfg.VaultCount) {
			return errors.New("invalid retained genesis committees")
		}
		roster := make(map[string]bool, len(topK.Addresses))
		for _, address := range topK.Addresses {
			roster[address] = true
		}
		for _, members := range verifiedCommittees[0] {
			seen := make(map[string]bool, len(members))
			for _, member := range members {
				if !roster[member] || seen[member] {
					return errors.New("retained committee contains unknown or duplicate member")
				}
				seen[member] = true
			}
		}
		vaultCommittees = verifiedCommittees[0]
	} else {
		vaultCommittees, err = assignInitialVaultCommittees(cfg, topK, chain, epochID)
		if err != nil {
			return err
		}
	}

	for vaultID := uint32(0); vaultID < cfg.VaultCount; vaultID++ {
		if len(groupPubkeys[vaultID]) == 0 {
			return fmt.Errorf("empty initial group pubkey for chain %s vault %d", chain, vaultID)
		}
		if len(vaultCommittees[vaultID]) != int(cfg.CommitteeSize) {
			return fmt.Errorf("incomplete initial committee for chain %s vault %d: have %d want %d", chain, vaultID, len(vaultCommittees[vaultID]), cfg.CommitteeSize)
		}
		threshold, err := config.ComputeVaultCommitteeThreshold(len(vaultCommittees[vaultID]))
		if err != nil {
			return err
		}
		expected := &pb.FrostVaultState{
			VaultId:            vaultID,
			Chain:              chain,
			KeyEpoch:           epochID,
			GroupPubkey:        append([]byte(nil), groupPubkeys[vaultID]...),
			SignAlgo:           cfg.SignAlgo,
			Status:             VaultStatusActive,
			ActiveSinceHeight:  activeSinceHeight,
			CommitteeMembers:   append([]string(nil), vaultCommittees[vaultID]...),
			CommitteeThreshold: uint32(threshold),
		}
		if err := activateVaultCommitteeTerm(expected, activeSinceHeight); err != nil {
			return err
		}
		vaultStateKey := keys.KeyFrostVaultState(chain, vaultID)
		existingData, exists, err := sv.Get(vaultStateKey)
		if err != nil {
			return fmt.Errorf("read configured initial vault state for chain %s vault %d: %w", chain, vaultID, err)
		}
		if exists && len(existingData) > 0 {
			var existing pb.FrostVaultState
			if err := proto.Unmarshal(existingData, &existing); err != nil {
				return fmt.Errorf("decode existing configured vault state for chain %s vault %d: %w", chain, vaultID, err)
			}
			if !proto.Equal(&existing, expected) {
				return fmt.Errorf("existing configured vault state conflicts for chain %s vault %d", chain, vaultID)
			}
			logs.Debug("[InitConfiguredActiveVaultStates] vault state already exists for chain=%s vault=%d", chain, vaultID)
			continue
		}
		if err := persistVaultState(sv, vaultStateKey, expected, "InitConfiguredActiveVaultStates", vaultID); err != nil {
			return fmt.Errorf("persist configured initial vault state for chain %s vault %d: %w", chain, vaultID, err)
		}
		logs.Info("[InitConfiguredActiveVaultStates] initialized ACTIVE vault state for chain=%s vault=%d epoch=%d committee_size=%d",
			chain, vaultID, epochID, len(expected.CommitteeMembers))
	}

	return nil
}

                                                                                  
func InitVaultTransitions(sv StateView, chain string, epochID uint64, triggerHeight uint64, commitWindow uint64, sharingWindow uint64, disputeWindow uint64) error {
	cfg, err := loadFrostVaultConfig(sv, chain)
	if err != nil {
		return err
	}

	for vaultID := uint32(0); vaultID < cfg.VaultCount; vaultID++ {
		vaultKey := keys.KeyFrostVaultState(chain, vaultID)
		vaultData, vaultExists, err := sv.Get(vaultKey)
		if err != nil {
			logs.Warn("[InitVaultTransitions] failed to read vault state for chain=%s vault=%d: %v", chain, vaultID, err)
			continue
		}

		var vaultState pb.FrostVaultState
		if vaultExists && len(vaultData) > 0 {
			if err := unmarshalProtoCompat(vaultData, &vaultState); err != nil {
				logs.Warn("[InitVaultTransitions] failed to parse vault state for chain=%s vault=%d: %v", chain, vaultID, err)
			}
		}

		effectiveEpoch := epochID
		if effectiveEpoch == 0 {
			if vaultState.KeyEpoch > 0 {
				effectiveEpoch = vaultState.KeyEpoch
			} else {
				effectiveEpoch = 1
			}
		}

		transitionKey := keys.KeyFrostVaultTransition(chain, vaultID, effectiveEpoch)
		existingData, exists, err := sv.Get(transitionKey)
		if err != nil {
			logs.Warn("[InitVaultTransitions] failed to check transition for chain=%s vault=%d: %v", chain, vaultID, err)
			continue
		}
		if exists && len(existingData) > 0 {
			continue
		}
		                                                                   
		                                                                       
		if len(vaultState.GroupPubkey) > 0 {
			continue
		}

		committeeMembers := vaultState.CommitteeMembers
		if len(committeeMembers) == 0 {
			logs.Warn("[InitVaultTransitions] empty committee for chain=%s vault=%d, skip transition", chain, vaultID)
			continue
		}

		threshold, err := config.ComputeVaultCommitteeThreshold(len(committeeMembers))
		if err != nil {
			return fmt.Errorf("compute initial DKG threshold for chain %s vault %d: %w", chain, vaultID, err)
		}

		commitDeadline := triggerHeight + commitWindow - 1
		sharingDeadline := commitDeadline + sharingWindow
		disputeDeadline := sharingDeadline + disputeWindow
		logs.Info("[InitVaultTransitions] logic: trigger(%d) + window(%d) - 1 = deadline(%d)", triggerHeight, commitWindow, commitDeadline)

		transition := &pb.VaultTransitionState{
			Chain:               chain,
			VaultId:             vaultID,
			EpochId:             effectiveEpoch,
			SignAlgo:            cfg.SignAlgo,
			TriggerHeight:       triggerHeight,
			OldCommitteeMembers: committeeMembers,
			NewCommitteeMembers: committeeMembers,
			DkgStatus:           DKGStatusCommitting,
			DkgSessionId:        fmt.Sprintf("%s_%d_%d_%d", chain, vaultID, effectiveEpoch, 1),
			DkgThresholdT:       uint32(threshold),
			DkgN:                uint32(len(committeeMembers)),
			DkgCommitDeadline:   commitDeadline,
			DkgSharingDeadline:  sharingDeadline,
			DkgDisputeDeadline:  disputeDeadline,
			OldGroupPubkey:      vaultState.GroupPubkey,
			ValidationStatus:    "NOT_STARTED",
			Lifecycle:           frostTransitionLifecycleForOldKey(vaultState.GroupPubkey),
			DkgAttempt:          1,
		}

		if err := setTransitionStateWithIndex(sv, transitionKey, transition); err != nil {
			logs.Warn("[InitVaultTransitions] failed to persist transition for chain=%s vault=%d: %v", chain, vaultID, err)
			continue
		}
		logs.Info("[InitVaultTransitions] initialized transition for chain=%s vault=%d epoch=%d committee=%d",
			chain, vaultID, effectiveEpoch, len(committeeMembers))
	}

	return nil
}

                                                                 
func UpdateVaultStateAfterDKG(sv StateView, chain string, vaultID uint32, epochID uint64, groupPubkey []byte, signAlgo pb.SignAlgo, committeeMembers []string) error {
	vaultKey := keys.KeyFrostVaultState(chain, vaultID)
	vaultData, exists, err := sv.Get(vaultKey)
	if err != nil {
		return fmt.Errorf("failed to read vault state: %w", err)
	}

	var vault *pb.FrostVaultState
	if exists && len(vaultData) > 0 {
		vault, _ = unmarshalFrostVaultState(vaultData)
	}
	if vault == nil {
		vault = &pb.FrostVaultState{
			VaultId:  vaultID,
			Chain:    chain,
			SignAlgo: signAlgo,
		}
	}

	vault.KeyEpoch = epochID
	vault.GroupPubkey = groupPubkey
	vault.Status = VaultStatusKeyReady
	if len(committeeMembers) > 0 {
		vault.CommitteeMembers = committeeMembers
		threshold, thresholdErr := config.ComputeVaultCommitteeThreshold(len(committeeMembers))
		if thresholdErr != nil {
			return thresholdErr
		}
		vault.CommitteeThreshold = uint32(threshold)
	}
	vault.PendingCommitteeDigest = nil
	vault.PendingCommitteeSinceHeight = 0

	updatedData, err := proto.Marshal(vault)
	if err != nil {
		return fmt.Errorf("failed to marshal vault state: %w", err)
	}

	sv.Set(vaultKey, updatedData)
	logs.Info("[UpdateVaultStateAfterDKG] updated vault state: chain=%s vault=%d epoch=%d status=KEY_READY",
		chain, vaultID, epochID)
	return nil
}

func loadFrostVaultConfig(sv StateView, chain string) (*pb.FrostVaultConfig, error) {
	vaultCfgKey := keys.KeyFrostVaultConfig(chain, 0)
	cfgData, exists, err := sv.Get(vaultCfgKey)
	if err != nil || !exists || len(cfgData) == 0 {
		return nil, fmt.Errorf("vault config not found for chain %s, please init vault config first", chain)
	}

	var cfg pb.FrostVaultConfig
	if err := unmarshalProtoCompat(cfgData, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse vault config: %w", err)
	}
	return &cfg, nil
}

func loadFrostActiveMiners(sv StateView) (*pb.FrostActiveMiners, error) {
	activeMinersKey := keys.KeyFrostActiveMiners()
	activeMinersData, exists, err := sv.Get(activeMinersKey)
	if err != nil || !exists || len(activeMinersData) == 0 {
		return nil, fmt.Errorf("active miners list not found, please ensure it is initialized")
	}

	var topK pb.FrostActiveMiners
	if err := proto.Unmarshal(activeMinersData, &topK); err != nil {
		return nil, fmt.Errorf("failed to parse topK: %w", err)
	}
	if err := validateFrostCandidateSnapshot(&topK); err != nil {
		return nil, err
	}
	return &topK, nil
}

func assignInitialVaultCommittees(cfg *pb.FrostVaultConfig, topK *pb.FrostActiveMiners, chain string, epochID uint64) ([][]string, error) {
	if cfg == nil || topK == nil {
		return nil, fmt.Errorf("missing initial committee input")
	}
	committeeSize := int(cfg.CommitteeSize)
	if _, err := config.ComputeVaultCommitteeThreshold(committeeSize); err != nil {
		return nil, err
	}
	if len(topK.Addresses) < committeeSize {
		return nil, fmt.Errorf("insufficient VaultEligible miners: have=%d need=%d", len(topK.Addresses), committeeSize)
	}
	selected, err := ceremony.InitialCommitteeIndicesForChain(chain, topK.Indices, topK.Addresses, committeeSize)
	if err != nil {
		return nil, err
	}
	byIndex := make(map[uint64]string, len(topK.Indices))
	for i, index := range topK.Indices {
		byIndex[index] = topK.Addresses[i]
	}
	members := make([]string, len(selected))
	for i, index := range selected {
		members[i] = byIndex[index]
	}
	committees := make([][]string, cfg.VaultCount)
	for vaultID := uint32(0); vaultID < cfg.VaultCount; vaultID++ {
		committees[vaultID] = append([]string(nil), members...)
	}

	return committees, nil
}

func persistVaultState(sv StateView, key string, state *pb.FrostVaultState, logPrefix string, vaultID uint32) error {
	stateData, err := proto.Marshal(state)
	if err != nil {
		logs.Warn("[%s] failed to marshal vault state for vault %d: %v", logPrefix, vaultID, err)
		return err
	}
	sv.Set(key, stateData)
	return nil
}
