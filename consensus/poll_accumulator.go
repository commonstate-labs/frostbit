package consensus

import "fmt"

                                                                          
                                                                           
                                                       
type PollRecordResult struct {
	Accepted  bool
	Completed bool
	Weight    int
}

                                                                              
                                                                              
type PollOutcome struct {
	Votes          map[string]int
	RespondedSlots int
	Closed         bool
}

                                                                            
                                                                          
                                                 
type PollAccumulator[P comparable] struct {
	k               int
	alphaPreference int
	alphaConfidence int
	sampleWeights   map[P]int
	startCandidates map[string]struct{}
	responses       map[P]string
	votes           map[string]int
	respondedSlots  int
	closed          bool
}

                                                                            
                                                                             
                                               
func NewPollAccumulator[P comparable](
	k int,
	alphaPreference int,
	alphaConfidence int,
	sampleWeights map[P]int,
	startCandidates map[string]struct{},
) (*PollAccumulator[P], error) {
	if k <= 0 {
		return nil, fmt.Errorf("poll K must be positive")
	}
	if alphaPreference <= 0 || alphaPreference > alphaConfidence {
		return nil, fmt.Errorf("poll alpha preference must be in [1, alpha confidence]")
	}
	if alphaConfidence > k {
		return nil, fmt.Errorf("poll alpha confidence must be in [1, K]")
	}
	if len(sampleWeights) == 0 {
		return nil, fmt.Errorf("poll sample weights must not be empty")
	}

	weights := make(map[P]int, len(sampleWeights))
	totalSlots := 0
	for peer, weight := range sampleWeights {
		if weight <= 0 {
			return nil, fmt.Errorf("poll sample weight must be positive")
		}
		if weight > k-totalSlots {
			return nil, fmt.Errorf("poll sample weights exceed K")
		}
		weights[peer] = weight
		totalSlots += weight
	}
	if totalSlots != k {
		return nil, fmt.Errorf("poll sample weights total %d, want K=%d", totalSlots, k)
	}

	candidates := make(map[string]struct{}, len(startCandidates))
	for candidate := range startCandidates {
		candidates[candidate] = struct{}{}
	}

	return &PollAccumulator[P]{
		k:               k,
		alphaPreference: alphaPreference,
		alphaConfidence: alphaConfidence,
		sampleWeights:   weights,
		startCandidates: candidates,
		responses:       make(map[P]string, len(weights)),
		votes:           make(map[string]int),
	}, nil
}

                                                                            
                                                                            
         
func (p *PollAccumulator[P]) Record(peer P, preferredID string) PollRecordResult {
	if p == nil || p.closed {
		return PollRecordResult{}
	}
	weight, sampled := p.sampleWeights[peer]
	if !sampled {
		return PollRecordResult{}
	}
	if _, duplicate := p.responses[peer]; duplicate {
		return PollRecordResult{}
	}

	p.responses[peer] = preferredID
	p.respondedSlots += weight
	if preferredID != "" {
		p.votes[preferredID] += weight
	}

	_, wasCandidateAtStart := p.startCandidates[preferredID]
	hardWinner := preferredID != "" &&
		wasCandidateAtStart &&
		p.votes[preferredID] >= p.alphaConfidence
	remainingSlots := p.k - p.respondedSlots
	maxObservedVotes := 0
	for _, votes := range p.votes {
		if votes > maxObservedVotes {
			maxObservedVotes = votes
		}
	}
	                                                                        
	                                                                         
	                                                                    
	                                                                          
	           
	softOutcomeImpossible := maxObservedVotes+remainingSlots < p.alphaPreference
	if hardWinner || p.respondedSlots == p.k || softOutcomeImpossible {
		p.closed = true
	}
	return PollRecordResult{
		Accepted:  true,
		Completed: p.closed,
		Weight:    weight,
	}
}

                                                                          
func (p *PollAccumulator[P]) SampleWeight(peer P) (int, bool) {
	if p == nil {
		return 0, false
	}
	weight, ok := p.sampleWeights[peer]
	return weight, ok
}

                                                                           
                                                              
func (p *PollAccumulator[P]) Close() PollOutcome {
	if p == nil {
		return PollOutcome{Closed: true}
	}
	p.closed = true
	return p.Outcome()
}

                                                                              
                          
func (p *PollAccumulator[P]) Outcome() PollOutcome {
	if p == nil {
		return PollOutcome{}
	}
	return PollOutcome{
		Votes:          cloneStringIntMap(p.votes),
		RespondedSlots: p.respondedSlots,
		Closed:         p.closed,
	}
}
