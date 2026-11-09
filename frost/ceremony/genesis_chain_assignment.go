package ceremony

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"

	"dex/config"
	"dex/utils"
)

const initialCommitteeChainRankDomain = "DEX_GENESIS_COMMITTEE_CHAIN_RANK_V1\x00"

type initialCommitteeCandidate struct {
	index   uint64
	address string
	rank    [sha256.Size]byte
}

                                                                              
                                                                             
                                                                           
                                                                     
func InitialCommitteeIndicesForChain(chain string, indices []uint64, addresses []string, committeeSize int) ([]uint64, error) {
	candidates, err := normalizeInitialCommitteeCandidates(indices, addresses)
	if err != nil {
		return nil, err
	}
	return rankInitialCommitteeCandidates(chain, candidates, committeeSize)
}

                                                                          
                                                                             
                                                                            
                                                                        
func InitialCommitteeAssignments(cfg config.FrostConfig, indices []uint64, addresses []string) (map[string][][]uint64, error) {
	candidates, err := normalizeInitialCommitteeCandidates(indices, addresses)
	if err != nil {
		return nil, err
	}
	if cfg.Committee.TopN < 0 {
		return nil, errors.New("committee.topN must not be negative")
	}
	if _, err := config.ComputeVaultCommitteeThreshold(cfg.Vault.DefaultK); err != nil {
		return nil, err
	}
	if len(cfg.GenesisKeyReuse) == 0 && cfg.Committee.TopN > 0 && len(candidates) > cfg.Committee.TopN {
		candidates = candidates[:cfg.Committee.TopN]
	}
	if len(candidates) < cfg.Vault.DefaultK {
		return nil, errors.New("insufficient initial committee candidates")
	}
	result := make(map[string][][]uint64, len(cfg.Chains))
	vaultCount := 0
	for _, chain := range genesisCeremonyChainOrder {
		chainCfg, ok := cfg.Chains[chain]
		if !ok {
			continue
		}
		if chainCfg.VaultsPerChain <= 0 || chainCfg.VaultsPerChain > MaxVaults-vaultCount {
			return nil, fmt.Errorf("invalid genesis vault count for %s", chain)
		}
		vaultCount += chainCfg.VaultsPerChain
		committees := make([][]uint64, chainCfg.VaultsPerChain)
		if len(cfg.GenesisKeyReuse) != 0 {
			for vaultID := range committees {
				committees[vaultID], err = InitialCommitteeIndices(cfg, indices, chain, uint32(vaultID))
				if err != nil {
					return nil, err
				}
			}
		} else {
			members, err := rankInitialCommitteeCandidates(chain, append([]initialCommitteeCandidate(nil), candidates...), cfg.Vault.DefaultK)
			if err != nil {
				return nil, err
			}
			for vaultID := range committees {
				committees[vaultID] = append([]uint64(nil), members...)
			}
		}
		result[chain] = committees
	}
	if len(result) == 0 || len(result) != len(cfg.Chains) {
		return nil, errors.New("unknown or missing genesis chain")
	}
	return result, nil
}

func normalizeInitialCommitteeCandidates(indices []uint64, addresses []string) ([]initialCommitteeCandidate, error) {
	if len(indices) == 0 || len(indices) != len(addresses) {
		return nil, errors.New("initial committee indices and addresses must form a complete roster")
	}
	candidates := make([]initialCommitteeCandidate, len(indices))
	seenIndices := make(map[uint64]struct{}, len(indices))
	seenAddresses := make(map[string]struct{}, len(addresses))
	for i, index := range indices {
		if !utils.IsDexAddress(addresses[i]) {
			return nil, errors.New("initial committee candidate address is not canonical")
		}
		if _, duplicate := seenIndices[index]; duplicate {
			return nil, errors.New("duplicate initial committee candidate index")
		}
		if _, duplicate := seenAddresses[addresses[i]]; duplicate {
			return nil, errors.New("duplicate initial committee candidate address")
		}
		seenIndices[index] = struct{}{}
		seenAddresses[addresses[i]] = struct{}{}
		candidates[i] = initialCommitteeCandidate{index: index, address: addresses[i]}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].address < candidates[j].address })
	return candidates, nil
}

func rankInitialCommitteeCandidates(chain string, candidates []initialCommitteeCandidate, committeeSize int) ([]uint64, error) {
	knownChain := false
	for _, name := range genesisCeremonyChainOrder {
		knownChain = knownChain || chain == name
	}
	if !knownChain {
		return nil, errors.New("unknown genesis chain")
	}
	if _, err := config.ComputeVaultCommitteeThreshold(committeeSize); err != nil {
		return nil, err
	}
	if len(candidates) < committeeSize {
		return nil, errors.New("insufficient initial committee candidates")
	}
	for i := range candidates {
		candidates[i].rank = sha256.Sum256([]byte(initialCommitteeChainRankDomain + chain + "\x00" + candidates[i].address))
	}
	sort.Slice(candidates, func(i, j int) bool { return initialCommitteeCandidateLess(candidates[i], candidates[j]) })
	members := make([]uint64, committeeSize)
	for i := range members {
		members[i] = candidates[i].index
	}
	return members, nil
}

func initialCommitteeCandidateLess(a, b initialCommitteeCandidate) bool {
	if order := bytes.Compare(a.rank[:], b.rank[:]); order != 0 {
		return order < 0
	}
	return a.address < b.address
}
