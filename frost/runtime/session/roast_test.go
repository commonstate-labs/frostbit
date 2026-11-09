                                      
                           

package session

import (
	"errors"
	"testing"
	"time"
)

func createTestCommittee() []Participant {
	return []Participant{
		{ID: "node1", Index: 0, Address: "addr1", IP: "127.0.0.1:6001"},
		{ID: "node2", Index: 1, Address: "addr2", IP: "127.0.0.1:6002"},
		{ID: "node3", Index: 2, Address: "addr3", IP: "127.0.0.1:6003"},
	}
}

func TestSession_Start(t *testing.T) {
	committee := createTestCommittee()
	sess := NewSession(SessionParams{
		JobID:     "job1",
		Messages:  [][]byte{[]byte("msg")},
		Committee: committee,
		Threshold: 1,
		MyIndex:   0,
	})

	if err := sess.Start(); err != nil {
		t.Fatalf("Start should succeed: %v", err)
	}
	if sess.GetState() != SignSessionStateCollectingNonces {
		t.Errorf("State should be COLLECTING_NONCES, got %v", sess.GetState())
	}
	if len(sess.SelectedSetSnapshot()) != 1 {
		t.Errorf("Selected set should have 1 member")
	}
}

func TestSession_AddNonce(t *testing.T) {
	committee := createTestCommittee()
	sess := NewSession(SessionParams{
		JobID:     "job1",
		Messages:  [][]byte{[]byte("msg")},
		Committee: committee,
		Threshold: 1,
		MyIndex:   0,
	})
	_ = sess.Start()

	err := sess.AddNonce(0, [][]byte{[]byte("hiding1")}, [][]byte{[]byte("binding1")})
	if err != nil {
		t.Fatalf("AddNonce should succeed: %v", err)
	}
	if !sess.HasEnoughNonces() {
		t.Errorf("Should have enough nonces")
	}
}

func TestSession_OneOfOne(t *testing.T) {
	sess := NewSession(SessionParams{
		JobID:     "job1",
		Messages:  [][]byte{[]byte("msg")},
		Committee: []Participant{{ID: "node1", Index: 0}},
		Threshold: 1,
		MyIndex:   0,
	})
	if err := sess.Start(); err != nil {
		t.Fatalf("Start should succeed: %v", err)
	}
	if got := len(sess.SelectedSetSnapshot()); got != 1 {
		t.Fatalf("Selected set size = %d, want 1", got)
	}
	if err := sess.AddNonce(0, [][]byte{[]byte("hiding1")}, [][]byte{[]byte("binding1")}); err != nil {
		t.Fatalf("AddNonce should succeed: %v", err)
	}
	if !sess.HasEnoughNonces() {
		t.Fatalf("1-of-1 should have enough nonces after one nonce")
	}
	sess.SetState(SignSessionStateCollectingShares)
	if err := sess.AddShare(0, [][]byte{[]byte("share1")}); err != nil {
		t.Fatalf("AddShare should succeed: %v", err)
	}
	if !sess.HasEnoughShares() {
		t.Fatalf("1-of-1 should have enough shares after one share")
	}
}

func TestSession_AddShare(t *testing.T) {
	committee := createTestCommittee()
	sess := NewSession(SessionParams{
		JobID:     "job1",
		Messages:  [][]byte{[]byte("msg")},
		Committee: committee,
		Threshold: 1,
		MyIndex:   0,
	})
	_ = sess.Start()

	                                                                                   
	_ = sess.AddNonce(0, [][]byte{[]byte("h0")}, [][]byte{[]byte("b0")})
	_ = sess.AddNonce(1, [][]byte{[]byte("h1")}, [][]byte{[]byte("b1")})
	sess.SetState(SignSessionStateCollectingShares)

	err := sess.AddShare(0, [][]byte{[]byte("share1")})
	if err != nil {
		t.Fatalf("AddShare should succeed: %v", err)
	}
	if !sess.HasEnoughShares() {
		t.Errorf("Should have enough shares")
	}

	                                               
	err = sess.AddShare(2, [][]byte{[]byte("share3")})
	if err == nil {
		t.Fatalf("AddShare without nonce should fail")
	}
}

