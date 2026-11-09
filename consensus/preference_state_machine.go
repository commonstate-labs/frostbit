package consensus

import (
	"strconv"
	"strings"
)

                                                                       
type PreferenceMode string

const (
	                                                                     
	                                                                         
	PreferenceModeSnowflake PreferenceMode = "snowflake"

	                                                                          
	                                                                         
	                                                                  
	                                                               
	PreferenceModeSnowball PreferenceMode = "snowball"
)

                                                                          
                                                                     
                                                          
const LegacyFailureSwitchAfter = 45

                                                                            
type PreferenceChangeReason string

const (
	PreferenceChangeNone            PreferenceChangeReason = ""
	PreferenceChangeStrongPoll      PreferenceChangeReason = "strong-poll"
	PreferenceChangeInitialFallback PreferenceChangeReason = "initial-fallback"
	PreferenceChangeFailureEscape   PreferenceChangeReason = "failure-escape"
	PreferenceChangeCumulative      PreferenceChangeReason = "cumulative-strength"
	PreferenceChangePersistedChit   PreferenceChangeReason = "persisted-chit"
)

                                                                              
                                                                             
type PreferenceState struct {
	                                                                      
	                                             
	Preference string
	                                                                           
	                                                                  
	                                                                          
	ConfidencePreference string
	Confidence           int
	ConsecutiveFailures  int
	Finalized            bool
	Strength             map[string]int
}

                                                      
func NewPreferenceState() PreferenceState {
	return PreferenceState{}
}

                                                                   
func (s PreferenceState) Clone() PreferenceState {
	cloned := s
	cloned.Strength = cloneStringIntMap(s.Strength)
	return cloned
}

                                                                        
                                                                               
                                                            
func AddSnowballCandidate(current PreferenceState, blockID string) PreferenceState {
	next := current.Clone()
	if current.Finalized || blockID == "" {
		return next
	}
	if next.Strength == nil {
		next.Strength = make(map[string]int)
	}
	if _, exists := next.Strength[blockID]; !exists {
		next.Strength[blockID] = 0
	}
	if next.Preference == "" {
		next.Preference = blockID
	}
	if next.ConfidencePreference == "" {
		next.ConfidencePreference = next.Preference
	}
	return next
}

                                                                        
type PreferencePollInput struct {
	Mode               PreferenceMode
	Candidates         []string
	Votes              map[string]int
	AlphaPreference    int
	AlphaConfidence    int
	FailureSwitchAfter int
}

                                                                             
                                                                        
type PreferenceTransition struct {
	Ignored bool

	Winner      string
	WinnerVotes int
	SoftPoll    bool
	StrongPoll  bool

	PreviousPreference           string
	Preference                   string
	PreviousConfidence           int
	Confidence                   int
	PreviousConfidencePreference string
	ConfidencePreference         string

	PreferenceChanged   bool
	ChangeReason        PreferenceChangeReason
	EmitPreferenceEvent bool
	ResetEvidence       bool
}

                                                                        
                                                                           
func AdvancePreferenceState(current PreferenceState, input PreferencePollInput) (PreferenceState, PreferenceTransition) {
	next := current.Clone()
	transition := PreferenceTransition{
		PreviousPreference:           current.Preference,
		Preference:                   current.Preference,
		PreviousConfidence:           current.Confidence,
		Confidence:                   current.Confidence,
		PreviousConfidencePreference: current.ConfidencePreference,
		ConfidencePreference:         current.ConfidencePreference,
	}
	if current.Finalized {
		transition.Ignored = true
		return next, transition
	}

	winner, winnerVotes := selectPreferencePollWinner(input.Votes)
	if input.Mode == PreferenceModeSnowball {
		winner, winnerVotes = selectKnownPreferencePollWinner(input.Candidates, input.Votes)
	}
	alphaConfidence := input.AlphaConfidence
	alphaPreference := input.AlphaPreference
	if alphaPreference <= 0 {
		alphaPreference = alphaConfidence
	}
	transition.Winner = winner
	transition.WinnerVotes = winnerVotes
	transition.SoftPoll = winner != "" && alphaPreference > 0 && winnerVotes >= alphaPreference
	transition.StrongPoll = winner != "" && alphaConfidence > 0 && winnerVotes >= alphaConfidence

	switch input.Mode {
	case PreferenceModeSnowball:
		advanceCumulativePreference(&next, &transition)
	default:
		advanceConsecutivePreference(&next, &transition, input.Candidates, input.FailureSwitchAfter)
	}

	transition.Preference = next.Preference
	transition.Confidence = next.Confidence
	transition.ConfidencePreference = next.ConfidencePreference
	return next, transition
}

