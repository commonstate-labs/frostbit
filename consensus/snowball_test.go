package consensus

import (
	"sync"
	"testing"

	"dex/interfaces"
	"dex/types"
)

                                                                            
                                                  
type stubEventBus struct {
	mu     sync.Mutex
	events []interfaces.Event
}

func (b *stubEventBus) Subscribe(types.EventType, interfaces.EventHandler) {}
func (b *stubEventBus) Publish(e interfaces.Event)                         { b.pushEvent(e) }
func (b *stubEventBus) PublishAsync(e interfaces.Event)                    { b.pushEvent(e) }

func (b *stubEventBus) pushEvent(e interfaces.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, e)
}

func (b *stubEventBus) preferenceChanges() []PreferenceSwitch {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]PreferenceSwitch, 0, len(b.events))
	for _, event := range b.events {
		if event.Type() != types.EventPreferenceChanged {
			continue
		}
		if change, ok := event.Data().(PreferenceSwitch); ok {
			out = append(out, change)
		}
	}
	return out
}

func blockID(height int, proposer string, window int, hash string) string {
	return "block-" + itoa(height) + "-" + proposer + "-w" + itoa(window) + "-" + hash
}

func TestSnowballInitialStateAndCandidateRegistration(t *testing.T) {
	sb := NewSnowball(&stubEventBus{})
	if got := sb.GetPreference(); got != "" {
		t.Fatalf("initial preference = %q, want empty", got)
	}
	if got := sb.GetConfidence(); got != 0 {
		t.Fatalf("initial confidence = %d, want 0", got)
	}
	if sb.IsFinalized() {
		t.Fatal("new Snowball must not be finalized")
	}
	if id, ok := sb.FinalizationPreference(1); ok || id != "" {
		t.Fatalf("empty Snowball is finalizable: id=%q ok=%v", id, ok)
	}

	blockA := blockID(10, "early", 0, "bbbb")
	blockB := blockID(10, "late", 0, "aaaa")
	sb.AddCandidate(blockA)
	if got := sb.GetPreference(); got != blockA {
		t.Fatalf("first candidate did not initialize preference: got=%s want=%s", got, blockA)
	}
	if got := sb.GetConfidence(); got != 0 {
		t.Fatalf("candidate registration added confidence: %d", got)
	}

	                                                                         
	                     
	sb.AddCandidates([]string{blockB, blockA})
	if got := sb.GetPreference(); got != blockA {
		t.Fatalf("later candidate changed first-seen preference: got=%s want=%s", got, blockA)
	}
}

func TestSnowballSoftPollUsesCumulativeStrengthAndStrictOvertake(t *testing.T) {
	bus := &stubEventBus{}
	sb := NewSnowball(bus)
	blockA := blockID(10, "early", 0, "bbbb")
	blockB := blockID(10, "late", 0, "aaaa")
	candidates := []string{blockA, blockB}
	sb.AddCandidates(candidates)

	const (
		alphaPreference = 3
		alphaConfidence = 5
	)
	softA := map[string]int{blockA: 3, blockB: 2}
	softB := map[string]int{blockB: 3, blockA: 2}

	sb.RecordVote(candidates, softA, alphaPreference, alphaConfidence)
	sb.RecordVote(candidates, softB, alphaPreference, alphaConfidence)
	if got := sb.GetPreference(); got != blockA {
		t.Fatalf("equal cumulative strength switched preference: got=%s want=%s", got, blockA)
	}
	if got := sb.GetConfidence(); got != 0 {
		t.Fatalf("soft polls built finalization confidence: %d", got)
	}

	sb.RecordVote(candidates, softB, alphaPreference, alphaConfidence)
	if got := sb.GetPreference(); got != blockB {
		t.Fatalf("strict cumulative overtake did not switch preference: got=%s want=%s", got, blockB)
	}
	if got := sb.GetConfidence(); got != 0 {
		t.Fatalf("soft preference switch built confidence: %d", got)
	}

	changes := bus.preferenceChanges()
	if len(changes) != 1 {
		t.Fatalf("cumulative preference switch events = %d, want 1: %+v", len(changes), changes)
	}
	change := changes[0]
	if change.BlockID != blockA ||
		change.Winner != blockB ||
		change.AlphaPreference != alphaPreference ||
		change.AlphaConfidence != alphaConfidence {
		t.Fatalf("unexpected preference event: %+v", change)
	}
}

