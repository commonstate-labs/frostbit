package committee

import (
	"dex/config"
	"dex/frost/runtime"
	"dex/keys"
	"dex/pb"
	"fmt"
	"math"
	"sort"
	"strings"

	"google.golang.org/protobuf/proto"
)

                                                                        
type DefaultVaultCommitteeProvider struct {
	stateReader runtime.ChainStateReader
	cfg         VaultCommitteeProviderConfig
	assigner    *VaultCommitteeAssigner
}

                                         
type VaultCommitteeProviderConfig struct {
	DefaultVaultCount     int
	DefaultCommitteeSize  int
	DefaultThresholdRatio float64
}

                                                            
func DefaultVaultCommitteeProviderConfig() VaultCommitteeProviderConfig {
	return VaultCommitteeProviderConfig{
		DefaultVaultCount:     1,
		DefaultCommitteeSize:  4,
		DefaultThresholdRatio: 0.75,
	}
}

                                                                    
func NewDefaultVaultCommitteeProvider(
	stateReader runtime.ChainStateReader,
	cfg VaultCommitteeProviderConfig,
) *DefaultVaultCommitteeProvider {
	return &DefaultVaultCommitteeProvider{
		stateReader: stateReader,
		cfg:         cfg,
		assigner:    NewVaultCommitteeAssigner(),
	}
}

                                                          
func (p *DefaultVaultCommitteeProvider) VaultCommittee(chain string, vaultID uint32, epoch uint64) ([]runtime.SignerInfo, error) {
	topK, err := p.GetTopK()
	if err != nil {
		return nil, fmt.Errorf("failed to get topK: %w", err)
	}

	                                                                                                   
	if signers, found, err := p.committeeFromSnapshot(chain, vaultID, epoch, topK); err != nil {
		return nil, err
	} else if found {
		return signers, nil
	}
	return nil, fmt.Errorf("frozen vault committee snapshot not found for %s/%d epoch %d", chain, vaultID, epoch)
}

func (p *DefaultVaultCommitteeProvider) committeeFromSnapshot(chain string, vaultID uint32, epoch uint64, topK *pb.FrostActiveMiners) ([]runtime.SignerInfo, bool, error) {
	members, found, err := p.snapshotCommitteeMembers(chain, vaultID, epoch)
	if err != nil {
		return nil, false, err
	}
	if !found {
		return nil, false, nil
	}
	signers, err := p.addressesToSignerInfo(members, topK)
	if err != nil {
		return nil, false, err
	}
	return signers, true, nil
}

func (p *DefaultVaultCommitteeProvider) snapshotCommitteeMembers(chain string, vaultID uint32, epoch uint64) ([]string, bool, error) {
	transitionKey := keys.KeyFrostVaultTransition(chain, vaultID, epoch)
	transitionData, exists, err := p.stateReader.Get(transitionKey)
	if err != nil {
		return nil, false, fmt.Errorf("failed to read vault transition %s/%d/%d: %w", chain, vaultID, epoch, err)
	}
	if exists {
		var ts pb.VaultTransitionState
		if err := proto.Unmarshal(transitionData, &ts); err != nil {
			return nil, false, fmt.Errorf("failed to unmarshal vault transition %s/%d/%d: %w", chain, vaultID, epoch, err)
		}
		if ts.Chain != chain || ts.VaultId != vaultID || ts.EpochId != epoch {
			return nil, false, fmt.Errorf("vault transition identity mismatch for %s/%d/%d", chain, vaultID, epoch)
		}
		if len(ts.NewCommitteeMembers) > 0 {
			expected, thresholdErr := config.ComputeVaultCommitteeThreshold(len(ts.NewCommitteeMembers))
			if thresholdErr != nil || ts.DkgN != uint32(len(ts.NewCommitteeMembers)) || ts.DkgThresholdT != uint32(expected) {
				return nil, false, fmt.Errorf("invalid frozen transition committee for %s/%d/%d: n=%d members=%d t=%d", chain, vaultID, epoch, ts.DkgN, len(ts.NewCommitteeMembers), ts.DkgThresholdT)
			}
			return append([]string(nil), ts.NewCommitteeMembers...), true, nil
		}
		return nil, false, fmt.Errorf("frozen transition committee is empty for %s/%d/%d", chain, vaultID, epoch)
	}

	stateKey := keys.KeyFrostVaultState(chain, vaultID)
	stateData, exists, err := p.stateReader.Get(stateKey)
	if err != nil {
		return nil, false, fmt.Errorf("failed to read vault state %s/%d: %w", chain, vaultID, err)
	}
	if !exists {
		return nil, false, nil
	}

	var state pb.FrostVaultState
	if err := proto.Unmarshal(stateData, &state); err != nil {
		return nil, false, fmt.Errorf("failed to unmarshal vault state %s/%d: %w", chain, vaultID, err)
	}
	if state.Chain != chain || state.VaultId != vaultID {
		return nil, false, fmt.Errorf("vault state identity mismatch for %s/%d", chain, vaultID)
	}
	if state.KeyEpoch == epoch && len(state.CommitteeMembers) > 0 {
		expected, thresholdErr := config.ComputeVaultCommitteeThreshold(len(state.CommitteeMembers))
		if thresholdErr != nil || state.CommitteeThreshold != uint32(expected) {
			return nil, false, fmt.Errorf("invalid frozen vault committee for %s/%d epoch %d: members=%d t=%d", chain, vaultID, epoch, len(state.CommitteeMembers), state.CommitteeThreshold)
		}
		return append([]string(nil), state.CommitteeMembers...), true, nil
	}
	return nil, false, nil
}

