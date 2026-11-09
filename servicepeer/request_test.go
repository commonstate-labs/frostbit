package servicepeer

import (
	"errors"
	"testing"
)

func TestRequestAuthBindsRouteRecordAndPayload(t *testing.T) {
	signer := testSigner(t, 91)
	record := testRecord(t, 91, "8.8.4.4:19000", 7, 100, 200)
	payload := []byte("block-gossip-payload")
	auth, err := NewRequestAuth(record, "/service/gossip", payload, signer)
	if err != nil {
		t.Fatalf("NewRequestAuth: %v", err)
	}
	if err := VerifyRequestAuth(auth, record, testChainID, "/service/gossip", payload); err != nil {
		t.Fatalf("VerifyRequestAuth: %v", err)
	}

	if err := VerifyRequestAuth(auth, record, testChainID, "/service/syncblocks", payload); err == nil {
		t.Fatal("request auth was replayable on another route")
	}
	if err := VerifyRequestAuth(auth, record, testChainID, "/service/gossip", []byte("changed")); err == nil {
		t.Fatal("request auth accepted changed payload")
	}

	newer := testRecord(t, 91, "8.8.4.4:19000", 8, 100, 201)
	if err := VerifyRequestAuth(auth, newer, testChainID, "/service/gossip", payload); err == nil {
		t.Fatal("request auth accepted another record generation")
	}
}

func TestRequestAuthRejectsWrongSignerAndChain(t *testing.T) {
	record := testRecord(t, 92, "1.1.1.1:19000", 1, 1, 2)
	if _, err := NewRequestAuth(record, "/service/gossip", []byte("x"), testSigner(t, 93)); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("wrong signer error = %v, want identity mismatch", err)
	}

	auth, err := NewRequestAuth(record, "/service/gossip", []byte("x"), testSigner(t, 92))
	if err != nil {
		t.Fatalf("NewRequestAuth: %v", err)
	}
	if err := VerifyRequestAuth(auth, record, testChainID+1, "/service/gossip", []byte("x")); !errors.Is(err, ErrWrongChain) {
		t.Fatalf("wrong-chain error = %v", err)
	}
}