func TestSnowballFinalizationUsesRecentStrongWinner(t *testing.T) {
	sb := NewSnowball(&stubEventBus{})
	blockA := blockID(10, "early", 0, "bbbb")
	blockB := blockID(10, "late", 0, "aaaa")
	candidates := []string{blockA, blockB}
	sb.AddCandidates(candidates)

	const (
		alphaPreference = 3
		alphaConfidence = 5
		beta            = 3
	)

	                                                                     
	                                               
	for i := 0; i < 6; i++ {
		sb.RecordVote(
			candidates,
			map[string]int{blockA: alphaPreference, blockB: 2},
			alphaPreference,
			alphaConfidence,
		)
	}
	for round := 1; round <= beta; round++ {
		sb.RecordVote(
			candidates,
			map[string]int{blockB: alphaConfidence},
			alphaPreference,
			alphaConfidence,
		)
		id, ok := sb.FinalizationPreference(beta)
		if round < beta {
			if ok || id != "" {
				t.Fatalf("round %d finalized early: id=%q ok=%v", round, id, ok)
			}
			continue
		}
		if !ok || id != blockB {
			t.Fatalf("Beta strong B polls chose id=%q ok=%v, want B", id, ok)
		}
	}

	if got := sb.GetPreference(); got != blockA {
		t.Fatalf("setup failed to keep cumulative preference A: %s", got)
	}
	if !sb.Finalize(blockB) {
		t.Fatal("Finalize rejected the eligible recent strong winner")
	}
	if !sb.IsFinalized() || sb.GetPreference() != blockB {
		t.Fatalf("finalized wrapper did not lock B: finalized=%v preference=%s",
			sb.IsFinalized(), sb.GetPreference())
	}
	if id, ok := sb.FinalizationPreference(1); ok || id != "" {
		t.Fatalf("already-finalized wrapper remained eligible: id=%q ok=%v", id, ok)
	}
}

func TestSnowballFinalizeRejectsWrongCandidateAndIsMonotonic(t *testing.T) {
	sb := NewSnowball(&stubEventBus{})
	blockA := blockID(10, "early", 0, "bbbb")
	blockB := blockID(10, "late", 0, "aaaa")
	candidates := []string{blockA, blockB}
	sb.AddCandidates(candidates)

	sb.RecordVote(candidates, map[string]int{blockA: 5}, 3, 5)
	if id, ok := sb.FinalizationPreference(1); !ok || id != blockA {
		t.Fatalf("expected A to be eligible: id=%q ok=%v", id, ok)
	}
	if sb.Finalize(blockB) {
		t.Fatal("Finalize accepted a block other than the eligible confidence preference")
	}
	if sb.IsFinalized() {
		t.Fatal("wrong-ID finalization mutated state")
	}
	if !sb.Finalize(blockA) {
		t.Fatal("Finalize rejected the eligible block")
	}
	if !sb.Finalize(blockA) {
		t.Fatal("idempotent Finalize rejected the already-sealed block")
	}
	if sb.Finalize(blockB) {
		t.Fatal("finalized Snowball accepted a different block")
	}

	sb.RecordVote(candidates, map[string]int{blockB: 5}, 3, 5)
	if !sb.IsFinalized() || sb.GetPreference() != blockA || sb.GetConfidence() != 1 {
		t.Fatalf("late poll changed finalized state: preference=%s confidence=%d finalized=%v",
			sb.GetPreference(), sb.GetConfidence(), sb.IsFinalized())
	}
}

