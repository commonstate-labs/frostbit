package config

import (
	"dex/pb"
	"dex/protocol"
	"fmt"
	"math"
	"strings"
)

                                                                          
                                                                            
                           
var VaultCommitteeTiers = [...]int{4, 8, 20, 32, 64, 100}

                                                                      
                                                                            
                                                                              
                                                                           
                                                             
const VaultCandidateSnapshotLimit = 175

                                                                               
                                                                             
                                                                              
                                                   
func ValidateVaultCandidateSnapshot(top *pb.FrostActiveMiners) error {
	if top == nil {
		return fmt.Errorf("VaultEligible miner snapshot not found")
	}
	n := len(top.Addresses)
	if n > VaultCandidateSnapshotLimit {
		return fmt.Errorf("VaultEligible snapshot exceeds protocol limit: have=%d limit=%d", n, VaultCandidateSnapshotLimit)
	}
	if len(top.Indices) != n || len(top.PublicKeys) != n {
		return fmt.Errorf("VaultEligible identity field length mismatch: addresses=%d indices=%d public_keys=%d", n, len(top.Indices), len(top.PublicKeys))
	}

	genesisRoster := top.Height == 0 && top.UpdateHeight == 0
	if genesisRoster {
		if n == 0 {
			return fmt.Errorf("genesis FROST roster is empty")
		}
		if len(top.MaturedStakes) != 0 || len(top.ActiveSinceHeights) != 0 {
			return fmt.Errorf("genesis FROST roster must not claim liveness maturity fields")
		}
		if top.EligibleCount != uint32(n) {
			return fmt.Errorf("genesis FROST roster eligible_count=%d, want %d", top.EligibleCount, n)
		}
	} else {
		if top.Height == 0 || top.UpdateHeight != top.Height {
			return fmt.Errorf("VaultEligible snapshot height mismatch: height=%d update_height=%d", top.Height, top.UpdateHeight)
		}
		if len(top.MaturedStakes) != n || len(top.ActiveSinceHeights) != n {
			return fmt.Errorf("VaultEligible ranking field length mismatch: addresses=%d matured_stakes=%d active_since_heights=%d", n, len(top.MaturedStakes), len(top.ActiveSinceHeights))
		}
		if n < VaultCandidateSnapshotLimit && top.EligibleCount != uint32(n) {
			return fmt.Errorf("untruncated VaultEligible snapshot count mismatch: stored=%d eligible=%d", n, top.EligibleCount)
		}
		if n == VaultCandidateSnapshotLimit && top.EligibleCount < uint32(n) {
			return fmt.Errorf("truncated VaultEligible snapshot eligible_count=%d, want at least %d", top.EligibleCount, n)
		}
		                                                                        
		                                                                         
		                                                                         
		                          
		if n == 0 {
			return nil
		}
	}

	seenAddresses := make(map[string]struct{}, n)
	seenIndices := make(map[uint64]struct{}, n)
	for i, address := range top.Addresses {
		if strings.TrimSpace(address) == "" || address != strings.TrimSpace(address) {
			return fmt.Errorf("invalid VaultEligible miner address at rank %d", i)
		}
		if _, duplicate := seenAddresses[address]; duplicate {
			return fmt.Errorf("duplicate VaultEligible miner %s", address)
		}
		seenAddresses[address] = struct{}{}
		index := top.Indices[i]
		if index > math.MaxUint32 {
			return fmt.Errorf("VaultEligible miner index %d exceeds uint32", index)
		}
		if _, duplicate := seenIndices[index]; duplicate {
			return fmt.Errorf("duplicate VaultEligible miner index %d", index)
		}
		seenIndices[index] = struct{}{}
		if len(top.PublicKeys[i]) == 0 {
			return fmt.Errorf("empty VaultEligible public key for miner %s", address)
		}
		if genesisRoster {
			continue
		}
		if top.MaturedStakes[i] < protocol.MinMinerStakeRaw {
			return fmt.Errorf("VaultEligible matured stake below protocol minimum for miner %s", address)
		}
		if top.ActiveSinceHeights[i] > top.Height {
			return fmt.Errorf("VaultEligible active height %d exceeds snapshot height %d for miner %s", top.ActiveSinceHeights[i], top.Height, address)
		}
		if i == 0 {
			continue
		}
		previous := i - 1
		ordered := top.MaturedStakes[previous] > top.MaturedStakes[i] ||
			(top.MaturedStakes[previous] == top.MaturedStakes[i] && top.ActiveSinceHeights[previous] < top.ActiveSinceHeights[i]) ||
			(top.MaturedStakes[previous] == top.MaturedStakes[i] && top.ActiveSinceHeights[previous] == top.ActiveSinceHeights[i] && top.Indices[previous] < top.Indices[i])
		if !ordered {
			return fmt.Errorf("VaultEligible snapshot is not ranked at positions %d and %d", previous, i)
		}
	}
	return nil
}

                                                                        
func ComputeVaultCommitteeThreshold(committeeSize int) (int, error) {
	if !IsVaultCommitteeSize(committeeSize) {
		return 0, fmt.Errorf("unsupported FROST vault committee size %d", committeeSize)
	}
	return committeeSize * 3 / 4, nil
}

func IsVaultCommitteeSize(committeeSize int) bool {
	for _, tier := range VaultCommitteeTiers {
		if committeeSize == tier {
			return true
		}
	}
	return false
}

                                                                            
                                                                         
