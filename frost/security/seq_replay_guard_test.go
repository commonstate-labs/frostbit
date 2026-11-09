package security

import (
	"crypto/sha256"
	"sync"
	"testing"
)

func replayDigest(label string) [sha256.Size]byte {
	return sha256.Sum256([]byte(label))
}

func TestSeqReplayGuardAcceptsForwardJumpsAndWindowReordering(t *testing.T) {
	guard := NewSeqReplayGuard(4)
	const sender = "sender->receiver"

	if valid, replay := guard.Check(sender, 100); !valid || replay {
		t.Fatalf("first high sequence rejected: valid=%v replay=%v", valid, replay)
	}
	if valid, replay := guard.Check(sender, 105); !valid || replay {
		t.Fatalf("forward jump rejected: valid=%v replay=%v", valid, replay)
	}
	if valid, replay := guard.Check(sender, 104); !valid || replay {
		t.Fatalf("in-window reordering rejected: valid=%v replay=%v", valid, replay)
	}
	if valid, replay := guard.Check(sender, 104); valid || !replay {
		t.Fatalf("duplicate accepted: valid=%v replay=%v", valid, replay)
	}
	if valid, replay := guard.Check(sender, 101); valid || !replay {
		t.Fatalf("out-of-window sequence accepted: valid=%v replay=%v", valid, replay)
	}
	if got := guard.GetMaxSeq(sender); got != 105 {
		t.Fatalf("max sequence = %d, want 105", got)
	}
}

func TestSeqReplayGuardTracksSendersIndependently(t *testing.T) {
	guard := NewSeqReplayGuard(8)

	if valid, _ := guard.Check("sender->receiver-1", 9_000); !valid {
		t.Fatal("receiver-1 first sequence rejected")
	}
	if valid, _ := guard.Check("sender->receiver-2", 1); !valid {
		t.Fatal("receiver-2 sequence was affected by receiver-1")
	}
	if valid, replay := guard.Check("sender->receiver-2", 1); valid || !replay {
		t.Fatalf("receiver-2 duplicate accepted: valid=%v replay=%v", valid, replay)
	}
}

func TestSeqReplayGuardStrictMode(t *testing.T) {
	guard := NewSeqReplayGuard(0)

	if valid, _ := guard.Check("sender", 2); !valid {
		t.Fatal("first sequence rejected")
	}
	if valid, replay := guard.Check("sender", 1); valid || !replay {
		t.Fatalf("older sequence accepted in strict mode: valid=%v replay=%v", valid, replay)
	}
	if valid, _ := guard.Check("sender", 3); !valid {
		t.Fatal("newer sequence rejected in strict mode")
	}
}

func TestSeqReplayGuardReservationReleaseAllowsRetryAndCommit(t *testing.T) {
	guard := NewSeqReplayGuard(8)
	digest := replayDigest("request")
	conflictingDigest := replayDigest("different request")

	first, decision, previousMax := guard.Reserve("sender", 7, digest)
	if decision != SeqReplayAccepted || first == nil || previousMax != 0 {
		t.Fatalf("first reserve = (%v, %v, %d), want accepted token at max 0", first, decision, previousMax)
	}
	if got := guard.GetMaxSeq("sender"); got != 0 {
		t.Fatalf("reservation consumed sequence before success: max=%d", got)
	}
	if token, decision, _ := guard.Reserve("sender", 7, digest); token != nil || decision != SeqReplayInFlight {
		t.Fatalf("same in-flight request = (%v, %v), want in-flight", token, decision)
	}
	if token, decision, _ := guard.Reserve("sender", 7, conflictingDigest); token != nil || decision != SeqReplayConflict {
		t.Fatalf("different payload at in-flight seq = (%v, %v), want conflict", token, decision)
	}

	if !guard.Release(first) {
		t.Fatal("failed request reservation was not released")
	}
	second, decision, _ := guard.Reserve("sender", 7, digest)
	if decision != SeqReplayAccepted || second == nil {
		t.Fatalf("retry reserve = (%v, %v), want accepted", second, decision)
	}
	if guard.Release(first) {
		t.Fatal("stale release removed a newer reservation generation")
	}
	if token, decision, _ := guard.Reserve("sender", 7, digest); token != nil || decision != SeqReplayInFlight {
		t.Fatalf("new generation disappeared after stale release: token=%v decision=%v", token, decision)
	}

	if !guard.Commit(second) {
		t.Fatal("successful request reservation was not committed")
	}
	if got := guard.GetMaxSeq("sender"); got != 7 {
		t.Fatalf("committed max=%d, want 7", got)
	}
	if token, decision, _ := guard.Reserve("sender", 7, digest); token != nil || decision != SeqReplayAlreadyCommitted {
		t.Fatalf("successful duplicate = (%v, %v), want already committed", token, decision)
	}
	if token, decision, _ := guard.Reserve("sender", 7, conflictingDigest); token != nil || decision != SeqReplayConflict {
		t.Fatalf("different payload at committed seq = (%v, %v), want conflict", token, decision)
	}
}

