package consensus

import (
	"testing"

	"dex/types"
)

func TestSnowballEvidenceTracksCurrentStrongStreak(t *testing.T) {
	sb := NewSnowball(&stubEventBus{})
	blockA := blockID(10, "p1", 0, "aaa")
	blockB := blockID(10, "p2", 0, "bbb")
	candidates := []string{blockA, blockB}
	sb.AddCandidates(candidates)

	record := func(block string, seqID uint32) {
		t.Helper()
		sb.RecordVoteWithDetails(
			candidates,
			map[string]int{block: 5},
			3,
			5,
			seqID,
			[]byte{byte(seqID)},
			[]types.FinalizationChit{{
				NodeID:      "n1",
				PreferredID: block,
				Signature:   []byte{byte(seqID), 0xff},
			}},
		)
	}

	record(blockA, 7)
	record(blockA, 8)
	history := sb.GetSuccessHistory()
	if len(history) != 2 || history[0].Round != 1 || history[1].Round != 2 {
		t.Fatalf("A streak history = %+v, want rounds 1 and 2", history)
	}
	if history[0].SeqID != 7 || history[1].SeqID != 8 {
		t.Fatalf("A streak seqIDs = %d/%d, want 7/8", history[0].SeqID, history[1].SeqID)
	}

	                                                                          
	                                                              
	record(blockB, 9)
	history = sb.GetSuccessHistory()
	if len(history) != 1 {
		t.Fatalf("winner switch retained old evidence: %+v", history)
	}
	if history[0].Round != 1 || history[0].SeqID != 9 ||
		len(history[0].Votes) != 1 || history[0].Votes[0].PreferredID != blockB {
		t.Fatalf("unexpected B streak evidence: %+v", history[0])
	}

	                                                                       
	                  
	sb.RecordVoteWithDetails(
		candidates,
		map[string]int{blockB: 3, blockA: 2},
		3,
		5,
		10,
		[]byte{10},
		nil,
	)
	if history := sb.GetSuccessHistory(); len(history) != 0 {
		t.Fatalf("soft-only poll did not clear strong-streak evidence: %+v", history)
	}
}

func TestSnowballDropFinalizedEvidenceReleasesVotePayloads(t *testing.T) {
	sb := NewSnowball(&stubEventBus{})
	blockA := blockID(10, "p1", 0, "aaa")
	candidates := []string{blockA}
	sb.AddCandidate(blockA)
	sb.RecordVoteWithDetails(
		candidates,
		map[string]int{blockA: 5},
		3,
		5,
		7,
		[]byte("seed"),
		[]types.FinalizationChit{{
			NodeID:      "n1",
			PreferredID: blockA,
			Signature:   []byte("sig"),
		}},
	)
	if len(sb.GetSuccessHistory()) != 1 || len(sb.GetLastVotes()) != 1 {
		t.Fatal("setup failed: expected retained vote evidence before finalization")
	}

	sb.DropFinalizedEvidence()
	if len(sb.GetSuccessHistory()) != 1 || len(sb.GetLastVotes()) != 1 {
		t.Fatal("DropFinalizedEvidence cleared evidence before finalization")
	}

	if id, ok := sb.FinalizationPreference(1); !ok || id != blockA {
		t.Fatalf("expected A to be finalizable: id=%q ok=%v", id, ok)
	}
	if !sb.Finalize(blockA) {
		t.Fatal("Finalize rejected eligible A")
	}
	sb.DropFinalizedEvidence()
	if !sb.IsFinalized() {
		t.Fatal("evidence drop changed finalized state")
	}
	if sb.GetPreference() != blockA || sb.GetConfidence() != 1 {
		t.Fatalf("evidence drop changed decision: preference=%s confidence=%d",
			sb.GetPreference(), sb.GetConfidence())
	}
	if history := sb.GetSuccessHistory(); len(history) != 0 {
		t.Fatalf("success history retained after finalized drop: %+v", history)
	}
	if votes := sb.GetLastVotes(); len(votes) != 0 {
		t.Fatalf("last votes retained after finalized drop: %+v", votes)
	}
}

func TestSnowballRecordVoteIgnoredAfterFinalize(t *testing.T) {
	sb := NewSnowball(&stubEventBus{})
	blockA := blockID(10, "p1", 0, "aaa")
	blockB := blockID(10, "p2", 0, "bbb")
	candidates := []string{blockA, blockB}
	sb.AddCandidates(candidates)

	sb.RecordVote(candidates, map[string]int{blockA: 5}, 3, 5)
	if !sb.Finalize(blockA) {
		t.Fatal("Finalize rejected A")
	}
	sb.DropFinalizedEvidence()
	sb.RecordVoteWithDetails(
		candidates,
		map[string]int{blockB: 5},
		3,
		5,
		8,
		[]byte("late-seed"),
		[]types.FinalizationChit{{
			NodeID:      "late",
			PreferredID: blockB,
			Signature:   []byte("late-sig"),
		}},
	)

	if history := sb.GetSuccessHistory(); len(history) != 0 {
		t.Fatalf("finalized Snowball retained late history: %+v", history)
	}
	if votes := sb.GetLastVotes(); len(votes) != 0 {
		t.Fatalf("finalized Snowball retained late votes: %+v", votes)
	}
	if sb.GetPreference() != blockA || sb.GetConfidence() != 1 {
		t.Fatalf("late vote changed finalized decision: preference=%s confidence=%d",
			sb.GetPreference(), sb.GetConfidence())
	}
}
