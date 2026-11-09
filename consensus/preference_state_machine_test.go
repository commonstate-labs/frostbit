package consensus

import (
	"reflect"
	"testing"
)

func TestPreferenceStateMachineTransitionIsPureAndReplayable(t *testing.T) {
	blockA := blockID(10, "early", 0, "bbbb")
	blockB := blockID(10, "late", 0, "aaaa")
	current := PreferenceState{
		Preference: blockA,
		Confidence: 2,
		Strength:   map[string]int{blockA: 3},
	}
	input := PreferencePollInput{
		Mode:               PreferenceModeSnowflake,
		Candidates:         []string{blockA, blockB},
		Votes:              map[string]int{blockB: 12, blockA: 3},
		AlphaPreference:    12,
		AlphaConfidence:    12,
		FailureSwitchAfter: LegacyFailureSwitchAfter,
	}

	firstState, firstTransition := AdvancePreferenceState(current, input)
	secondState, secondTransition := AdvancePreferenceState(current, input)

	if !reflect.DeepEqual(firstState, secondState) || !reflect.DeepEqual(firstTransition, secondTransition) {
		t.Fatal("same state and input must produce exactly the same transition")
	}
	if current.Preference != blockA || current.Confidence != 2 || current.Strength[blockA] != 3 {
		t.Fatalf("input state was mutated: %+v", current)
	}
	if input.Votes[blockB] != 12 {
		t.Fatalf("input votes were mutated: %+v", input.Votes)
	}
	if firstState.Preference != blockB || firstState.Confidence != 1 {
		t.Fatalf("unexpected next state: %+v", firstState)
	}
	firstState.Strength[blockA] = 99
	if current.Strength[blockA] != 3 {
		t.Fatal("next state shares its strength map with the input state")
	}
}

func TestResolvePreferenceChitDynamicAndPersistentFallback(t *testing.T) {
	blockA := blockID(10, "early", 0, "bbbb")
	blockB := blockID(10, "late", 0, "aaaa")
	candidates := []string{blockA, blockB}
	current := NewPreferenceState()

	dynamicState, dynamic := ResolvePreferenceChit(current, PreferenceChitInput{
		Candidates:      candidates,
		PersistFallback: false,
	})
	if dynamic.Preference != blockB {
		t.Fatalf("expected lower-hash B, got %s", dynamic.Preference)
	}
	if dynamicState.Preference != "" || dynamic.PreferenceChanged {
		t.Fatalf("dynamic fallback must not persist preference: state=%+v transition=%+v", dynamicState, dynamic)
	}

	persistentState, persistent := ResolvePreferenceChit(current, PreferenceChitInput{
		Candidates:      candidates,
		PersistFallback: true,
	})
	if persistentState.Preference != blockB || !persistent.PreferenceChanged {
		t.Fatalf("persistent fallback did not record B: state=%+v transition=%+v", persistentState, persistent)
	}
	if current.Preference != "" {
		t.Fatal("Chit transition mutated the input state")
	}
}

func TestPreferenceStateMachineCumulativeStrengthSeparatesThresholds(t *testing.T) {
	blockA := blockID(10, "early", 0, "bbbb")
	blockB := blockID(10, "late", 0, "aaaa")
	state := PreferenceState{
		Preference: blockA,
		Strength:   map[string]int{blockA: 2},
	}
	softB := PreferencePollInput{
		Mode:               PreferenceModeSnowball,
		Candidates:         []string{blockA, blockB},
		Votes:              map[string]int{blockB: 8, blockA: 7},
		AlphaPreference:    8,
		AlphaConfidence:    12,
		FailureSwitchAfter: LegacyFailureSwitchAfter,
	}

	for round := 1; round <= 2; round++ {
		var transition PreferenceTransition
		state, transition = AdvancePreferenceState(state, softB)
		if !transition.SoftPoll || transition.StrongPoll {
			t.Fatalf("round %d should be soft but not strong: %+v", round, transition)
		}
		if state.Preference != blockA {
			t.Fatalf("strength tie must retain A on round %d: %+v", round, state)
		}
		if state.Confidence != 0 {
			t.Fatalf("soft poll must not build finalization confidence: %+v", state)
		}
	}

	state, transition := AdvancePreferenceState(state, softB)
	if state.Preference != blockB || !transition.PreferenceChanged {
		t.Fatalf("B should become preferred only after greater cumulative strength: state=%+v transition=%+v", state, transition)
	}
	if state.Confidence != 0 {
		t.Fatalf("preference threshold alone must not build confidence: %+v", state)
	}

	strongB := softB
	strongB.Votes = map[string]int{blockB: 12, blockA: 3}
	state, transition = AdvancePreferenceState(state, strongB)
	if !transition.StrongPoll || state.Preference != blockB || state.Confidence != 1 {
		t.Fatalf("hard B poll should start confidence at one: state=%+v transition=%+v", state, transition)
	}
}