func TestSeqReplayGuardConcurrentDuplicateHasSingleReservation(t *testing.T) {
	guard := NewSeqReplayGuard(16)
	digest := replayDigest("concurrent request")
	const workers = 64

	type result struct {
		reservation *SeqReplayReservation
		decision    SeqReplayDecision
	}
	results := make(chan result, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reservation, decision, _ := guard.Reserve("sender", 11, digest)
			results <- result{reservation: reservation, decision: decision}
		}()
	}
	wg.Wait()
	close(results)

	accepted := 0
	inFlight := 0
	var acceptedReservation *SeqReplayReservation
	for result := range results {
		switch result.decision {
		case SeqReplayAccepted:
			accepted++
			acceptedReservation = result.reservation
		case SeqReplayInFlight:
			inFlight++
		default:
			t.Fatalf("unexpected concurrent decision %v", result.decision)
		}
	}
	if accepted != 1 || inFlight != workers-1 {
		t.Fatalf("accepted=%d inFlight=%d, want 1/%d", accepted, inFlight, workers-1)
	}
	if !guard.Commit(acceptedReservation) {
		t.Fatal("failed to commit the sole accepted reservation")
	}
}

func TestSeqReplayGuardFailedForwardJumpDoesNotPoisonWindow(t *testing.T) {
	guard := NewSeqReplayGuard(4)
	base := replayDigest("base")
	baseReservation, decision, _ := guard.Reserve("sender", 100, base)
	if decision != SeqReplayAccepted || !guard.Commit(baseReservation) {
		t.Fatal("failed to establish committed base sequence")
	}

	jumpReservation, decision, _ := guard.Reserve("sender", 10_000, replayDigest("failed jump"))
	if decision != SeqReplayAccepted {
		t.Fatalf("forward jump decision=%v, want accepted", decision)
	}
	if got := guard.GetMaxSeq("sender"); got != 100 {
		t.Fatalf("pending forward jump poisoned max: got %d want 100", got)
	}
	if !guard.Release(jumpReservation) {
		t.Fatal("failed to release forward jump")
	}

	next, decision, _ := guard.Reserve("sender", 101, replayDigest("next"))
	if decision != SeqReplayAccepted || !guard.Commit(next) {
		t.Fatalf("normal sequence after failed jump was not commit-able: decision=%v", decision)
	}
}

func TestSeqReplayGuardCommitsReservedSequencesOutOfOrder(t *testing.T) {
	guard := NewSeqReplayGuard(8)
	olderDigest := replayDigest("older")
	newerDigest := replayDigest("newer")
	older, olderDecision, _ := guard.Reserve("sender", 104, olderDigest)
	newer, newerDecision, _ := guard.Reserve("sender", 105, newerDigest)
	if olderDecision != SeqReplayAccepted || newerDecision != SeqReplayAccepted {
		t.Fatalf("reserve decisions older=%v newer=%v", olderDecision, newerDecision)
	}
	if !guard.Commit(newer) || !guard.Commit(older) {
		t.Fatal("out-of-order commits failed")
	}
	if got := guard.GetMaxSeq("sender"); got != 105 {
		t.Fatalf("max after out-of-order commit=%d, want 105", got)
	}
	if _, decision, _ := guard.Reserve("sender", 104, olderDigest); decision != SeqReplayAlreadyCommitted {
		t.Fatalf("older committed sequence decision=%v, want already committed", decision)
	}
	if _, decision, _ := guard.Reserve("sender", 105, newerDigest); decision != SeqReplayAlreadyCommitted {
		t.Fatalf("newer committed sequence decision=%v, want already committed", decision)
	}
}

