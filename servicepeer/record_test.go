package servicepeer

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"

	"dex/utils"
)

const testChainID uint64 = 4242

func testSigner(t *testing.T, scalar int) *utils.KeyManager {
	t.Helper()
	signer := utils.NewKeyManager()
	if err := signer.InitKey(fmt.Sprintf("%064x", scalar)); err != nil {
		t.Fatalf("InitKey(%d): %v", scalar, err)
	}
	return signer
}

func testRecord(t *testing.T, scalar int, endpoint string, sequence uint64, archiveFrom, archiveTo uint64) Record {
	t.Helper()
	record, err := NewSignedRecord(Record{
		ChainID:          testChainID,
		Endpoint:         endpoint,
		ArchiveFrom:      archiveFrom,
		ArchiveTo:        archiveTo,
		CheckpointHeight: archiveTo / 2,
		Sequence:         sequence,
	}, testSigner(t, scalar))
	if err != nil {
		t.Fatalf("NewSignedRecord: %v", err)
	}
	return record
}

func TestSignedRecordRoundTripAndTampering(t *testing.T) {
	signer := testSigner(t, 1)
	record, err := NewSignedRecord(Record{
		Version:          999,                                                          
		ChainID:          testChainID,
		NodeID:           "forged",
		SecpPublicKey:    []byte("forged"),
		Endpoint:         "8.8.8.8:19000",
		ArchiveFrom:      100,
		ArchiveTo:        200,
		CheckpointHeight: 150,
		Sequence:         7,
		Signature:        []byte("forged"),
	}, signer)
	if err != nil {
		t.Fatalf("NewSignedRecord: %v", err)
	}
	if record.NodeID != signer.GetAddress() || record.Version != ProtocolVersion {
		t.Fatalf("self-certified identity not populated: %#v", record)
	}
	if err := VerifyRecord(record, testChainID); err != nil {
		t.Fatalf("VerifyRecord: %v", err)
	}

	tampered := cloneRecord(record)
	tampered.ArchiveTo++
	if !errors.Is(VerifyRecord(tampered, testChainID), ErrInvalidSignature) {
		t.Fatal("tampered stored-block range unexpectedly verified")
	}
	tampered = cloneRecord(record)
	tampered.Endpoint = "1.1.1.1:19000"
	if !errors.Is(VerifyRecord(tampered, testChainID), ErrInvalidSignature) {
		t.Fatal("tampered endpoint unexpectedly verified")
	}
	if !errors.Is(VerifyRecord(record, testChainID+1), ErrWrongChain) {
		t.Fatal("cross-chain record unexpectedly verified")
	}

	other := testSigner(t, 2)
	tampered = cloneRecord(record)
	tampered.SecpPublicKey = other.PublicKeyBytes()
	if !errors.Is(VerifyRecord(tampered, testChainID), ErrIdentityMismatch) {
		t.Fatal("record with unrelated identity public key unexpectedly verified")
	}

	                                          
	pub0, sig0 := record.SecpPublicKey[0], record.Signature[0]
	tampered.SecpPublicKey[0] ^= 0xff
	tampered.Signature[0] ^= 0xff
	if record.SecpPublicKey[0] != pub0 || record.Signature[0] != sig0 {
		t.Fatal("record byte slices alias another copy")
	}
}

func TestPublicIPv4EndpointValidation(t *testing.T) {
	valid := []string{
		"8.8.8.8:1",
		"1.1.1.1:65535",
		"9.9.9.9:19000",
	}
	for _, endpoint := range valid {
		canonical, err := CanonicalPublicEndpoint(endpoint)
		if err != nil || canonical != endpoint {
			t.Errorf("valid endpoint %q: canonical=%q err=%v", endpoint, canonical, err)
		}
	}
	invalid := []string{
		"",
		" 8.8.8.8:19000",
		"https://8.8.8.8:19000",
		"dns.google:19000",
		"8.8.8.8",
		"8.8.8.8:0",
		"8.8.8.8:65536",
		"[2001:4860:4860::8888]:19000",
		"0.0.0.0:19000",
		"10.0.0.1:19000",
		"100.64.0.1:19000",
		"127.0.0.1:19000",
		"169.254.1.1:19000",
		"172.16.1.1:19000",
		"192.168.1.1:19000",
		"192.0.2.1:19000",
		"198.18.0.1:19000",
		"198.51.100.1:19000",
		"203.0.113.1:19000",
		"224.0.0.1:19000",
		"255.255.255.255:19000",
	}
	for _, endpoint := range invalid {
		if _, err := CanonicalPublicEndpoint(endpoint); !errors.Is(err, ErrInvalidEndpoint) {
			t.Errorf("invalid endpoint %q returned %v", endpoint, err)
		}
	}
}

func TestRecordRejectsInvalidRangesAndSequence(t *testing.T) {
	signer := testSigner(t, 3)
	base := Record{ChainID: testChainID, Endpoint: "8.8.4.4:19000", Sequence: 1}
	bad := base
	bad.ArchiveFrom = 2
	if _, err := NewSignedRecord(bad, signer); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("archive_from without archive_to: %v", err)
	}
	bad = base
	bad.ArchiveTo = 2
	if _, err := NewSignedRecord(bad, signer); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("non-empty stored-block range starting at genesis: %v", err)
	}
	bad = base
	bad.ArchiveFrom, bad.ArchiveTo = 3, 2
	if _, err := NewSignedRecord(bad, signer); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("reversed stored-block range: %v", err)
	}
	bad = base
	bad.Sequence = 0
	if _, err := NewSignedRecord(bad, signer); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("zero sequence: %v", err)
	}
}