func TestSnowballFailedPollResetsConfidenceWithoutChangingPreference(t *testing.T) {
	sb := NewSnowball(&stubEventBus{})
	blockA := blockID(10, "early", 0, "bbbb")
	blockB := blockID(10, "late", 0, "aaaa")
	candidates := []string{blockA, blockB}
	sb.AddCandidates(candidates)

	sb.RecordVote(candidates, map[string]int{blockA: 5}, 3, 5)
	sb.RecordVote(candidates, map[string]int{blockA: 5}, 3, 5)
	if got := sb.GetConfidence(); got != 2 {
		t.Fatalf("setup confidence = %d, want 2", got)
	}

	sb.RecordVote(candidates, map[string]int{blockA: 2, blockB: 2}, 3, 5)
	if got := sb.GetConfidence(); got != 0 {
		t.Fatalf("failed poll did not reset confidence: %d", got)
	}
	if got := sb.GetPreference(); got != blockA {
		t.Fatalf("failed poll changed preference: got=%s want=%s", got, blockA)
	}
	if id, ok := sb.FinalizationPreference(1); ok || id != "" {
		t.Fatalf("reset confidence remained finalizable: id=%q ok=%v", id, ok)
	}
}

func TestSnowballWrapperIgnoresVotesForUnknownCandidates(t *testing.T) {
	sb := NewSnowball(&stubEventBus{})
	blockA := blockID(10, "early", 0, "bbbb")
	unknownB := blockID(10, "unknown", 0, "aaaa")
	sb.AddCandidate(blockA)

	sb.RecordVote(
		[]string{blockA},
		map[string]int{unknownB: 5, blockA: 1},
		3,
		5,
	)
	if got := sb.GetPreference(); got != blockA {
		t.Fatalf("unknown candidate votes changed preference: got=%s want=%s", got, blockA)
	}
	if got := sb.GetConfidence(); got != 0 {
		t.Fatalf("unknown candidate votes built confidence: %d", got)
	}
	if id, ok := sb.FinalizationPreference(1); ok || id != "" {
		t.Fatalf("unknown candidate became finalizable: id=%q ok=%v", id, ok)
	}
}

func TestSnowballInvalidationPreservesRemainingCandidate(t *testing.T) {
	sb := NewSnowball(&stubEventBus{})
	blockA := blockID(10, "early", 0, "bbbb")
	blockB := blockID(10, "late", 0, "aaaa")
	candidates := []string{blockA, blockB}
	sb.AddCandidates(candidates)

	sb.RecordVote(candidates, map[string]int{blockA: 5}, 3, 5)
	sb.RecordVote(candidates, map[string]int{blockB: 3, blockA: 2}, 3, 5)
	if !sb.InvalidateCandidate(blockA) {
		t.Fatal("tracked candidate A was not invalidated")
	}
	if got := sb.GetPreference(); got != blockB {
		t.Fatalf("remaining candidate was not selected: got=%s want=%s", got, blockB)
	}
	if got := sb.GetConfidence(); got != 0 {
		t.Fatalf("invalidation did not conservatively reset confidence: %d", got)
	}
	if sb.InvalidateCandidate("not-tracked") {
		t.Fatal("unknown candidate reported as invalidated")
	}
}

func TestSnowballGetLastVotesReturnsCopy(t *testing.T) {
	sb := NewSnowball(&stubEventBus{})
	blockA := blockID(10, "p", 0, "a")
	sb.AddCandidate(blockA)
	sb.RecordVote([]string{blockA}, map[string]int{blockA: 5}, 3, 5)

	first := sb.GetLastVotes()
	first[blockA] = 999
	if second := sb.GetLastVotes(); second[blockA] != 5 {
		t.Fatalf("GetLastVotes exposed internal map: %+v", second)
	}
}
