package consensus

import "testing"

func TestPollAccumulatorRecordAndCompletion(t *testing.T) {
	tests := []struct {
		name            string
		weights         map[string]int
		startCandidates map[string]struct{}
		records         []struct {
			peer string
			vote string
		}
		wantAccepted    []bool
		wantCompletedAt int
		wantVotes       map[string]int
		wantResponded   int
		closeAtEnd      bool
	}{
		{
			name:            "rejects unsampled and duplicate peers",
			weights:         map[string]int{"peer-a": 8, "peer-b": 7},
			startCandidates: map[string]struct{}{"A": {}, "B": {}},
			records: []struct {
				peer string
				vote string
			}{
				{peer: "unknown", vote: "A"},
				{peer: "peer-a", vote: "A"},
				{peer: "peer-a", vote: "A"},
				{peer: "peer-b", vote: "B"},
			},
			wantAccepted:    []bool{false, true, false, true},
			wantCompletedAt: 3,
			wantVotes:       map[string]int{"A": 8, "B": 7},
			wantResponded:   15,
		},
		{
			name:            "high weight response reaches hard threshold",
			weights:         map[string]int{"heavy": 12, "silent": 3},
			startCandidates: map[string]struct{}{"A": {}},
			records: []struct {
				peer string
				vote string
			}{
				{peer: "heavy", vote: "A"},
			},
			wantAccepted:    []bool{true},
			wantCompletedAt: 0,
			wantVotes:       map[string]int{"A": 12},
			wantResponded:   12,
		},
		{
			name:            "close exposes partial timeout votes",
			weights:         map[string]int{"returned": 7, "silent": 8},
			startCandidates: map[string]struct{}{"A": {}, "B": {}},
			records: []struct {
				peer string
				vote string
			}{
				{peer: "returned", vote: "B"},
			},
			wantAccepted:    []bool{true},
			wantCompletedAt: -1,
			wantVotes:       map[string]int{"B": 7},
			wantResponded:   7,
			closeAtEnd:      true,
		},
		{
			name:            "all slots complete without hard winner",
			weights:         map[string]int{"peer-a": 8, "peer-b": 7},
			startCandidates: map[string]struct{}{"A": {}, "B": {}},
			records: []struct {
				peer string
				vote string
			}{
				{peer: "peer-a", vote: "A"},
				{peer: "peer-b", vote: "B"},
			},
			wantAccepted:    []bool{true, true},
			wantCompletedAt: 1,
			wantVotes:       map[string]int{"A": 8, "B": 7},
			wantResponded:   15,
		},
		{
			name:            "candidate added after start cannot end poll early",
			weights:         map[string]int{"late": 12, "rest": 3},
			startCandidates: map[string]struct{}{"A": {}},
			records: []struct {
				peer string
				vote string
			}{
				{peer: "late", vote: "B"},
				{peer: "rest", vote: "A"},
			},
			wantAccepted:    []bool{true, true},
			wantCompletedAt: 1,
			wantVotes:       map[string]int{"B": 12, "A": 3},
			wantResponded:   15,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			accumulator, err := NewPollAccumulator(
				15,
				8,
				12,
				tt.weights,
				tt.startCandidates,
			)
			if err != nil {
				t.Fatalf("NewPollAccumulator: %v", err)
			}
			for i, record := range tt.records {
				result := accumulator.Record(record.peer, record.vote)
				if result.Accepted != tt.wantAccepted[i] {
					t.Fatalf("record %d accepted=%v, want %v", i, result.Accepted, tt.wantAccepted[i])
				}
				if result.Completed != (i == tt.wantCompletedAt) {
					t.Fatalf("record %d completed=%v, want %v", i, result.Completed, i == tt.wantCompletedAt)
				}
			}

			var outcome PollOutcome
			if tt.closeAtEnd {
				outcome = accumulator.Close()
			} else {
				outcome = accumulator.Outcome()
			}
			if outcome.RespondedSlots != tt.wantResponded {
				t.Fatalf("responded slots=%d, want %d", outcome.RespondedSlots, tt.wantResponded)
			}
			if outcome.Closed != (tt.wantCompletedAt >= 0 || tt.closeAtEnd) {
				t.Fatalf("closed=%v", outcome.Closed)
			}
			if len(outcome.Votes) != len(tt.wantVotes) {
				t.Fatalf("votes=%v, want %v", outcome.Votes, tt.wantVotes)
			}
			for candidate, want := range tt.wantVotes {
				if got := outcome.Votes[candidate]; got != want {
					t.Fatalf("votes[%s]=%d, want %d (all=%v)", candidate, got, want, outcome.Votes)
				}
			}

			outcome.Votes["mutated"] = 99
			if _, exists := accumulator.Outcome().Votes["mutated"]; exists {
				t.Fatal("Outcome returned accumulator-owned vote map")
			}
		})
	}
}

