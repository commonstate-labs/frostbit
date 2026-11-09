package consensus

import (
	"dex/utils"
	"testing"
)

func TestComputeVRFSeedDeterministicAndBoundToInputs(t *testing.T) {
	a := computeVRFSeed("block-123", 42, 0, "node-05")
	b := computeVRFSeed("block-123", 42, 0, "node-05")
	c := computeVRFSeed("block-123", 42, 0, "node-06")
	if len(a) != 32 || string(a) != string(b) || string(a) == string(c) {
		t.Fatal("VRF seed is not deterministic or not bound to its inputs")
	}
}

func TestPollSampleSeedBindsPrivateQueryNonce(t *testing.T) {
	vrfSeed := computeVRFSeed("block-123", 42, 0, "node-05")
	a := computePollSampleSeed(vrfSeed, 7, queryManagerTestNonce(1))
	b := computePollSampleSeed(vrfSeed, 7, queryManagerTestNonce(1))
	c := computePollSampleSeed(vrfSeed, 7, queryManagerTestNonce(2))
	if len(a) != 32 || string(a) != string(b) {
		t.Fatal("poll sample seed is not deterministic")
	}
	if string(a) == string(c) {
		t.Fatal("poll sample seed did not bind query nonce")
	}
}

func TestECDSASignerSignAndVerify(t *testing.T) {
	ClearNodePublicKeys()
	defer ClearNodePublicKeys()
	signer := newTestKeyManager(t)
	RegisterSignerPublicKey("test-node", signer)
	digest := ComputeChitDigest(ChitDigestContext{
		RequestID: 1, Requester: "requester", Responder: "test-node",
		PreferredBlock: "block-42", QueryHeight: 42, VRFSeed: []byte("seed"), SeqID: 7,
		QueryNonce: queryManagerTestNonce(1),
	})
	signature, err := signer.Sign(digest)
	if err != nil {
		t.Fatal(err)
	}
	if len(signature) != 64 || !VerifyChitSignature("test-node", digest, signature) {
		t.Fatal("valid ECDSA signature did not verify")
	}
}

func TestECDSASignerRejectsForgery(t *testing.T) {
	ClearNodePublicKeys()
	defer ClearNodePublicKeys()
	signer := newTestKeyManager(t)
	RegisterSignerPublicKey("honest-node", signer)
	digest := ComputeChitDigest(ChitDigestContext{
		RequestID: 1, Requester: "requester", Responder: "honest-node",
		PreferredBlock: "block-42", QueryHeight: 42, VRFSeed: []byte("seed"), SeqID: 7,
		QueryNonce: queryManagerTestNonce(2),
	})
	if VerifyChitSignature("honest-node", digest, make([]byte, 64)) {
		t.Fatal("zero signature was accepted")
	}
	other := newTestKeyManager(t)
	otherSignature, _ := other.Sign(digest)
	if VerifyChitSignature("honest-node", digest, otherSignature) {
		t.Fatal("signature from another key was accepted")
	}
}

func newTestKeyManager(t *testing.T) *utils.KeyManager {
	t.Helper()
	manager := utils.NewKeyManager()
	if err := manager.InitKeyRandom(); err != nil {
		t.Fatalf("InitKeyRandom: %v", err)
	}
	return manager
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	result := ""
	for value > 0 {
		result = string(rune('0'+value%10)) + result
		value /= 10
	}
	return result
}