func advanceConsecutivePreference(
	next *PreferenceState,
	transition *PreferenceTransition,
	candidates []string,
	failureSwitchAfter int,
) {
	if transition.StrongPoll {
		next.ConsecutiveFailures = 0
		if transition.Winner != next.Preference {
			changePreferenceState(next, transition, transition.Winner, PreferenceChangeStrongPoll, true)
			next.Confidence = 1
			transition.ResetEvidence = true
		} else {
			next.Confidence++
		}
		return
	}

	if next.Confidence > 0 {
		transition.ResetEvidence = true
	}
	next.Confidence = 0
	next.ConsecutiveFailures++

	if next.Preference == "" && len(candidates) > 0 {
		changePreferenceState(next, transition, selectBestCandidate(candidates), PreferenceChangeInitialFallback, false)
		return
	}
	if failureSwitchAfter <= 0 || next.ConsecutiveFailures < failureSwitchAfter || len(candidates) == 0 {
		return
	}
	best := selectBestCandidate(candidates)
	if best != "" && best != next.Preference {
		changePreferenceState(next, transition, best, PreferenceChangeFailureEscape, true)
		next.ConsecutiveFailures = 0
	}
}

func advanceCumulativePreference(next *PreferenceState, transition *PreferenceTransition) {
	if next.Strength == nil {
		next.Strength = make(map[string]int)
	}
	if transition.SoftPoll {
		next.Strength[transition.Winner]++
		if next.Preference == "" || next.Strength[transition.Winner] > next.Strength[next.Preference] {
			changePreferenceState(next, transition, transition.Winner, PreferenceChangeCumulative, true)
		}
	}

	if transition.StrongPoll {
		next.ConsecutiveFailures = 0
		if next.ConfidencePreference != transition.Winner {
			if next.Confidence > 0 {
				transition.ResetEvidence = true
			}
			next.ConfidencePreference = transition.Winner
			next.Confidence = 1
		} else {
			next.Confidence++
		}
		return
	}

	if next.Confidence > 0 {
		transition.ResetEvidence = true
	}
	next.Confidence = 0
	next.ConsecutiveFailures++
}

func changePreferenceState(
	next *PreferenceState,
	transition *PreferenceTransition,
	preference string,
	reason PreferenceChangeReason,
	emitEvent bool,
) {
	if preference == "" || preference == next.Preference {
		return
	}
	next.Preference = preference
	transition.PreferenceChanged = true
	transition.ChangeReason = reason
	transition.EmitPreferenceEvent = emitEvent
}

func selectPreferencePollWinner(votes map[string]int) (string, int) {
	winner := ""
	winnerVotes := 0
	for blockID, count := range votes {
		switch {
		case count > winnerVotes:
			winner = blockID
			winnerVotes = count
		case count == winnerVotes && count > 0:
			winner = selectBestCandidate([]string{winner, blockID})
		}
	}
	return winner, winnerVotes
}

