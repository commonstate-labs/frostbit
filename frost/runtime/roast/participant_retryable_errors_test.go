package roast

import (
	"bytes"
	"errors"
	"testing"

	runtimetypes "dex/frost/runtime/types"
	"dex/logs"
	"dex/pb"
)

type failingParticipantResponseMessenger struct {
	err error
}

func (m *failingParticipantResponseMessenger) Send(NodeID, *runtimetypes.RoastEnvelope) error {
	return m.err
}

func (m *failingParticipantResponseMessenger) Broadcast([]NodeID, *runtimetypes.RoastEnvelope) error {
	return nil
}

func TestParticipantResponseSendErrorsAreRetryable(t *testing.T) {
	participant := &Participant{messenger: &failingParticipantResponseMessenger{err: errors.New("control queue full")}}
	session := &ParticipantSession{
		JobID:         "job-send-error",
		Chain:         "btc",
		VaultID:       1,
		KeyEpoch:      2,
		SignAlgo:      pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		Round:         3,
		HidingPoints:  [][]byte{{0x01}},
		BindingPoints: [][]byte{{0x02}},
	}

	if err := participant.sendNonceCommitment(session, "coordinator", 3); !errors.Is(err, ErrRoastResponseSendFailed) {
		t.Fatalf("nonce commitment send error=%v, want ErrRoastResponseSendFailed", err)
	}
	if err := participant.sendSignatureShares(session, "coordinator", [][]byte{{0x03}}); !errors.Is(err, ErrRoastResponseSendFailed) {
		t.Fatalf("signature share send error=%v, want ErrRoastResponseSendFailed", err)
	}
}

func TestParticipantMissingCanonicalShareIsRetryable(t *testing.T) {
	participant := NewParticipant(
		"node-a",
		&recordingMessenger{},
		&fixedCommitteeVaultProvider{committee: []SignerInfo{{ID: "node-a", Index: 0, Weight: 1}}},
		&mockCryptoFactory{},
		nil,
		nil,
		logs.NewNodeLogger("participant-share-lag-test", 0),
	)
	envelope := &Envelope{
		SessionID: "job-share-lag",
		Kind:      "NonceRequest",
		From:      "node-a",
		Chain:     "btc",
		VaultID:   1,
		Epoch:     2,
		SignAlgo:  pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		Payload:   encodeRoastRequestPayload([][]byte{bytes.Repeat([]byte{0x22}, 32)}, nil),
	}

	if err := participant.HandleRoastNonceRequest(envelope); !errors.Is(err, ErrLocalShareNotReady) {
		t.Fatalf("missing share error=%v, want ErrLocalShareNotReady", err)
	}
}
