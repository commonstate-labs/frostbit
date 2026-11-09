package consensus

import (
	"dex/formal/itftrace"
	"dex/formal/refinement"
	"os"
	"reflect"
	"testing"
)

const (
	formalSnowballAlphaPreference = 2
	formalSnowballAlphaConfidence = 3
	formalSnowballBeta            = 3
	formalSnowballBlockA          = "block-10-formal-a-w0-aaaa"
	formalSnowballBlockB          = "block-10-formal-b-w0-bbbb"
)

func TestFormalSnowballPreferenceITFDifferential(t *testing.T) {
	cases := []struct {
		name string
		env  string
	}{
		{name: "soft-cumulative-switch", env: "DEX_FORMAL_ITF_SNOWBALL_SOFT"},
		{name: "failed-poll-reset", env: "DEX_FORMAL_ITF_SNOWBALL_RESET"},
		{name: "hard-winner-switch", env: "DEX_FORMAL_ITF_SNOWBALL_SWITCH"},
		{name: "recent-hard-winner-finalizes", env: "DEX_FORMAL_ITF_SNOWBALL_FINALIZE"},
		{name: "candidate-invalidation", env: "DEX_FORMAL_ITF_SNOWBALL_INVALIDATE"},
		{name: "unknown-candidate-vote", env: "DEX_FORMAL_ITF_SNOWBALL_UNKNOWN"},
	}
	if os.Getenv(cases[0].env) == "" {
		t.Skip("snowball ITF paths are set by formal/run-go-differential.ps1")
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runFormalSnowballTrace(t, os.Getenv(tc.env))
		})
	}
}

func runFormalSnowballTrace(t *testing.T, path string) {
	t.Helper()
	trace, err := itftrace.Load(path)
	if err != nil {
		t.Fatalf("load Quint snowball ITF trace: %v", err)
	}
	if len(trace.States) < 2 {
		t.Fatalf("snowball differential trace states=%d want>=2", len(trace.States))
	}

	initial, err := refinement.SnowballFromITF(trace.States[0])
	if err != nil {
		t.Fatal(err)
	}
	pure, wrapper := initializeFormalSnowballProductionState(t, initial)
	assertFormalSnowballStateRelation(t, trace.States[0], pure, wrapper, 0)
	assertFormalSnowballFinalizationBoundary(t, initial, pure, wrapper, 0)

	for i := 1; i < len(trace.States); i++ {
		before, err := refinement.SnowballFromITF(trace.States[i-1])
		if err != nil {
			t.Fatal(err)
		}
		after, err := refinement.SnowballFromITF(trace.States[i])
		if err != nil {
			t.Fatal(err)
		}

		assertFormalSnowballFinalizationBoundary(t, before, pure, wrapper, i-1)
		applyFormalSnowballModelStep(t, before, after, &pure, wrapper)
		assertFormalSnowballStateRelation(t, trace.States[i], pure, wrapper, i)
		assertFormalSnowballFinalizationBoundary(t, after, pure, wrapper, i)
	}
}

