package consensus

import (
	"bytes"
	"dex/types"
	"dex/utils"
	"testing"
)

func newAcceptedFrontierTestSigner(t *testing.T) *utils.KeyManager {
	t.Helper()
	signer := utils.NewKeyManager()
	if err := signer.InitKeyRandom(); err != nil {
		t.Fatalf("InitKeyRandom: %v", err)
	}
	RegisterSignerPublicKey(types.NodeID(signer.GetAddress()), signer)
	return signer
}

func TestAcceptedFrontierAllowsUnregisteredObserverRequester(t *testing.T) {
	requester := utils.NewKeyManager()
	if err := requester.InitKeyRandom(); err != nil {
		t.Fatalf("InitKeyRandom: %v", err)
	}
	query := AcceptedFrontierQuery{
		Requester:     types.NodeID(requester.GetAddress()),
		Nonce:         bytes.Repeat([]byte{0x71}, AcceptedFrontierNonceSize),
		AnchorHeight:  20,
		AnchorBlockID: "block-20",
		TargetHeight:  21,
	}
	req, err := NewSignedAcceptedFrontierRequest(query, requester)
	if err != nil {
		t.Fatalf("NewSignedAcceptedFrontierRequest: %v", err)
	}
	if _, registered := GetNodePublicKey(requester.GetAddress()); registered {
		t.Fatal("test observer unexpectedly entered the consensus public-key registry")
	}
	if _, err := VerifyAcceptedFrontierRequest(req); err != nil {
		t.Fatalf("self-describing observer request did not verify: %v", err)
	}
	if _, registered := GetNodePublicKey(requester.GetAddress()); registered {
		t.Fatal("observer request verification granted registry membership")
	}

	other := utils.NewKeyManager()
	if err := other.InitKeyRandom(); err != nil {
		t.Fatalf("other InitKeyRandom: %v", err)
	}
	req.RequesterSecpPublicKey = other.PublicKeyBytes()
	if _, err := VerifyAcceptedFrontierRequest(req); err == nil {
		t.Fatal("request with a public key unrelated to requester_id verified")
	}
}

func TestAcceptedFrontierSignedRoundTrip(t *testing.T) {
	requester := newAcceptedFrontierTestSigner(t)
	responder := newAcceptedFrontierTestSigner(t)
	query := AcceptedFrontierQuery{
		Requester:     types.NodeID(requester.GetAddress()),
		Nonce:         make([]byte, AcceptedFrontierNonceSize),
		AnchorHeight:  40,
		AnchorBlockID: "block-40",
		TargetHeight:  41,
	}
	query.Nonce[0] = 1

	req, err := NewSignedAcceptedFrontierRequest(query, requester)
	if err != nil {
		t.Fatalf("NewSignedAcceptedFrontierRequest: %v", err)
	}
	decodedQuery, err := VerifyAcceptedFrontierRequest(req)
	if err != nil {
		t.Fatalf("VerifyAcceptedFrontierRequest: %v", err)
	}
	if decodedQuery.AnchorBlockID != query.AnchorBlockID || decodedQuery.TargetHeight != query.TargetHeight {
		t.Fatalf("decoded query mismatch: %#v", decodedQuery)
	}

	vote := AcceptedFrontierVote{
		Peer:             types.NodeID(responder.GetAddress()),
		Status:           AcceptedFrontierStatusOK,
		AcceptedHeight:   43,
		ObservedAnchorID: query.AnchorBlockID,
		TargetHeight:     query.TargetHeight,
		TargetBlockID:    "block-41",
	}
	resp, err := NewSignedAcceptedFrontierResponse(query, vote, responder)
	if err != nil {
		t.Fatalf("NewSignedAcceptedFrontierResponse: %v", err)
	}
	decodedVote, err := VerifyAcceptedFrontierResponse(query, vote.Peer, resp)
	if err != nil {
		t.Fatalf("VerifyAcceptedFrontierResponse: %v", err)
	}
	if decodedVote.Status != AcceptedFrontierStatusOK || decodedVote.TargetBlockID != vote.TargetBlockID {
		t.Fatalf("decoded vote mismatch: %#v", decodedVote)
	}
	if !bytes.Equal(decodedVote.RequestDigest, resp.RequestDigest) {
		t.Fatal("decoded vote did not retain its signed request digest")
	}
}