func TestChallengeRoundTripTamperingAndBinding(t *testing.T) {
	challenger := testSigner(t, 10)
	responderSigner := testSigner(t, 11)
	responder := testRecord(t, 11, "1.1.1.1:19000", 9, 100, 200)
	nonce := bytes.Repeat([]byte{0x5a}, NonceSize)
	challenge, err := NewSignedChallenge(challenger, testChainID, responder, nonce, 137)
	if err != nil {
		t.Fatalf("NewSignedChallenge: %v", err)
	}
	if err := VerifyChallenge(challenge, testChainID, responder.NodeID); err != nil {
		t.Fatalf("VerifyChallenge: %v", err)
	}

	payload := sha256.Sum256([]byte("canonical block bytes"))
	response, err := NewSignedChallengeResponse(challenge, testChainID, responderSigner, true, payload[:])
	if err != nil {
		t.Fatalf("NewSignedChallengeResponse: %v", err)
	}
	if err := VerifyChallengeResponse(challenge, response, testChainID); err != nil {
		t.Fatalf("VerifyChallengeResponse: %v", err)
	}

	tampered := cloneChallengeResponse(response)
	tampered.RequestedHeight++
	if err := VerifyChallengeResponse(challenge, tampered, testChainID); err == nil {
		t.Fatal("tampered response unexpectedly verified")
	}
	tampered = cloneChallengeResponse(response)
	tampered.PayloadDigest[0] ^= 0xff
	if !errors.Is(VerifyChallengeResponse(challenge, tampered, testChainID), ErrInvalidSignature) {
		t.Fatal("tampered payload digest unexpectedly verified")
	}

	wrongResponder := testSigner(t, 12)
	if _, err := NewSignedChallengeResponse(challenge, testChainID, wrongResponder, true, payload[:]); err == nil {
		t.Fatal("unrelated responder signed directed challenge")
	}
	if _, err := NewSignedChallenge(challenger, testChainID, responder, nonce, 201); err == nil {
		t.Fatal("challenge outside advertised block range unexpectedly created")
	}
	if _, err := NewSignedChallengeResponse(challenge, testChainID, responderSigner, false, payload[:]); err == nil {
		t.Fatal("unavailable response with payload digest unexpectedly created")
	}
	unavailable, err := NewSignedChallengeResponse(challenge, testChainID, responderSigner, false, nil)
	if err != nil || VerifyChallengeResponse(challenge, unavailable, testChainID) != nil {
		t.Fatalf("valid unavailable response failed: responseErr=%v verifyErr=%v", err, VerifyChallengeResponse(challenge, unavailable, testChainID))
	}
	if !errors.Is(VerifyChallengeResponse(challenge, response, testChainID+1), ErrWrongChain) {
		t.Fatal("cross-chain response unexpectedly verified")
	}
}

func TestConnectivityChallengeSentinelDoesNotRequireArchive(t *testing.T) {
	challenger := testSigner(t, 13)
	responderSigner := testSigner(t, 14)
	responder := testRecord(t, 14, "8.8.4.4:19000", 1, 0, 0)
	nonce := bytes.Repeat([]byte{0x6b}, NonceSize)

	challenge, err := NewSignedChallenge(challenger, testChainID, responder, nonce, ConnectivityProbeHeight)
	if err != nil {
		t.Fatalf("connectivity-only challenge without a block range: %v", err)
	}
	if challenge.RequestedHeight != ConnectivityProbeHeight {
		t.Fatalf("connectivity sentinel changed to %d", challenge.RequestedHeight)
	}
	if err := VerifyChallenge(challenge, testChainID, responder.NodeID); err != nil {
		t.Fatalf("verify connectivity-only challenge: %v", err)
	}
	response, err := NewSignedChallengeResponse(challenge, testChainID, responderSigner, false, nil)
	if err != nil {
		t.Fatalf("sign connectivity-only response: %v", err)
	}
	if err := VerifyChallengeResponse(challenge, response, testChainID); err != nil {
		t.Fatalf("verify connectivity-only response: %v", err)
	}
	payload := sha256.Sum256([]byte("height zero is not a block payload"))
	if _, err := NewSignedChallengeResponse(challenge, testChainID, responderSigner, true, payload[:]); err == nil {
		t.Fatal("connectivity-only response accepted a block payload")
	}
	if _, err := NewSignedChallenge(challenger, testChainID, responder, nonce, 1); err == nil {
		t.Fatal("non-zero challenge bypassed missing block coverage")
	}

	archived := testRecord(t, 15, "8.8.8.8:19000", 1, 100, 200)
	for _, height := range []uint64{99, 201} {
		if _, err := NewSignedChallenge(challenger, testChainID, archived, nonce, height); err == nil {
			t.Fatalf("out-of-range non-zero height %d accepted", height)
		}
	}
	for _, height := range []uint64{100, 200} {
		if _, err := NewSignedChallenge(challenger, testChainID, archived, nonce, height); err != nil {
			t.Fatalf("covered boundary height %d rejected: %v", height, err)
		}
	}
}

func TestNonceGeneration(t *testing.T) {
	input := bytes.Repeat([]byte{0x42}, NonceSize)
	nonce, err := NewNonceFrom(bytes.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(nonce, input) {
		t.Fatalf("nonce mismatch: %x", nonce)
	}
	if _, err := NewNonceFrom(bytes.NewReader(input[:NonceSize-1])); err == nil {
		t.Fatal("short random reader unexpectedly succeeded")
	}
}