func applyFormalSnowballModelStep(
	t *testing.T,
	before, after refinement.SnowballState,
	pure *PreferenceState,
	wrapper *Snowball,
) {
	t.Helper()

	switch {
	case !before.Finalized && after.Finalized:
		finalID := formalSnowballBlock(after.FinalCandidate)
		if finalID == "" || !CanFinalizePreferenceState(*pure, formalSnowballBeta) {
			t.Fatalf("model finalized outside Go Beta boundary: before=%+v after=%+v", before, after)
		}
		*pure = FinalizePreferenceState(*pure)
		if !wrapper.Finalize(finalID) {
			t.Fatalf("production wrapper rejected modeled final candidate %s", after.FinalCandidate)
		}

	case before.TrackedA != after.TrackedA || before.TrackedB != after.TrackedB:
		invalidID := ""
		switch {
		case before.TrackedA && !after.TrackedA:
			invalidID = formalSnowballBlockA
		case before.TrackedB && !after.TrackedB:
			invalidID = formalSnowballBlockB
		default:
			t.Fatalf("unsupported modeled candidate-set transition: before=%+v after=%+v", before, after)
		}
		var invalidated bool
		*pure, invalidated = InvalidatePreferenceCandidate(*pure, invalidID)
		if !invalidated || !wrapper.InvalidateCandidate(invalidID) {
			t.Fatalf("production did not invalidate modeled candidate %s", invalidID)
		}

	case after.StrengthA != before.StrengthA || after.StrengthB != before.StrengthB:
		winner := ""
		switch {
		case after.StrengthA == before.StrengthA+1 && after.StrengthB == before.StrengthB:
			winner = formalSnowballBlockA
		case after.StrengthB == before.StrengthB+1 && after.StrengthA == before.StrengthA:
			winner = formalSnowballBlockB
		default:
			t.Fatalf("model poll did not add exactly one winner strength: before=%+v after=%+v", before, after)
		}
		winnerVotes := formalSnowballAlphaPreference
		if after.Confidence > 0 {
			winnerVotes = formalSnowballAlphaConfidence
		}
		votes := map[string]int{winner: winnerVotes}
		candidates := formalSnowballCandidates(before)
		*pure, _ = AdvancePreferenceState(*pure, PreferencePollInput{
			Mode:            PreferenceModeSnowball,
			Candidates:      candidates,
			Votes:           votes,
			AlphaPreference: formalSnowballAlphaPreference,
			AlphaConfidence: formalSnowballAlphaConfidence,
		})
		wrapper.RecordVote(candidates, votes, formalSnowballAlphaPreference, formalSnowballAlphaConfidence)

	case before.Confidence > 0 && after.Confidence == 0:
		candidates := formalSnowballCandidates(before)
		*pure, _ = AdvancePreferenceState(*pure, PreferencePollInput{
			Mode:            PreferenceModeSnowball,
			Candidates:      candidates,
			Votes:           map[string]int{},
			AlphaPreference: formalSnowballAlphaPreference,
			AlphaConfidence: formalSnowballAlphaConfidence,
		})
		wrapper.RecordVote(candidates, map[string]int{}, formalSnowballAlphaPreference, formalSnowballAlphaConfidence)

	case reflect.DeepEqual(before, after):
		candidates := formalSnowballCandidates(before)
		votes := map[string]int{"block-10-formal-unknown-w0-cccc": formalSnowballAlphaConfidence}
		*pure, _ = AdvancePreferenceState(*pure, PreferencePollInput{
			Mode:            PreferenceModeSnowball,
			Candidates:      candidates,
			Votes:           votes,
			AlphaPreference: formalSnowballAlphaPreference,
			AlphaConfidence: formalSnowballAlphaConfidence,
		})
		wrapper.RecordVote(candidates, votes, formalSnowballAlphaPreference, formalSnowballAlphaConfidence)

	default:
		t.Fatalf("unsupported modeled snowball step: before=%+v after=%+v", before, after)
	}
}

func assertFormalSnowballStateRelation(
	t *testing.T,
	model itftrace.State,
	pure PreferenceState,
	wrapper *Snowball,
	step int,
) {
	t.Helper()
	expected, err := refinement.SnowballFromITF(model)
	if err != nil {
		t.Fatal(err)
	}
	pureState := formalSnowballStateFromGo(pure)
	if !reflect.DeepEqual(pureState, expected) {
		t.Fatalf("snowball pure state relation failed at step=%d action=%s\nactual:   %+v\nexpected: %+v",
			step, model.Action, pureState, expected)
	}

	wrapper.mu.RLock()
	wrapperState := wrapper.state.Clone()
	wrapper.mu.RUnlock()
	if !reflect.DeepEqual(wrapperState, pure) {
		t.Fatalf("snowball production wrapper drifted from pure state at step=%d action=%s\nwrapper: %+v\npure:    %+v",
			step, model.Action, wrapperState, pure)
	}
	if actual := formalSnowballStateFromGo(wrapperState); !reflect.DeepEqual(actual, expected) {
		t.Fatalf("snowball wrapper state relation failed at step=%d action=%s\nactual:   %+v\nexpected: %+v",
			step, model.Action, actual, expected)
	}
	if history := wrapper.GetSuccessHistory(); len(history) != int(expected.Confidence) {
		t.Fatalf("snowball hard-poll evidence length=%d want confidence=%d at step=%d", len(history), expected.Confidence, step)
	}
}