func (p *DefaultVaultCommitteeProvider) addressesToSignerInfo(members []string, topK *pb.FrostActiveMiners) ([]runtime.SignerInfo, error) {
	byAddress := make(map[string]runtime.SignerInfo, len(topK.Addresses))
	for i, addr := range topK.Addresses {
		if addr == "" {
			continue
		}
		signer := runtime.SignerInfo{ID: runtime.NodeID(addr)}
		if i < len(topK.Indices) {
			signer.Index = uint32(topK.Indices[i])
		}
		if i < len(topK.PublicKeys) && len(topK.PublicKeys[i]) > 0 {
			signer.PublicKey = append([]byte(nil), topK.PublicKeys[i]...)
		}
		byAddress[addr] = signer
	}

	signers := make([]runtime.SignerInfo, 0, len(members))
	seenAddresses := make(map[string]struct{}, len(members))
	seenIndices := make(map[uint32]struct{}, len(members))
	for _, addr := range members {
		if strings.TrimSpace(addr) == "" {
			return nil, fmt.Errorf("frozen committee contains an empty member")
		}
		if _, duplicate := seenAddresses[addr]; duplicate {
			return nil, fmt.Errorf("frozen committee contains duplicate member %s", addr)
		}
		seenAddresses[addr] = struct{}{}
		var signer runtime.SignerInfo
		if snapshotSigner, ok := byAddress[addr]; ok {
			                                                               
			signer = snapshotSigner
		} else {
			signer = runtime.SignerInfo{ID: runtime.NodeID(addr)}
			acct, exists := p.getAccount(addr)
			if !exists || acct.Index > math.MaxUint32 {
				return nil, fmt.Errorf("frozen committee member identity not found for %s", addr)
			}
			signer.Index = uint32(acct.Index)
			if acct.PublicKeys != nil {
				signer.PublicKey = pickDeterministicPubKeyFromMap(acct.PublicKeys.Keys)
			}
		}
		if signer.ID != runtime.NodeID(addr) || len(signer.PublicKey) == 0 {
			return nil, fmt.Errorf("frozen committee member public key not found for %s", addr)
		}
		if _, duplicate := seenIndices[signer.Index]; duplicate {
			return nil, fmt.Errorf("frozen committee contains duplicate miner index %d", signer.Index)
		}
		seenIndices[signer.Index] = struct{}{}
		signers = append(signers, signer)
	}
	return signers, nil
}

func (p *DefaultVaultCommitteeProvider) getAccount(address string) (*pb.Account, bool) {
	data, exists, err := p.stateReader.Get(keys.KeyAccount(address))
	if err != nil || !exists {
		return nil, false
	}
	var acct pb.Account
	if err := proto.Unmarshal(data, &acct); err != nil {
		return nil, false
	}
	return &acct, true
}

