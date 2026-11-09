                      
                                                                         
package witness

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"

	"github.com/shopspring/decimal"
)

                                            
type WitnessCandidate struct {
	Address     string                   
	StakeAmount decimal.Decimal                
	IsActive    bool                           
}

                              
type Selector struct {
	config *Config
}

                                    
func NewSelector(config *Config) *Selector {
	if config == nil {
		config = DefaultConfig()
	}
	return &Selector{config: config}
}

                                  
          
                                                           
                                       
                                                    
                                                                               
                                                                                                                 
  
                                         
func (s *Selector) SelectWitnesses(
	requestID string,
	round uint32,
	candidates []*WitnessCandidate,
	excludeAddresses map[string]bool,
	beacon []byte,
) ([]string, error) {
	                                          
	activeCandidates := make([]*WitnessCandidate, 0)
	for _, c := range candidates {
		if c.IsActive && !excludeAddresses[c.Address] {
			activeCandidates = append(activeCandidates, c)
		}
	}

	if len(activeCandidates) == 0 {
		return nil, fmt.Errorf("no active candidates available")
	}

	                                             
	count := s.calculateWitnessCount(round)
	if uint32(len(activeCandidates)) < count {
		count = uint32(len(activeCandidates))
	}

	                           
	selected := s.weightedRandomSelect(requestID, round, activeCandidates, count, beacon)

	return selected, nil
}

                                                             
func (s *Selector) calculateWitnessCount(round uint32) uint32 {
	count := s.config.InitialWitnessCount
	for i := uint32(0); i < round; i++ {
		count *= s.config.ExpandMultiplier
	}
	return count
}

                                          
                                                         
func (s *Selector) weightedRandomSelect(
	requestID string,
	round uint32,
	candidates []*WitnessCandidate,
	count uint32,
	beacon []byte,
) []string {
	                                                               
	type weightedCandidate struct {
		address string
		weight  decimal.Decimal
		score   uint64                         
	}

	totalStake := decimal.Zero
	for _, c := range candidates {
		totalStake = totalStake.Add(c.StakeAmount)
	}

	                                           
	if totalStake.IsZero() {
		totalStake = decimal.NewFromInt(int64(len(candidates)))
		for i := range candidates {
			candidates[i].StakeAmount = decimal.NewFromInt(1)
		}
	}

	                                          
	weighted := make([]weightedCandidate, len(candidates))
	for i, c := range candidates {
		                              
		score := s.calculateScore(requestID, round, c.Address, beacon)
		                                 
		weight := c.StakeAmount.Div(totalStake)
		weighted[i] = weightedCandidate{
			address: c.Address,
			weight:  weight,
			score:   score,
		}
	}

	                                                      
	sort.Slice(weighted, func(i, j int) bool {
		                                             
		                                             
		scoreI := float64(weighted[i].score) * weighted[i].weight.InexactFloat64()
		scoreJ := float64(weighted[j].score) * weighted[j].weight.InexactFloat64()
		if scoreI == scoreJ {
			return weighted[i].address < weighted[j].address
		}
		return scoreI > scoreJ
	})

	                      
	selected := make([]string, 0, count)
	for i := uint32(0); i < count && i < uint32(len(weighted)); i++ {
		selected = append(selected, weighted[i].address)
	}

	return selected
}

                                       
                                                                                      
                                                                                           
                                                                                                             
func (s *Selector) calculateScore(requestID string, round uint32, address string, beacon []byte) uint64 {
	hasher := sha256.New()
	hasher.Write([]byte(requestID))

	roundBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(roundBytes, round)
	hasher.Write(roundBytes)

	hasher.Write([]byte(address))
	if len(beacon) > 0 {
		hasher.Write(beacon)
	}

	hash := hasher.Sum(nil)
	return binary.BigEndian.Uint64(hash[:8])
}

                                    
                                                                        
                                                                                                           
func (s *Selector) SelectArbitrators(
	challengeID string,
	round uint32,
	candidates []*WitnessCandidate,
	originalWitnesses []string,
	beacon []byte,
) ([]string, error) {
	                     
	excludeAddresses := make(map[string]bool)
	for _, addr := range originalWitnesses {
		excludeAddresses[addr] = true
	}

	                                 
	count := s.calculateArbitratorCount(round)

	                                          
	activeCandidates := make([]*WitnessCandidate, 0)
	for _, c := range candidates {
		if c.IsActive && !excludeAddresses[c.Address] {
			activeCandidates = append(activeCandidates, c)
		}
	}

	if len(activeCandidates) == 0 {
		return nil, fmt.Errorf("no arbitrators available")
	}

	if uint32(len(activeCandidates)) < count {
		count = uint32(len(activeCandidates))
	}

	                           
	selected := s.weightedRandomSelect(challengeID, round, activeCandidates, count, beacon)

	return selected, nil
}

                                                 
func (s *Selector) calculateArbitratorCount(round uint32) uint32 {
	                                                        
	count := s.config.InitialWitnessCount * 2
	for i := uint32(0); i < round; i++ {
		count *= s.config.ExpandMultiplier
	}
	return count
}

                                                           
                                                   
func (s *Selector) VerifyWitnessSelection(
	requestID string,
	round uint32,
	candidates []*WitnessCandidate,
	selectedWitnesses []string,
	beacon []byte,
) bool {
	                           
	expected, err := s.SelectWitnesses(requestID, round, candidates, nil, beacon)
	if err != nil {
		return false
	}

	               
	if len(expected) != len(selectedWitnesses) {
		return false
	}

	expectedSet := make(map[string]bool)
	for _, addr := range expected {
		expectedSet[addr] = true
	}

	for _, addr := range selectedWitnesses {
		if !expectedSet[addr] {
			return false
		}
	}

	return true
}

                                                               
func (s *Selector) GetWitnessCountForRound(round uint32) uint32 {
	return s.calculateWitnessCount(round)
}
