package session

import "testing"

func TestDKGSession_OneOfOneThreshold(t *testing.T) {
	sess := NewDKGSession(DKGSessionParams{
		SessionID: "dkg1",
		Chain:     "btc",
		VaultID:   0,
		EpochID:   1,
		Committee: []string{"node1"},
		Threshold: 1,
		MyIndex:   1,
		MyAddress: "node1",
	})
	if err := sess.Start(); err != nil {
		t.Fatalf("Start should succeed: %v", err)
	}
	if err := sess.AddCommitment(1, "node1", [][]byte{[]byte("point")}, []byte("ai0")); err != nil {
		t.Fatalf("AddCommitment should succeed: %v", err)
	}
	if !sess.HasEnoughCommitments() {
		t.Fatalf("1-of-1 should have enough commitments after one commitment")
	}
	if err := sess.TransitionToSharing(); err != nil {
		t.Fatalf("TransitionToSharing should succeed: %v", err)
	}
	if err := sess.AddShare(1, "node1", []byte("share")); err != nil {
		t.Fatalf("AddShare should succeed: %v", err)
	}
	if !sess.HasEnoughShares() {
		t.Fatalf("1-of-1 should have enough shares after one share")
	}
}