func ValidateVaultCommitteePolicy(cfg FrostConfig) error {
	if !IsVaultCommitteeSize(cfg.Vault.DefaultK) || !IsVaultCommitteeSize(cfg.Vault.MinK) || !IsVaultCommitteeSize(cfg.Vault.MaxK) {
		return fmt.Errorf("FROST vault K values must use protocol tiers %v", VaultCommitteeTiers)
	}
	if cfg.Vault.DefaultK != 4 || cfg.Vault.MinK != 4 || cfg.Vault.MaxK != 100 {
		return fmt.Errorf("FROST vault K policy must be default=4 min=4 max=100")
	}
	if cfg.Vault.ThresholdRatio != 0.75 {
		return fmt.Errorf("FROST vault threshold ratio must be exactly 0.75")
	}
	if cfg.Committee.TopN != VaultCandidateSnapshotLimit {
		return fmt.Errorf("FROST candidate topN must equal protocol limit %d", VaultCandidateSnapshotLimit)
	}
	if cfg.Committee.ThresholdRatio != 1.0 {
		return fmt.Errorf("deprecated FROST committee thresholdRatio must be fixed at 1.0")
	}
	if cfg.Vault.MaturityBlocks == 0 {
		return fmt.Errorf("FROST vault maturityBlocks must be positive")
	}
	if cfg.Vault.CommitteeTermBlocks != protocol.VaultCommitteeTermBlocks {
		return fmt.Errorf("FROST committeeTermBlocks must equal protocol term %d", protocol.VaultCommitteeTermBlocks)
	}
	if cfg.Vault.ScaleMinerMultiplier == 0 {
		return fmt.Errorf("FROST vault scaleMinerMultiplier must be positive")
	}
	if cfg.Transition.ConfirmBlocks == 0 {
		return fmt.Errorf("FROST transition confirmBlocks must be positive")
	}
	if cfg.Transition.TriggerChangeRatio != 0.2 {
		return fmt.Errorf("FROST transition triggerChangeRatio must be exactly 0.2")
	}
	if cfg.Transition.DkgCommitWindowBlocks <= 0 || cfg.Transition.DkgSharingWindowBlocks <= 0 || cfg.Transition.DkgDisputeWindowBlocks <= 0 {
		return fmt.Errorf("FROST DKG windows must all be positive")
	}
	if cfg.Vault.Count != 1 {
		return fmt.Errorf("dynamic FROST committee policy requires exactly one vault per chain")
	}
	for chain, chainCfg := range cfg.Chains {
		if chainCfg.VaultsPerChain != 1 {
			return fmt.Errorf("dynamic FROST committee policy requires one %s vault, got %d", chain, chainCfg.VaultsPerChain)
		}
	}
	return nil
}

                                                                               
                                                                            
                                                                            
                                   
func ValidateOnChainVaultCommitteePolicy(cfg *pb.FrostVaultConfig) error {
	if cfg == nil {
		return fmt.Errorf("on-chain FROST vault config is nil")
	}
	if strings.TrimSpace(cfg.Chain) == "" {
		return fmt.Errorf("on-chain FROST vault chain is empty")
	}
	if cfg.VaultCount != 1 {
		return fmt.Errorf("on-chain FROST vault count must be 1, got %d", cfg.VaultCount)
	}
	if cfg.CommitteeSize != uint32(VaultCommitteeTiers[0]) {
		return fmt.Errorf("on-chain FROST initial committee size must be 4, got %d", cfg.CommitteeSize)
	}
	if cfg.ThresholdRatio != 0.75 {
		return fmt.Errorf("on-chain FROST threshold ratio must be exactly 0.75")
	}
	if cfg.VaultMaturityBlocks == 0 {
		return fmt.Errorf("on-chain FROST vault maturity must be positive")
	}
	if cfg.CommitteeTermBlocks != protocol.VaultCommitteeTermBlocks {
		return fmt.Errorf("on-chain FROST committee term must equal %d blocks", protocol.VaultCommitteeTermBlocks)
	}
	if cfg.CommitteeScaleMinerMultiplier == 0 {
		return fmt.Errorf("on-chain FROST committee scale multiplier must be positive")
	}
	if cfg.TransitionConfirmBlocks == 0 {
		return fmt.Errorf("on-chain FROST transition confirmation must be positive")
	}
	if cfg.CommitteeMaxSize != uint32(VaultCommitteeTiers[len(VaultCommitteeTiers)-1]) {
		return fmt.Errorf("on-chain FROST maximum committee size must be 100")
	}
	if cfg.TransitionTriggerRatio != float32(0.2) {
		return fmt.Errorf("on-chain FROST transition trigger ratio must be exactly 0.2")
	}
	if cfg.DkgCommitWindowBlocks == 0 || cfg.DkgSharingWindowBlocks == 0 || cfg.DkgDisputeWindowBlocks == 0 {
		return fmt.Errorf("on-chain FROST DKG windows must all be positive")
	}
	return nil
}

                                                                            
                                                                            
                                                                         
                          
func TargetVaultCommitteeSize(eligibleCount uint32, multiplier uint32, maxSize, currentSize int) (int, error) {
	if multiplier == 0 {
		return 0, fmt.Errorf("FROST committee scale multiplier must be positive")
	}
	if !IsVaultCommitteeSize(maxSize) {
		return 0, fmt.Errorf("unsupported FROST vault maximum committee size %d", maxSize)
	}
	if currentSize != 0 && !IsVaultCommitteeSize(currentSize) {
		return 0, fmt.Errorf("unsupported current FROST vault committee size %d", currentSize)
	}
	target := VaultCommitteeTiers[0]
	for _, tier := range VaultCommitteeTiers {
		if tier > maxSize {
			break
		}
		if uint64(eligibleCount) >= uint64(tier)*uint64(multiplier) {
			target = tier
		}
	}
	if currentSize > target {
		target = currentSize
	}
	return target, nil
}
