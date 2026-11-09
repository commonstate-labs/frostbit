package consensus

import (
	"dex/interfaces"
	"dex/types"
	"sort"
	"sync"
	"time"
)

                                                                          
                                                                       
                                     
type SuccessRound struct {
	Round     int
	Timestamp int64
	SeqID     uint32
	VRFSeed   []byte
	Votes     []types.FinalizationChit
}

                                                                             
                                                                               
                                                                   
type Snowball struct {
	mu             sync.RWMutex
	state          PreferenceState
	events         interfaces.EventBus
	lastVotes      map[string]int
	successHistory []SuccessRound
}

func NewSnowball(events interfaces.EventBus) *Snowball {
	return &Snowball{
		events:         events,
		state:          NewPreferenceState(),
		lastVotes:      make(map[string]int),
		successHistory: make([]SuccessRound, 0),
	}
}

                                                                              
                                                                         
func (sb *Snowball) AddCandidate(blockID string) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	sb.state = AddSnowballCandidate(sb.state, blockID)
}

                                                                           
                                 
func (sb *Snowball) AddCandidates(blockIDs []string) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	for _, blockID := range blockIDs {
		sb.state = AddSnowballCandidate(sb.state, blockID)
	}
}

                                                                        
func (sb *Snowball) Candidates() []string {
	sb.mu.RLock()
	defer sb.mu.RUnlock()
	result := make([]string, 0, len(sb.state.Strength))
	for blockID := range sb.state.Strength {
		result = append(result, blockID)
	}
	sort.Strings(result)
	return result
}

                                                           
  
                                                                               
                                                                               
                                                                       
func (sb *Snowball) RecordVoteWithDetails(
	candidates []string,
	votes map[string]int,
	alphaPreference int,
	alphaConfidence int,
	seqID uint32,
	vrfSeed []byte,
	voteDetails []types.FinalizationChit,
) {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	if sb.state.Finalized {
		return
	}
	for _, blockID := range candidates {
		sb.state = AddSnowballCandidate(sb.state, blockID)
	}

	sb.lastVotes = cloneStringIntMap(votes)
	next, transition := AdvancePreferenceState(sb.state, PreferencePollInput{
		Mode:            PreferenceModeSnowball,
		Candidates:      candidates,
		Votes:           votes,
		AlphaPreference: alphaPreference,
		AlphaConfidence: alphaConfidence,
	})
	sb.state = next

	if transition.ResetEvidence {
		sb.successHistory = nil
	}
	if transition.StrongPoll {
		sb.successHistory = append(sb.successHistory, SuccessRound{
			Round:     sb.state.Confidence,
			Timestamp: time.Now().UnixMilli(),
			SeqID:     seqID,
			VRFSeed:   append([]byte(nil), vrfSeed...),
			Votes:     cloneFinalizationChits(voteDetails),
		})
	}
	if transition.EmitPreferenceEvent && sb.events != nil {
		sb.events.PublishAsync(types.BaseEvent{
			EventType: types.EventPreferenceChanged,
			EventData: PreferenceSwitch{
				BlockID:         transition.PreviousPreference,
				Confidence:      transition.PreviousConfidence,
				Winner:          transition.Preference,
				AlphaPreference: alphaPreference,
				AlphaConfidence: alphaConfidence,
			},
		})
	}
}

func (sb *Snowball) RecordVote(candidates []string, votes map[string]int, alphaPreference, alphaConfidence int) {
	sb.RecordVoteWithDetails(candidates, votes, alphaPreference, alphaConfidence, 0, nil, nil)
}

func (sb *Snowball) GetPreference() string {
	sb.mu.RLock()
	defer sb.mu.RUnlock()
	return sb.state.Preference
}

func (sb *Snowball) GetConfidencePreference() string {
	sb.mu.RLock()
	defer sb.mu.RUnlock()
	return sb.state.ConfidencePreference
}

func (sb *Snowball) GetConfidence() int {
	sb.mu.RLock()
	defer sb.mu.RUnlock()
	return sb.state.Confidence
}

func (sb *Snowball) GetStrength(blockID string) int {
	sb.mu.RLock()
	defer sb.mu.RUnlock()
	return sb.state.Strength[blockID]
}

                                                                           
                                        
func (sb *Snowball) FinalizationPreference(beta int) (string, bool) {
	sb.mu.RLock()
	defer sb.mu.RUnlock()
	if !CanFinalizePreferenceState(sb.state, beta) {
		return "", false
	}
	return preferenceToFinalize(sb.state), true
}

                                                                             
                                                                             
func (sb *Snowball) Finalize(blockID string) bool {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	if blockID == "" || preferenceToFinalize(sb.state) != blockID {
		return false
	}
	if sb.state.Finalized {
		return sb.state.Preference == blockID
	}
	sb.state = FinalizePreferenceState(sb.state)
	return sb.state.Finalized && sb.state.Preference == blockID
}

                                                                               
                                                                              
                                                          
func (sb *Snowball) InvalidateCandidate(blockID string) bool {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	previousPreference := sb.state.Preference
	previousConfidencePreference := sb.state.ConfidencePreference
	previousConfidence := sb.state.Confidence
	next, invalidated := InvalidatePreferenceCandidate(sb.state, blockID)
	if !invalidated {
		return false
	}
	sb.state = next
	if sb.lastVotes != nil {
		delete(sb.lastVotes, blockID)
	}
	if previousPreference == blockID ||
		previousConfidencePreference == blockID ||
		(previousConfidence > 0 && sb.state.Confidence == 0) {
		sb.lastVotes = make(map[string]int)
		sb.successHistory = nil
	}
	return true
}

func (sb *Snowball) GetLastVotes() map[string]int {
	sb.mu.RLock()
	defer sb.mu.RUnlock()
	return cloneStringIntMap(sb.lastVotes)
}

                                                                              
                                               
func (sb *Snowball) DropFinalizedEvidence() {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	if !sb.state.Finalized {
		return
	}
	sb.lastVotes = nil
	sb.successHistory = nil
}

func (sb *Snowball) IsFinalized() bool {
	sb.mu.RLock()
	defer sb.mu.RUnlock()
	return sb.state.Finalized
}

func (sb *Snowball) GetSuccessHistory() []SuccessRound {
	sb.mu.RLock()
	defer sb.mu.RUnlock()
	result := make([]SuccessRound, len(sb.successHistory))
	for i, round := range sb.successHistory {
		result[i] = SuccessRound{
			Round:     round.Round,
			Timestamp: round.Timestamp,
			SeqID:     round.SeqID,
			VRFSeed:   append([]byte(nil), round.VRFSeed...),
			Votes:     cloneFinalizationChits(round.Votes),
		}
	}
	return result
}

func cloneFinalizationChits(input []types.FinalizationChit) []types.FinalizationChit {
	if len(input) == 0 {
		return nil
	}
	result := make([]types.FinalizationChit, len(input))
	copy(result, input)
	for i := range result {
		result[i].Signature = append([]byte(nil), input[i].Signature...)
	}
	return result
}