func TestSeqReplayGuardBoundsPendingReservationsPerSender(t *testing.T) {
	guard := NewSeqReplayGuard(4)
	reservations := make([]*SeqReplayReservation, 0, 4)
	for seq := uint64(1); seq <= 4; seq++ {
		reservation, decision, _ := guard.Reserve("sender", seq, replayDigest(string(rune('a'+seq))))
		if decision != SeqReplayAccepted || reservation == nil {
			t.Fatalf("reserve seq=%d decision=%v reservation=%v", seq, decision, reservation)
		}
		reservations = append(reservations, reservation)
	}

	if reservation, decision, _ := guard.Reserve("sender", 5, replayDigest("overflow")); reservation != nil || decision != SeqReplayBusy {
		t.Fatalf("overflow reserve=(%v,%v), want busy", reservation, decision)
	}
	                                                                       
	if reservation, decision, _ := guard.Reserve("sender", 1, replayDigest(string(rune('a'+1)))); reservation != nil || decision != SeqReplayInFlight {
		t.Fatalf("duplicate at capacity=(%v,%v), want in-flight", reservation, decision)
	}

	if !guard.Release(reservations[0]) {
		t.Fatal("release at capacity failed")
	}
	if reservation, decision, _ := guard.Reserve("sender", 5, replayDigest("overflow")); decision != SeqReplayAccepted || reservation == nil {
		t.Fatalf("reserve after release=(%v,%v), want accepted", reservation, decision)
	}
}

func TestSeqReplayGuardPermanentRejectionIsConsumedWithoutBecomingReplayable(t *testing.T) {
	guard := NewSeqReplayGuard(8)
	digest := replayDigest("permanently invalid")
	reservation, decision, _ := guard.Reserve("sender", 3, digest)
	if decision != SeqReplayAccepted || reservation == nil {
		t.Fatalf("reserve=(%v,%v), want accepted", reservation, decision)
	}
	if !guard.Reject(reservation) {
		t.Fatal("permanent rejection was not committed")
	}
	if reservation, decision, _ := guard.Reserve("sender", 3, digest); reservation != nil || decision != SeqReplayPermanentlyRejected {
		t.Fatalf("exact rejected duplicate=(%v,%v), want permanently rejected", reservation, decision)
	}
	if reservation, decision, _ := guard.Reserve("sender", 3, replayDigest("conflict")); reservation != nil || decision != SeqReplayConflict {
		t.Fatalf("different payload at rejected sequence=(%v,%v), want conflict", reservation, decision)
	}
}

func TestSeqReplayGuardStrictModeSerializesReservations(t *testing.T) {
	guard := NewSeqReplayGuard(0)
	first, decision, _ := guard.Reserve("sender", 2, replayDigest("two"))
	if decision != SeqReplayAccepted || first == nil {
		t.Fatalf("first strict reservation=(%v,%v), want accepted", first, decision)
	}
	if reservation, decision, _ := guard.Reserve("sender", 3, replayDigest("three")); reservation != nil || decision != SeqReplayBusy {
		t.Fatalf("concurrent later reservation=(%v,%v), want busy", reservation, decision)
	}
	if !guard.Commit(first) {
		t.Fatal("commit first strict reservation failed")
	}
	second, decision, previousMax := guard.Reserve("sender", 3, replayDigest("three"))
	if decision != SeqReplayAccepted || second == nil || previousMax != 2 {
		t.Fatalf("next strict reservation=(%v,%v,max=%d), want accepted after max 2", second, decision, previousMax)
	}
}
