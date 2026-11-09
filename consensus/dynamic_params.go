package consensus

import (
	"dex/interfaces"
	"sync"
)

                                                                       
                                                                              
                                    
type ConsensusPollParams struct {
	K               int
	AlphaPreference int
	AlphaConfidence int
	Beta            int
}

func pollParamsFromConfig(cfg ConsensusConfig) ConsensusPollParams {
	return ConsensusPollParams{
		K:               cfg.K,
		AlphaPreference: cfg.AlphaPreference,
		AlphaConfidence: cfg.AlphaConfidence,
		Beta:            cfg.Beta,
	}
}

                                                                              
                                                                           
                                                                              
                                  
  
                                                                             
                                                                              
                                                                               
                                               
func ResolveDynamicConsensusParams(base ConsensusConfig, voterCount int) ConsensusConfig {
	if voterCount < 2 || base.K < 1 {
		return base
	}

	k := base.K
	if peerCount := voterCount - 1; k > peerCount {
		k = peerCount
	}
	if growthCap := (3*voterCount + 2) / 4; k > growthCap {
		k = growthCap
	}
	if k < 1 {
		return base
	}

	                                                                   
	                                                                  
	if k == base.K {
		return base
	}

	base.K = k
	base.AlphaPreference = k/2 + 1
	base.AlphaConfidence = (4*k + 2) / 5
	if base.AlphaConfidence < base.AlphaPreference {
		base.AlphaConfidence = base.AlphaPreference
	}
	if base.AlphaConfidence > k {
		base.AlphaConfidence = k
	}
	return base
}

                                                              
                                                                               
                                                                      
type consensusVoterSnapshotSource interface {
	GetConsensusVoterSnapshot() (height uint64, count int)
}

                                                                          
                                                                            
                                                                              
                                          
type ConsensusParamsProvider struct {
	mu      sync.RWMutex
	base    ConsensusConfig
	tracker interfaces.LivenessTracker
}

func NewConsensusParamsProvider(base ConsensusConfig) *ConsensusParamsProvider {
	return &ConsensusParamsProvider{base: base}
}

func (p *ConsensusParamsProvider) SetLivenessTracker(tracker interfaces.LivenessTracker) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.tracker = tracker
	p.mu.Unlock()
}

                                                                            
                                                                             
                                                                           
                                                     
func (p *ConsensusParamsProvider) ParamsForAnchor(anchorHeight uint64) (params ConsensusPollParams, voterCount int, ok bool) {
	if p == nil {
		return ConsensusPollParams{}, 0, false
	}
	p.mu.RLock()
	base := p.base
	tracker := p.tracker
	p.mu.RUnlock()

	source, dynamic := tracker.(consensusVoterSnapshotSource)
	if !dynamic || source == nil {
		return pollParamsFromConfig(base), 0, true
	}
	height, count := source.GetConsensusVoterSnapshot()
	if height != anchorHeight {
		return ConsensusPollParams{}, count, false
	}
	resolved := ResolveDynamicConsensusParams(base, count)
	return pollParamsFromConfig(resolved), count, true
}