func TestPreferenceStateMachineFinalizationIsMonotonic(t *testing.T) {
	blockA := blockID(10, "early", 0, "bbbb")
	state := PreferenceState{
		Preference: blockA,
		Confidence: 15,
		Strength:   map[string]int{blockA: 4},
	}
	if !CanFinalizePreferenceState(state, 15) {
		t.Fatal("state should be eligible for finalization")
	}
	finalized := FinalizePreferenceState(state)
	if !finalized.Finalized || CanFinalizePreferenceState(finalized, 1) {
		t.Fatalf("unexpected finalized state: %+v", finalized)
	}

	next, transition := AdvancePreferenceState(finalized, PreferencePollInput{
		Mode:            PreferenceModeSnowflake,
		Candidates:      []string{blockA},
		Votes:           map[string]int{blockA: 20},
		AlphaPreference: 1,
		AlphaConfidence: 1,
	})
	if !transition.Ignored || !reflect.DeepEqual(next, finalized) {
		t.Fatalf("finalized decision changed: next=%+v transition=%+v", next, transition)
	}
	if _, invalidated := InvalidatePreferenceCandidate(finalized, blockA); invalidated {
		t.Fatal("finalized preference must not be invalidated")
	}
}

func TestSnowballWrapperMatchesPureProductionTransitions(t *testing.T) {
	blockA := blockID(10, "early", 0, "bbbb")
	blockB := blockID(10, "late", 0, "aaaa")
	candidates := []string{blockA, blockB}
	const (
		alphaPreference = 3
		alphaConfidence = 5
		beta            = 3
	)
	state := NewPreferenceState()
	for _, candidate := range candidates {
		state = AddSnowballCandidate(state, candidate)
	}
	wrapper := NewSnowball(&stubEventBus{})
	wrapper.AddCandidates(candidates)

	apply := func(votes map[string]int) {
		t.Helper()
		state, _ = AdvancePreferenceState(state, PreferencePollInput{
			Mode:            PreferenceModeSnowball,
			Candidates:      candidates,
			Votes:           votes,
			AlphaPreference: alphaPreference,
			AlphaConfidence: alphaConfidence,
		})
		wrapper.RecordVote(candidates, votes, alphaPreference, alphaConfidence)
		if !reflect.DeepEqual(wrapper.state, state) {
			t.Fatalf("wrapper drifted from pure state:\nwrapper=%+v\npure=%+v", wrapper.state, state)
		}
	}

	                                                                     
	                                                                      
	          
	apply(map[string]int{blockA: 3, blockB: 2})
	apply(map[string]int{blockB: 3, blockA: 2})
	apply(map[string]int{blockB: 3, blockA: 2})
	apply(map[string]int{blockA: 5})
	apply(map[string]int{blockA: 2, blockB: 2})

	for round := 0; round < beta; round++ {
		apply(map[string]int{blockB: 5})
	}

	wantID := preferenceToFinalize(state)
	wantOK := CanFinalizePreferenceState(state, beta)
	gotID, gotOK := wrapper.FinalizationPreference(beta)
	if gotID != wantID || gotOK != wantOK {
		t.Fatalf("finalization preference drifted: wrapper=%q/%v pure=%q/%v",
			gotID, gotOK, wantID, wantOK)
	}
	if !gotOK {
		t.Fatalf("setup did not reach Beta: %+v", state)
	}

	state = FinalizePreferenceState(state)
	if !wrapper.Finalize(gotID) {
		t.Fatalf("wrapper rejected pure finalization preference %s", gotID)
	}
	if !reflect.DeepEqual(wrapper.state, state) {
		t.Fatalf("wrapper finalization drifted:\nwrapper=%+v\npure=%+v", wrapper.state, state)
	}
}