func pickDeterministicPubKeyFromMap(keysByAlgo map[int32][]byte) []byte {
	if len(keysByAlgo) == 0 {
		return nil
	}
	if pk, ok := keysByAlgo[int32(pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1_DEX)]; ok && len(pk) > 0 {
		return append([]byte(nil), pk...)
	}
	algoIDs := make([]int, 0, len(keysByAlgo))
	for algo, pk := range keysByAlgo {
		if len(pk) > 0 {
			algoIDs = append(algoIDs, int(algo))
		}
	}
	if len(algoIDs) == 0 {
		return nil
	}
	sort.Ints(algoIDs)
	pk := keysByAlgo[int32(algoIDs[0])]
	if len(pk) == 0 {
		return nil
	}
	return append([]byte(nil), pk...)
}

                                                          
func (p *DefaultVaultCommitteeProvider) VaultCurrentEpoch(chain string, vaultID uint32) uint64 {
	vaultState, err := p.GetVaultState(chain, vaultID)
	if err != nil || vaultState == nil {
		return 1
	}
	return vaultState.KeyEpoch
}

                                                          
func (p *DefaultVaultCommitteeProvider) VaultGroupPubkey(chain string, vaultID uint32, epoch uint64) ([]byte, error) {
	vaultState, err := p.GetVaultState(chain, vaultID)
	if err != nil {
		return nil, fmt.Errorf("failed to get vault state: %w", err)
	}

	if vaultState.KeyEpoch != epoch {
		return nil, fmt.Errorf("epoch mismatch: expected %d, got %d", epoch, vaultState.KeyEpoch)
	}

	return vaultState.GroupPubkey, nil
}

                                               
func (p *DefaultVaultCommitteeProvider) GetTopK() (*pb.FrostActiveMiners, error) {
	key := keys.KeyFrostActiveMiners()
	data, exists, err := p.stateReader.Get(key)
	if err != nil {
		return nil, fmt.Errorf("failed to read topK: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("topK not found")
	}

	var topK pb.FrostActiveMiners
	if err := proto.Unmarshal(data, &topK); err != nil {
		return nil, fmt.Errorf("failed to unmarshal topK: %w", err)
	}
	if err := config.ValidateVaultCandidateSnapshot(&topK); err != nil {
		return nil, fmt.Errorf("invalid topK snapshot: %w", err)
	}

	return &topK, nil
}

                                                         
func (p *DefaultVaultCommitteeProvider) GetVaultConfig(chain string) (*pb.FrostVaultConfig, error) {
	key := keys.KeyFrostVaultConfig(chain, 0)
	data, exists, err := p.stateReader.Get(key)
	if err != nil {
		return nil, fmt.Errorf("failed to read vault config: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("vault config not found for chain %s", chain)
	}

	var cfg pb.FrostVaultConfig
	if err := proto.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal vault config: %w", err)
	}

	return &cfg, nil
}

                                              
func (p *DefaultVaultCommitteeProvider) GetVaultState(chain string, vaultID uint32) (*pb.FrostVaultState, error) {
	key := keys.KeyFrostVaultState(chain, vaultID)
	data, exists, err := p.stateReader.Get(key)
	if err != nil {
		return nil, fmt.Errorf("failed to read vault state: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("vault state not found for %s/%d", chain, vaultID)
	}

	var state pb.FrostVaultState
	if err := proto.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to unmarshal vault state: %w", err)
	}

	return &state, nil
}

                                                           
func (p *DefaultVaultCommitteeProvider) indexToSignerInfo(topK *pb.FrostActiveMiners, idx uint64) runtime.SignerInfo {
	for i, minerIdx := range topK.Indices {
		if minerIdx == idx {
			signer := runtime.SignerInfo{Index: uint32(idx)}
			if i < len(topK.Addresses) {
				signer.ID = runtime.NodeID(topK.Addresses[i])
			}
			if i < len(topK.PublicKeys) {
				signer.PublicKey = topK.PublicKeys[i]
			}
			return signer
		}
	}
	return runtime.SignerInfo{Index: uint32(idx)}
}

                                        
func (p *DefaultVaultCommitteeProvider) CalculateThreshold(chain string, vaultID uint32) (int, error) {
	vaultState, err := p.GetVaultState(chain, vaultID)
	if err != nil {
		return 0, fmt.Errorf("failed to get vault state: %w", err)
	}
	expected, err := config.ComputeVaultCommitteeThreshold(len(vaultState.CommitteeMembers))
	if err != nil {
		return 0, fmt.Errorf("invalid frozen committee for %s/%d: %w", chain, vaultID, err)
	}
	if int(vaultState.CommitteeThreshold) != expected {
		return 0, fmt.Errorf(
			"invalid frozen threshold for %s/%d: got %d, want %d for committee size %d",
			chain, vaultID, vaultState.CommitteeThreshold, expected, len(vaultState.CommitteeMembers),
		)
	}
	return int(vaultState.CommitteeThreshold), nil
}

var _ runtime.VaultCommitteeProvider = (*DefaultVaultCommitteeProvider)(nil)