func TestNewPollAccumulatorValidatesAndClonesWeights(t *testing.T) {
	tests := []struct {
		name    string
		k       int
		alpha   int
		weights map[string]int
	}{
		{name: "zero K", k: 0, alpha: 1, weights: map[string]int{"peer": 1}},
		{name: "invalid alpha", k: 15, alpha: 16, weights: map[string]int{"peer": 15}},
		{name: "empty weights", k: 15, alpha: 12},
		{name: "non-positive weight", k: 15, alpha: 12, weights: map[string]int{"a": 15, "b": 0}},
		{name: "short total", k: 15, alpha: 12, weights: map[string]int{"a": 14}},
		{name: "long total", k: 15, alpha: 12, weights: map[string]int{"a": 16}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewPollAccumulator(tt.k, 1, tt.alpha, tt.weights, map[string]struct{}{"A": {}}); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}

	weights := map[string]int{"peer": 15}
	candidates := map[string]struct{}{"A": {}}
	accumulator, err := NewPollAccumulator(15, 8, 12, weights, candidates)
	if err != nil {
		t.Fatalf("NewPollAccumulator: %v", err)
	}
	weights["peer"] = 1
	weights["other"] = 14
	delete(candidates, "A")
	candidates["B"] = struct{}{}

	result := accumulator.Record("peer", "A")
	if !result.Accepted || !result.Completed || result.Weight != 15 {
		t.Fatalf("caller mutation changed accumulator snapshots: %+v", result)
	}
}

func TestPollAccumulatorOnlyClosesEarlyWhenSoftOutcomeIsImpossible(t *testing.T) {
	t.Run("hard outcome impossible but soft outcome remains possible", func(t *testing.T) {
		accumulator, err := NewPollAccumulator(
			15,
			8,
			12,
			map[string]int{"vote-a": 4, "abstain": 4, "remaining-a": 4, "remaining-b": 3},
			map[string]struct{}{"A": {}},
		)
		if err != nil {
			t.Fatalf("NewPollAccumulator: %v", err)
		}
		if result := accumulator.Record("vote-a", "A"); result.Completed {
			t.Fatal("poll closed while a soft-threshold outcome was still possible")
		}
		if result := accumulator.Record("abstain", ""); result.Completed {
			t.Fatal("poll closed merely because the hard threshold became impossible")
		}
	})

	t.Run("remaining slots cannot reach soft threshold", func(t *testing.T) {
		accumulator, err := NewPollAccumulator(
			15,
			8,
			12,
			map[string]int{"known-failure": 8, "remaining-a": 4, "remaining-b": 3},
			map[string]struct{}{"A": {}},
		)
		if err != nil {
			t.Fatalf("NewPollAccumulator: %v", err)
		}
		result := accumulator.Record("known-failure", "")
		if !result.Completed {
			t.Fatal("poll stayed open although at most 7 remaining slots cannot reach AlphaPreference=8")
		}
	})
}