func assertFormalSnowballFinalizationBoundary(
	t *testing.T,
	model refinement.SnowballState,
	pure PreferenceState,
	wrapper *Snowball,
	step int,
) {
	t.Helper()
	wantReady := !model.Finalized && model.Confidence >= formalSnowballBeta
	wantID := ""
	if wantReady {
		wantID = formalSnowballBlock(model.ConfidencePreference)
	}
	if got := CanFinalizePreferenceState(pure, formalSnowballBeta); got != wantReady {
		t.Fatalf("pure Beta boundary ready=%t want=%t at step=%d state=%+v", got, wantReady, step, model)
	}
	gotID, gotReady := wrapper.FinalizationPreference(formalSnowballBeta)
	if gotReady != wantReady || gotID != wantID {
		t.Fatalf("wrapper Beta boundary=%q/%t want=%q/%t at step=%d state=%+v",
			gotID, gotReady, wantID, wantReady, step, model)
	}
}

func initializeFormalSnowballProductionState(
	t *testing.T,
	model refinement.SnowballState,
) (PreferenceState, *Snowball) {
	t.Helper()
	if !model.TrackedA || !model.TrackedB || model.StrengthB != 0 || model.Confidence != 0 || model.Finalized {
		t.Fatalf("unsupported Snowball model pre-state: %+v", model)
	}
	candidates := []string{formalSnowballBlockA, formalSnowballBlockB}
	pure := NewPreferenceState()
	for _, candidate := range candidates {
		pure = AddSnowballCandidate(pure, candidate)
	}
	wrapper := NewSnowball(nil)
	wrapper.AddCandidates(candidates)
	for i := int64(0); i < model.StrengthA; i++ {
		votes := map[string]int{formalSnowballBlockA: formalSnowballAlphaPreference}
		pure, _ = AdvancePreferenceState(pure, PreferencePollInput{
			Mode:            PreferenceModeSnowball,
			Candidates:      candidates,
			Votes:           votes,
			AlphaPreference: formalSnowballAlphaPreference,
			AlphaConfidence: formalSnowballAlphaConfidence,
		})
		wrapper.RecordVote(candidates, votes, formalSnowballAlphaPreference, formalSnowballAlphaConfidence)
	}
	return pure, wrapper
}

func formalSnowballStateFromGo(state PreferenceState) refinement.SnowballState {
	_, trackedA := state.Strength[formalSnowballBlockA]
	_, trackedB := state.Strength[formalSnowballBlockB]
	finalCandidate := "A"
	if state.Finalized {
		finalCandidate = formalSnowballTag(state.Preference)
	}
	return refinement.SnowballState{
		Preference:           formalSnowballTag(state.Preference),
		ConfidencePreference: formalSnowballTag(state.ConfidencePreference),
		Confidence:           int64(state.Confidence),
		StrengthA:            int64(state.Strength[formalSnowballBlockA]),
		StrengthB:            int64(state.Strength[formalSnowballBlockB]),
		TrackedA:             trackedA,
		TrackedB:             trackedB,
		Finalized:            state.Finalized,
		FinalCandidate:       finalCandidate,
	}
}

func formalSnowballCandidates(state refinement.SnowballState) []string {
	candidates := make([]string, 0, 2)
	if state.TrackedA {
		candidates = append(candidates, formalSnowballBlockA)
	}
	if state.TrackedB {
		candidates = append(candidates, formalSnowballBlockB)
	}
	return candidates
}

func formalSnowballBlock(tag string) string {
	switch tag {
	case "A":
		return formalSnowballBlockA
	case "B":
		return formalSnowballBlockB
	default:
		return ""
	}
}

func formalSnowballTag(blockID string) string {
	switch blockID {
	case formalSnowballBlockA:
		return "A"
	case formalSnowballBlockB:
		return "B"
	default:
		return ""
	}
}