func TestSession_ResetForRetry(t *testing.T) {
	committee := createTestCommittee()
	sess := NewSession(SessionParams{
		JobID:     "job1",
		Messages:  [][]byte{[]byte("msg")},
		Committee: committee,
		Threshold: 1,
		MyIndex:   0,
	})
	sess.SelectInitialSet()
	initial := sess.SelectedSetSnapshot()

	if !sess.ResetForRetry(2) {
		t.Fatalf("First retry should succeed")
	}
	next := sess.SelectedSetSnapshot()
	if len(next) != len(initial) {
		t.Errorf("Selected set size should remain the same")
	}

	if !sess.ResetForRetry(2) {
		t.Fatalf("Second retry should succeed")
	}
	if sess.ResetForRetry(2) {
		t.Fatalf("Third retry should fail")
	}
}

func TestSession_RoundAwareAddsRejectMismatchesAtomically(t *testing.T) {
	sess := NewSession(SessionParams{
		JobID: "job-round-aware", Messages: [][]byte{[]byte("msg")},
		Committee: createTestCommittee(), Threshold: 1, MyIndex: 0,
	})
	if err := sess.Start(); err != nil {
		t.Fatalf("start session: %v", err)
	}

	if err := sess.AddNonceForRound(1, 0, [][]byte{[]byte("stale-h")}, [][]byte{[]byte("stale-b")}); !errors.Is(err, ErrRoundMismatch) {
		t.Fatalf("future nonce error=%v want ErrRoundMismatch", err)
	}
	if _, ok := sess.GetNonce(0); ok {
		t.Fatal("mismatched nonce mutated the session")
	}
	if err := sess.AddNonceForRound(0, 0, [][]byte{[]byte("h0")}, [][]byte{[]byte("b0")}); err != nil {
		t.Fatalf("current round 0 nonce: %v", err)
	}

	if !sess.ResetForRetry(2) {
		t.Fatal("advance to round 1")
	}
	if got := sess.CurrentRound(); got != 1 {
		t.Fatalf("current round=%d want=1", got)
	}
	if err := sess.AddNonceForRound(0, 0, [][]byte{[]byte("old-h")}, [][]byte{[]byte("old-b")}); !errors.Is(err, ErrRoundMismatch) {
		t.Fatalf("stale nonce error=%v want ErrRoundMismatch", err)
	}
	if _, ok := sess.GetNonce(0); ok {
		t.Fatal("stale nonce from round 0 mutated round 1")
	}
	if err := sess.AddNonceForRound(1, 0, [][]byte{[]byte("h1")}, [][]byte{[]byte("b1")}); err != nil {
		t.Fatalf("current round 1 nonce: %v", err)
	}

	sess.SetState(SignSessionStateCollectingShares)
	if err := sess.AddShareForRound(0, 0, [][]byte{[]byte("old-share")}); !errors.Is(err, ErrRoundMismatch) {
		t.Fatalf("stale share error=%v want ErrRoundMismatch", err)
	}
	if _, ok := sess.GetShare(0); ok {
		t.Fatal("stale share from round 0 mutated round 1")
	}
	if err := sess.AddShareForRound(1, 0, [][]byte{[]byte("share1")}); err != nil {
		t.Fatalf("current round 1 share: %v", err)
	}
	if share, ok := sess.GetShare(0); !ok || share.Round != 1 {
		t.Fatalf("current share missing or wrong round: ok=%v share=%+v", ok, share)
	}
}

                                       

