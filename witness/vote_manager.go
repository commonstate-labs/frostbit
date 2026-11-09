                          
                                                      
                                                                                                                 
package witness

import (
	"dex/pb"
	"sort"
	"sync"
)

                              
type VoteManager struct {
	mu     sync.RWMutex
	config *Config

	                           
	votes map[string]map[string]*pb.WitnessVote                                    

	                                 
	selectedWitnesses map[string]map[string]bool                                    

	               
	eventChan chan *Event
}

                                       
func NewVoteManager(config *Config) *VoteManager {
	if config == nil {
		config = DefaultConfig()
	}
	return &VoteManager{
		config:            config,
		votes:             make(map[string]map[string]*pb.WitnessVote),
		selectedWitnesses: make(map[string]map[string]bool),
		eventChan:         make(chan *Event, 100),
	}
}

                                                                           
                                                                            
func IsValidVoteType(voteType pb.WitnessVoteType) bool {
	switch voteType {
	case pb.WitnessVoteType_VOTE_PASS,
		pb.WitnessVoteType_VOTE_FAIL,
		pb.WitnessVoteType_VOTE_ABSTAIN:
		return true
	default:
		return false
	}
}

                           
func (vm *VoteManager) Reset() {
	vm.mu.Lock()
	defer vm.mu.Unlock()
	vm.votes = make(map[string]map[string]*pb.WitnessVote)
	vm.selectedWitnesses = make(map[string]map[string]bool)
}

                                                      
func (vm *VoteManager) SetSelectedWitnesses(requestID string, witnesses []string) {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	witnessSet := make(map[string]bool)
	for _, w := range witnesses {
		witnessSet[w] = true
	}
	vm.selectedWitnesses[requestID] = witnessSet

	                        
	if vm.votes[requestID] == nil {
		vm.votes[requestID] = make(map[string]*pb.WitnessVote)
	}
}

                                                      
func (vm *VoteManager) IsSelectedWitness(requestID, witnessAddr string) bool {
	vm.mu.RLock()
	defer vm.mu.RUnlock()

	return vm.selectedWitnesses[requestID][witnessAddr]
}

                       
func (vm *VoteManager) AddVote(vote *pb.WitnessVote) error {
	if vote == nil || !IsValidVoteType(vote.VoteType) {
		return ErrInvalidVote
	}
	vm.mu.Lock()
	defer vm.mu.Unlock()

	requestID := vote.RequestId
	witnessAddr := vote.WitnessAddress

	                                    
	if !vm.selectedWitnesses[requestID][witnessAddr] {
		return ErrNotSelectedWitness
	}

	                           
	if vm.votes[requestID] == nil {
		vm.votes[requestID] = make(map[string]*pb.WitnessVote)
	}
	if _, exists := vm.votes[requestID][witnessAddr]; exists {
		return ErrDuplicateVote
	}

	               
	vm.votes[requestID][witnessAddr] = vote

	return nil
}

                                  
func (vm *VoteManager) GetVoteStats(requestID string) *VoteStats {
	vm.mu.RLock()
	defer vm.mu.RUnlock()

	votes := vm.votes[requestID]
	witnesses := vm.selectedWitnesses[requestID]

	if len(witnesses) == 0 {
		return &VoteStats{}
	}

	var pass, fail, abstain uint32
	for _, vote := range votes {
		switch vote.VoteType {
		case pb.WitnessVoteType_VOTE_PASS:
			pass++
		case pb.WitnessVoteType_VOTE_FAIL:
			fail++
		case pb.WitnessVoteType_VOTE_ABSTAIN:
			abstain++
		}
	}

	total := uint32(len(witnesses))
	return CalculateVoteStats(pass, fail, abstain, total)
}

                                    
func (vm *VoteManager) CheckConsensus(requestID string, isDeadline bool) ConsensusResult {
	stats := vm.GetVoteStats(requestID)
	return DetermineConsensus(stats, vm.config.ConsensusThreshold, isDeadline)
}

                              
func (vm *VoteManager) GetVotes(requestID string) []*pb.WitnessVote {
	vm.mu.RLock()
	defer vm.mu.RUnlock()

	votes := vm.votes[requestID]
	keys := make([]string, 0, len(votes))
	for addr := range votes {
		keys = append(keys, addr)
	}
	sort.Strings(keys)
	result := make([]*pb.WitnessVote, 0, len(keys))
	for _, addr := range keys {
		result = append(result, votes[addr])
	}
	return result
}

                                     
func (vm *VoteManager) GetPassVotes(requestID string) []*pb.WitnessVote {
	vm.mu.RLock()
	defer vm.mu.RUnlock()

	votes := vm.votes[requestID]
	keys := make([]string, 0, len(votes))
	for addr := range votes {
		keys = append(keys, addr)
	}
	sort.Strings(keys)
	result := make([]*pb.WitnessVote, 0)
	for _, addr := range keys {
		v := votes[addr]
		if v.VoteType == pb.WitnessVoteType_VOTE_PASS {
			result = append(result, v)
		}
	}
	return result
}

                                        
func (vm *VoteManager) ClearRequest(requestID string) {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	delete(vm.votes, requestID)
	delete(vm.selectedWitnesses, requestID)
}

                                        
func (vm *VoteManager) ExpandWitnesses(requestID string, newWitnesses []string) {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	if vm.selectedWitnesses[requestID] == nil {
		vm.selectedWitnesses[requestID] = make(map[string]bool)
	}

	for _, w := range newWitnesses {
		vm.selectedWitnesses[requestID][w] = true
	}

	                                                   
	vm.votes[requestID] = make(map[string]*pb.WitnessVote)
}

                                                      
func (vm *VoteManager) GetSelectedWitnesses(requestID string) []string {
	vm.mu.RLock()
	defer vm.mu.RUnlock()

	witnesses := vm.selectedWitnesses[requestID]
	result := make([]string, 0, len(witnesses))
	for addr := range witnesses {
		result = append(result, addr)
	}
	sort.Strings(result)
	return result
}

                                          
func (vm *VoteManager) HasVoted(requestID, witnessAddr string) bool {
	vm.mu.RLock()
	defer vm.mu.RUnlock()

	if vm.votes[requestID] == nil {
		return false
	}
	_, exists := vm.votes[requestID][witnessAddr]
	return exists
}

                                     
func (vm *VoteManager) GetVoteCount(requestID string) int {
	vm.mu.RLock()
	defer vm.mu.RUnlock()

	return len(vm.votes[requestID])
}

                                                     
func (vm *VoteManager) IsAllVoted(requestID string) bool {
	vm.mu.RLock()
	defer vm.mu.RUnlock()

	witnesses := vm.selectedWitnesses[requestID]
	votes := vm.votes[requestID]

	return len(votes) >= len(witnesses)
}

                                                         
                                                         
func (vm *VoteManager) CanReachConsensus(requestID string) bool {
	stats := vm.GetVoteStats(requestID)

	                                                                         
	maxAbstainRatio := float64(100 - vm.config.ConsensusThreshold)
	return stats.AbstainRatio <= maxAbstainRatio
}

                            
func (vm *VoteManager) Events() <-chan *Event {
	return vm.eventChan
}

                         
func (vm *VoteManager) emitEvent(event *Event) {
	select {
	case vm.eventChan <- event:
	default:
		                              
	}
}