func selectKnownPreferencePollWinner(candidates []string, votes map[string]int) (string, int) {
	if len(candidates) == 0 || len(votes) == 0 {
		return "", 0
	}
	known := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate != "" {
			known[candidate] = struct{}{}
		}
	}
	filtered := make(map[string]int, len(known))
	for blockID, count := range votes {
		if _, ok := known[blockID]; ok {
			filtered[blockID] = count
		}
	}
	return selectPreferencePollWinner(filtered)
}

                                                                       
type PreferenceChitInput struct {
	Candidates      []string
	PersistFallback bool
}

                                                                            
type PreferenceChitTransition struct {
	Preference        string
	PreferenceChanged bool
	ChangeReason      PreferenceChangeReason
}

                                                                             
                                                                              
                                              
func ResolvePreferenceChit(current PreferenceState, input PreferenceChitInput) (PreferenceState, PreferenceChitTransition) {
	next := current.Clone()
	transition := PreferenceChitTransition{Preference: current.Preference}
	if current.Finalized || current.Preference != "" || len(input.Candidates) == 0 {
		return next, transition
	}

	best := selectBestCandidate(input.Candidates)
	transition.Preference = best
	if input.PersistFallback && best != "" {
		next.Preference = best
		transition.PreferenceChanged = true
		transition.ChangeReason = PreferenceChangePersistedChit
	}
	return next, transition
}

                                                                                
func CanFinalizePreferenceState(state PreferenceState, beta int) bool {
	return beta > 0 &&
		state.Confidence >= beta &&
		preferenceToFinalize(state) != "" &&
		!state.Finalized
}

                                                                  
func FinalizePreferenceState(current PreferenceState) PreferenceState {
	next := current.Clone()
	if current.Finalized {
		return next
	}
	preference := preferenceToFinalize(current)
	if preference == "" {
		return next
	}
	if next.ConfidencePreference != "" {
		next.Preference = preference
	}
	next.Finalized = true
	return next
}

                                                                         
                          
func InvalidatePreferenceCandidate(current PreferenceState, blockID string) (PreferenceState, bool) {
	next := current.Clone()
	if current.Finalized || blockID == "" {
		return next, false
	}
	_, tracked := next.Strength[blockID]
	preferenceInvalid := current.Preference == blockID
	confidencePreferenceInvalid := current.ConfidencePreference == blockID
	if !tracked && !preferenceInvalid && !confidencePreferenceInvalid {
		return next, false
	}

	if next.Strength != nil {
		delete(next.Strength, blockID)
		if len(next.Strength) == 0 {
			next.Strength = nil
		}
	}
	if confidencePreferenceInvalid {
		next.ConfidencePreference = ""
		next.Confidence = 0
	}
	if preferenceInvalid {
		next.Preference = selectStrongestPreference(next.Strength)
		next.Confidence = 0
		next.ConsecutiveFailures = 0
		if next.ConfidencePreference == "" {
			next.ConfidencePreference = next.Preference
		}
	}
	return next, true
}

func preferenceToFinalize(state PreferenceState) string {
	if state.ConfidencePreference != "" {
		return state.ConfidencePreference
	}
	return state.Preference
}

func selectStrongestPreference(strength map[string]int) string {
	best := ""
	bestStrength := 0
	for candidate, candidateStrength := range strength {
		switch {
		case best == "" || candidateStrength > bestStrength:
			best = candidate
			bestStrength = candidateStrength
		case candidateStrength == bestStrength:
			best = selectBestCandidate([]string{best, candidate})
		}
	}
	return best
}

func cloneStringIntMap(input map[string]int) map[string]int {
	if len(input) == 0 {
		return nil
	}
	result := make(map[string]int, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

                                                                 
func extractBlockHash(blockID string) string {
	lastDash := strings.LastIndex(blockID, "-")
	if lastDash == -1 || lastDash == len(blockID)-1 {
		return blockID
	}
	return blockID[lastDash+1:]
}

                                                                              
                       
func extractWindow(blockID string) int {
	idx := strings.LastIndex(blockID, "-w")
	if idx == -1 {
		return 999
	}
	windowPart := blockID[idx+2:]
	if nextDash := strings.Index(windowPart, "-"); nextDash != -1 {
		windowPart = windowPart[:nextDash]
	}
	window, err := strconv.Atoi(windowPart)
	if err != nil {
		return 999
	}
	return window
}

                                                                               
                                                               
func selectBestCandidate(candidates []string) string {
	if len(candidates) == 0 {
		return ""
	}

	bestBlock := candidates[0]
	bestWindow := extractWindow(bestBlock)
	bestHash := extractBlockHash(bestBlock)
	for _, candidate := range candidates[1:] {
		window := extractWindow(candidate)
		hash := extractBlockHash(candidate)
		switch {
		case window < bestWindow:
			bestBlock, bestWindow, bestHash = candidate, window, hash
		case window == bestWindow && hash < bestHash:
			bestBlock, bestHash = candidate, hash
		case window == bestWindow && hash == bestHash && candidate < bestBlock:
			bestBlock = candidate
		}
	}
	return bestBlock
}