func TestRecoveryManager_PersistAndRecover(t *testing.T) {
	storage := NewMemorySessionStorage()
	rm := NewRecoveryManager(storage, nil)

	committee := createTestCommittee()
	sess := NewSession(SessionParams{
		JobID:     "job1",
		Messages:  [][]byte{[]byte("test message")},
		Committee: committee,
		Threshold: 1,
		MyIndex:   0,
	})
	if err := sess.Start(); err != nil {
		t.Fatalf("Failed to start session: %v", err)
	}
	if !sess.ResetForRetry(3) {
		t.Fatal("Failed to advance session round before persistence")
	}

	if err := rm.PersistSession(sess); err != nil {
		t.Fatalf("Failed to persist session: %v", err)
	}

	count, err := rm.GetPendingCount()
	if err != nil {
		t.Fatalf("Failed to get pending count: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 pending session, got %d", count)
	}

	recovered, expired, err := rm.RecoverSessions()
	if err != nil {
		t.Fatalf("Failed to recover sessions: %v", err)
	}
	if len(expired) != 0 {
		t.Errorf("Expected no expired sessions, got %d", len(expired))
	}
	if len(recovered) != 1 {
		t.Fatalf("Expected 1 recovered session, got %d", len(recovered))
	}

	recoveredSession := recovered[0]
	if recoveredSession.JobID != "job1" {
		t.Errorf("Expected JobID 'job1', got '%s'", recoveredSession.JobID)
	}
	if string(recoveredSession.Messages[0]) != "test message" {
		t.Errorf("Expected message 'test message', got '%s'", string(recoveredSession.Messages[0]))
	}
	if recoveredSession.State != SignSessionStateInit {
		t.Errorf("Expected state INIT after recovery, got %s", recoveredSession.State)
	}
	if recoveredSession.CurrentRound() != 1 || recoveredSession.RetryCount != 1 {
		t.Errorf("Expected recovered round/retry 1/1, got %d/%d", recoveredSession.CurrentRound(), recoveredSession.RetryCount)
	}
}

func TestRecoveryManager_ExpiredSessions(t *testing.T) {
	storage := NewMemorySessionStorage()
	config := &RecoveryConfig{
		MaxRecoveryAge: 1 * time.Millisecond,
		RetryDelay:     1 * time.Second,
	}
	rm := NewRecoveryManager(storage, config)

	committee := createTestCommittee()
	sess := NewSession(SessionParams{
		JobID:     "job1",
		Messages:  [][]byte{[]byte("msg")},
		Committee: committee,
		Threshold: 1,
		MyIndex:   0,
	})
	if err := sess.Start(); err != nil {
		t.Fatalf("Failed to start session: %v", err)
	}
	if err := rm.PersistSession(sess); err != nil {
		t.Fatalf("Failed to persist session: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	recovered, expired, err := rm.RecoverSessions()
	if err != nil {
		t.Fatalf("Failed to recover sessions: %v", err)
	}
	if len(recovered) != 0 {
		t.Errorf("Expected no recovered sessions, got %d", len(recovered))
	}
	if len(expired) != 1 {
		t.Errorf("Expected 1 expired session, got %d", len(expired))
	}
	if expired[0] != "job1" {
		t.Errorf("Expected expired job 'job1', got '%s'", expired[0])
	}
}

func TestMemorySessionStorage(t *testing.T) {
	storage := NewMemorySessionStorage()

	session := &PersistedSession{
		JobID:    "job1",
		KeyEpoch: 1,
		State:    SignSessionStateCollectingNonces,
	}
	if err := storage.SaveSession(session); err != nil {
		t.Fatalf("Failed to save session: %v", err)
	}

	loaded, err := storage.LoadSession("job1")
	if err != nil {
		t.Fatalf("Failed to load session: %v", err)
	}
	if loaded.JobID != "job1" {
		t.Errorf("Expected JobID 'job1', got '%s'", loaded.JobID)
	}

	_, err = storage.LoadSession("nonexistent")
	if err != ErrSessionNotFound {
		t.Errorf("Expected ErrSessionNotFound, got %v", err)
	}

	if err := storage.DeleteSession("job1"); err != nil {
		t.Fatalf("Failed to delete session: %v", err)
	}
	_, err = storage.LoadSession("job1")
	if err != ErrSessionNotFound {
		t.Errorf("Expected ErrSessionNotFound after delete, got %v", err)
	}
}
