package vm

import (
	"dex/config"
	"dex/keys"
	"dex/pb"
	"fmt"
	"sort"
	"strings"

	"google.golang.org/protobuf/proto"
)

                                                                   
func loadTransitionCreateVaultConfig(sv StateView, chain string) (*pb.FrostVaultConfig, error) {
	data, exists, err := sv.Get(keys.KeyFrostVaultConfig(chain, 0))
	if err != nil {
		return nil, fmt.Errorf("read vault config failed: %w", err)
	}
	if !exists || len(data) == 0 {
		return nil, fmt.Errorf("vault config not found")
	}

	var cfg pb.FrostVaultConfig
	if err := unmarshalProtoCompat(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid vault config")
	}
	if err := config.ValidateOnChainVaultCommitteePolicy(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

                                                                          
                                                                              
                                         
func expectedTransitionCommitteeFromActiveMiners(top *pb.FrostActiveMiners, vaultCfg *pb.FrostVaultConfig, chain string, vaultID uint32, epochID uint64) ([]string, error) {
	return expectedTransitionCommitteeExcluding(top, vaultCfg, nil)
}

func expectedTransitionCommitteeExcluding(top *pb.FrostActiveMiners, vaultCfg *pb.FrostVaultConfig, excluded map[string]bool) ([]string, error) {
	if vaultCfg == nil {
		return nil, fmt.Errorf("vault config not found")
	}
	if top == nil {
		return nil, fmt.Errorf("VaultEligible miner snapshot not found")
	}
	if err := validateFrostCandidateSnapshot(top); err != nil {
		return nil, err
	}
	committeeSize := int(vaultCfg.CommitteeSize)
	if !config.IsVaultCommitteeSize(committeeSize) {
		return nil, fmt.Errorf("unsupported target committee size %d", committeeSize)
	}
	if len(top.Addresses) < committeeSize {
		return nil, fmt.Errorf("insufficient VaultEligible miners: have=%d need=%d", len(top.Addresses), committeeSize)
	}
	result := make([]string, 0, committeeSize)
	seen := make(map[string]struct{}, committeeSize)
	for _, address := range top.Addresses {
		if address == "" || excluded[address] {
			continue
		}
		if _, duplicate := seen[address]; duplicate {
			return nil, fmt.Errorf("duplicate VaultEligible miner %s", address)
		}
		seen[address] = struct{}{}
		result = append(result, address)
		if len(result) == committeeSize {
			break
		}
	}
	if len(result) != committeeSize {
		return nil, fmt.Errorf("incomplete target committee: have=%d need=%d", len(result), committeeSize)
	}
	return result, nil
}

                                                                              
                                                                               
                                                                         
                                                   
func validateFrostCandidateSnapshot(top *pb.FrostActiveMiners) error {
	return config.ValidateVaultCandidateSnapshot(top)
}

func targetVaultCommitteeSize(top *pb.FrostActiveMiners, vaultCfg *pb.FrostVaultConfig, currentSize int) (int, error) {
	if top == nil || vaultCfg == nil {
		return 0, fmt.Errorf("missing dynamic committee input")
	}
	eligibleCount := top.EligibleCount
	multiplier := vaultCfg.CommitteeScaleMinerMultiplier
	maxSize := int(vaultCfg.CommitteeMaxSize)
	return config.TargetVaultCommitteeSize(eligibleCount, multiplier, maxSize, currentSize)
}

func committeeSizeForFrozenThreshold(threshold uint32) (int, error) {
	if threshold == 0 || (uint64(threshold)*4)%3 != 0 {
		return 0, fmt.Errorf("invalid frozen FROST threshold %d", threshold)
	}
	size := int(uint64(threshold) * 4 / 3)
	exact, err := config.ComputeVaultCommitteeThreshold(size)
	if err != nil || exact != int(threshold) {
		return 0, fmt.Errorf("frozen threshold %d does not map to a protocol committee tier", threshold)
	}
	return size, nil
}

func frostTransitionLifecycleForOldKey(oldGroupPubkey []byte) string {
	if len(oldGroupPubkey) > 0 {
		return VaultLifecycleDraining
	}
	return VaultLifecycleActive
}

                                                                         
func buildAutoTransitionState(
	chain string, vaultID uint32, epochID uint64, attempt uint32,
	signAlgo pb.SignAlgo, triggerHeight uint64,
	oldCommittee, newCommittee []string, oldGroupPubkey []byte,
	cfg *pb.FrostVaultConfig,
) (*pb.VaultTransitionState, error) {
	threshold, err := config.ComputeVaultCommitteeThreshold(len(newCommittee))
	if err != nil {
		return nil, err
	}

	commitWindow := cfg.DkgCommitWindowBlocks
	sharingWindow := cfg.DkgSharingWindowBlocks
	disputeWindow := cfg.DkgDisputeWindowBlocks
	if commitWindow == 0 || sharingWindow == 0 || disputeWindow == 0 {
		return nil, fmt.Errorf("on-chain DKG windows must all be positive")
	}

	commitDeadline := triggerHeight + commitWindow - 1
	sharingDeadline := commitDeadline + sharingWindow
	disputeDeadline := sharingDeadline + disputeWindow

	return &pb.VaultTransitionState{
		Chain:               chain,
		VaultId:             vaultID,
		EpochId:             epochID,
		SignAlgo:            signAlgo,
		TriggerHeight:       triggerHeight,
		OldCommitteeMembers: oldCommittee,
		NewCommitteeMembers: newCommittee,
		DkgStatus:           DKGStatusNotStarted,
		DkgSessionId:        fmt.Sprintf("%s_%d_%d_%d", chain, vaultID, epochID, attempt),
		DkgThresholdT:       uint32(threshold),
		DkgN:                uint32(len(newCommittee)),
		DkgCommitDeadline:   commitDeadline,
		DkgSharingDeadline:  sharingDeadline,
		DkgDisputeDeadline:  disputeDeadline,
		OldGroupPubkey:      oldGroupPubkey,
		ValidationStatus:    "NOT_STARTED",
		Lifecycle:           frostTransitionLifecycleForOldKey(oldGroupPubkey),
		DkgAttempt:          attempt,
	}, nil
}

                                                                                   
func discoverFrostChains(scanFn func(prefix string) (map[string][]byte, error)) []string {
	chains, _, _ := discoverFrostChainConfigs(scanFn)
	return chains
}

func discoverFrostChainConfigs(scanFn func(prefix string) (map[string][]byte, error)) ([]string, map[string][]byte, error) {
	prefix := keys.KeyFrostVaultConfig("", 0)
	                                                                         
	scanPrefix := strings.TrimSuffix(prefix, "_0")

	kv, err := scanFn(scanPrefix)
	if err != nil {
		return nil, nil, err
	}

	chainSet := make(map[string]bool)
	for _, v := range kv {
		var cfg pb.FrostVaultConfig
		if err := proto.Unmarshal(v, &cfg); err == nil && cfg.Chain != "" {
			chainSet[cfg.Chain] = true
		}
	}

	chains := make([]string, 0, len(chainSet))
	for c := range chainSet {
		chains = append(chains, c)
	}
	sort.Strings(chains)                   
	return chains, kv, nil
}