func TestAcceptedFrontierRejectsTamperingAndReplay(t *testing.T) {
	requester := newAcceptedFrontierTestSigner(t)
	responder := newAcceptedFrontierTestSigner(t)
	query := AcceptedFrontierQuery{
		Requester:     types.NodeID(requester.GetAddress()),
		Nonce:         make([]byte, AcceptedFrontierNonceSize),
		AnchorHeight:  7,
		AnchorBlockID: "block-7",
		TargetHeight:  8,
	}
	query.Nonce[0] = 2
	req, err := NewSignedAcceptedFrontierRequest(query, requester)
	if err != nil {
		t.Fatal(err)
	}
	req.TargetHeight++
	if _, err := VerifyAcceptedFrontierRequest(req); err == nil {
		t.Fatal("tampered request unexpectedly verified")
	}

	vote := AcceptedFrontierVote{
		Peer:             types.NodeID(responder.GetAddress()),
		Status:           AcceptedFrontierStatusOK,
		AcceptedHeight:   8,
		ObservedAnchorID: query.AnchorBlockID,
		TargetHeight:     query.TargetHeight,
		TargetBlockID:    "block-8",
	}
	resp, err := NewSignedAcceptedFrontierResponse(query, vote, responder)
	if err != nil {
		t.Fatal(err)
	}
	resp.TargetBlockId = "other-block-8"
	if _, err := VerifyAcceptedFrontierResponse(query, vote.Peer, resp); err == nil {
		t.Fatal("tampered response unexpectedly verified")
	}

	resp, err = NewSignedAcceptedFrontierResponse(query, vote, responder)
	if err != nil {
		t.Fatal(err)
	}
	otherRound := query
	otherRound.Nonce = append([]byte(nil), query.Nonce...)
	otherRound.Nonce[1] = 9
	if _, err := VerifyAcceptedFrontierResponse(otherRound, vote.Peer, resp); err == nil {
		t.Fatal("response replayed into another nonce unexpectedly verified")
	}
}

func TestAcceptedFrontierStatusInvariants(t *testing.T) {
	signer := newAcceptedFrontierTestSigner(t)
	query := AcceptedFrontierQuery{
		Requester:     types.NodeID(signer.GetAddress()),
		Nonce:         make([]byte, AcceptedFrontierNonceSize),
		AnchorHeight:  10,
		AnchorBlockID: "block-10",
		TargetHeight:  11,
	}
	bad := AcceptedFrontierVote{
		Peer:             types.NodeID(signer.GetAddress()),
		Status:           AcceptedFrontierStatusOK,
		AcceptedHeight:   10,
		ObservedAnchorID: query.AnchorBlockID,
		TargetHeight:     query.TargetHeight,
		TargetBlockID:    "block-11",
	}
	if _, err := NewSignedAcceptedFrontierResponse(query, bad, signer); err == nil {
		t.Fatal("ok response below target unexpectedly accepted")
	}

	bad.Status = AcceptedFrontierStatusAnchorMismatch
	bad.AcceptedHeight = 11
	bad.ObservedAnchorID = query.AnchorBlockID
	bad.TargetBlockID = ""
	if _, err := NewSignedAcceptedFrontierResponse(query, bad, signer); err == nil {
		t.Fatal("anchor mismatch with the requested anchor unexpectedly accepted")
	}
}

func TestAcceptedFrontierRejectsOversizedSignatures(t *testing.T) {
	requester := newAcceptedFrontierTestSigner(t)
	responder := newAcceptedFrontierTestSigner(t)
	query := AcceptedFrontierQuery{
		Requester:     types.NodeID(requester.GetAddress()),
		Nonce:         make([]byte, AcceptedFrontierNonceSize),
		AnchorHeight:  10,
		AnchorBlockID: "block-10",
		TargetHeight:  11,
	}
	req, err := NewSignedAcceptedFrontierRequest(query, requester)
	if err != nil {
		t.Fatal(err)
	}
	req.Signature = make([]byte, acceptedFrontierMaxSignatureSize+1)
	if _, err := VerifyAcceptedFrontierRequest(req); err == nil {
		t.Fatal("oversized request signature unexpectedly verified")
	}

	vote := AcceptedFrontierVote{
		Peer:             types.NodeID(responder.GetAddress()),
		Status:           AcceptedFrontierStatusOK,
		AcceptedHeight:   query.TargetHeight,
		ObservedAnchorID: query.AnchorBlockID,
		TargetHeight:     query.TargetHeight,
		TargetBlockID:    "block-11",
	}
	resp, err := NewSignedAcceptedFrontierResponse(query, vote, responder)
	if err != nil {
		t.Fatal(err)
	}
	resp.Signature = make([]byte, acceptedFrontierMaxSignatureSize+1)
	if _, err := VerifyAcceptedFrontierResponse(query, vote.Peer, resp); err == nil {
		t.Fatal("oversized response signature unexpectedly verified")
	}
}
