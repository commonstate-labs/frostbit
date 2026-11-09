package roast

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"errors"
	"testing"

	"dex/frost/security"
	"dex/pb"
)

func TestRoastEnvelopeWireRoundTrip(t *testing.T) {
	msg := &Envelope{
		SessionID: "job-1",
		Kind:      "NonceCommit",
		From:      NodeID("node-1"),
		Chain:     "btc",
		VaultID:   10,
		SignAlgo:  pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		Epoch:     7,
		Round:     1,
		Payload:   []byte{1, 2, 3},
	}

	env, err := PBEnvelopeFromRoast(msg)
	if err != nil {
		t.Fatalf("PBEnvelopeFromRoast failed: %v", err)
	}
	if env.Kind != pb.FrostEnvelopeKind_FROST_ENVELOPE_KIND_ROAST_RESPONSE {
		t.Fatalf("unexpected envelope kind: %v", env.Kind)
	}

	decoded, err := EnvelopeFromPB(env)
	if err != nil {
		t.Fatalf("EnvelopeFromPB failed: %v", err)
	}
	if decoded.SessionID != msg.SessionID || decoded.Kind != msg.Kind || decoded.From != msg.From {
		t.Fatalf("decoded header mismatch: got %+v want %+v", decoded, msg)
	}
	if decoded.Chain != msg.Chain || decoded.VaultID != msg.VaultID || decoded.SignAlgo != msg.SignAlgo {
		t.Fatalf("decoded routing mismatch: got %+v want %+v", decoded, msg)
	}
	if decoded.Epoch != msg.Epoch || decoded.Round != msg.Round {
		t.Fatalf("decoded epoch/round mismatch: got %+v want %+v", decoded, msg)
	}
	if !bytes.Equal(decoded.Payload, msg.Payload) {
		t.Fatalf("decoded payload mismatch: got %x want %x", decoded.Payload, msg.Payload)
	}
}

func TestEnvelopeFromPBEmptyPayload(t *testing.T) {
	_, err := EnvelopeFromPB(&pb.FrostEnvelope{})
	if !errors.Is(err, ErrEmptyRoastPayload) {
		t.Fatalf("expected ErrEmptyRoastPayload, got %v", err)
	}
}

func TestPBEnvelopeFromRoastInvalidKind(t *testing.T) {
	_, err := PBEnvelopeFromRoast(&Envelope{
		SessionID: "job-1",
		Kind:      "UnknownKind",
	})
	if !errors.Is(err, ErrInvalidRoastKind) {
		t.Fatalf("expected ErrInvalidRoastKind, got %v", err)
	}
}

func TestEnvelopeFromPBFillsEmptyInnerRoutingFields(t *testing.T) {
	payload, err := json.Marshal(&Envelope{})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	decoded, err := EnvelopeFromPB(&pb.FrostEnvelope{
		From:    "node-a",
		JobId:   "job-1",
		Kind:    pb.FrostEnvelopeKind_FROST_ENVELOPE_KIND_ROAST_REQUEST,
		Payload: payload,
	})
	if err != nil {
		t.Fatalf("EnvelopeFromPB failed: %v", err)
	}
	if decoded.From != NodeID("node-a") {
		t.Fatalf("decoded From = %q, want node-a", decoded.From)
	}
	if decoded.SessionID != "job-1" {
		t.Fatalf("decoded SessionID = %q, want job-1", decoded.SessionID)
	}
	if decoded.Kind != "NonceRequest" {
		t.Fatalf("decoded Kind = %q, want NonceRequest", decoded.Kind)
	}
}

func TestEnvelopeFromPBRejectsInnerOuterRoutingMismatch(t *testing.T) {
	base := Envelope{
		SessionID: "job-1",
		From:      NodeID("node-a"),
		Kind:      "NonceRequest",
	}

	tests := []struct {
		name    string
		mutate  func(*pb.FrostEnvelope)
		wantErr error
	}{
		{
			name: "session",
			mutate: func(env *pb.FrostEnvelope) {
				env.JobId = "job-2"
			},
			wantErr: ErrRoastSessionMismatch,
		},
		{
			name: "from",
			mutate: func(env *pb.FrostEnvelope) {
				env.From = "node-b"
			},
			wantErr: ErrRoastFromMismatch,
		},
		{
			name: "kind",
			mutate: func(env *pb.FrostEnvelope) {
				env.Kind = pb.FrostEnvelopeKind_FROST_ENVELOPE_KIND_ROAST_RESPONSE
			},
			wantErr: ErrRoastKindMismatch,
		},
		{
			name: "unspecified outer kind",
			mutate: func(env *pb.FrostEnvelope) {
				env.Kind = pb.FrostEnvelopeKind_FROST_ENVELOPE_KIND_UNSPECIFIED
			},
			wantErr: ErrRoastKindMismatch,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			payload, err := json.Marshal(&base)
			if err != nil {
				t.Fatalf("marshal payload: %v", err)
			}
			env := &pb.FrostEnvelope{
				From:    string(base.From),
				JobId:   base.SessionID,
				Kind:    pb.FrostEnvelopeKind_FROST_ENVELOPE_KIND_ROAST_REQUEST,
				Payload: payload,
			}
			tc.mutate(env)

			_, err = EnvelopeFromPB(env)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("EnvelopeFromPB error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestSignedRoastEnvelopeBindsInnerContextFields(t *testing.T) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}

	base := &Envelope{
		SessionID: "job-context-binding",
		Kind:      "NonceRequest",
		From:      NodeID("node-a"),
		Chain:     "btc",
		VaultID:   7,
		SignAlgo:  pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		Epoch:     3,
		Round:     1,
		Payload:   []byte{0x01, 0x02, 0x03},
		Tweaks:    [][]byte{{0x04, 0x05}},
	}
	signed, err := PBEnvelopeFromRoast(base)
	if err != nil {
		t.Fatalf("encode baseline envelope: %v", err)
	}
	signed.To = "node-b"
	signed.Seq = 9
	if err := security.SignFrostEnvelope(privateKey, signed); err != nil {
		t.Fatalf("sign baseline envelope: %v", err)
	}
	if !security.VerifyFrostEnvelope(&privateKey.PublicKey, signed) {
		t.Fatal("baseline envelope signature did not verify")
	}

	mutations := []struct {
		name   string
		mutate func(*Envelope)
	}{
		{name: "session_id", mutate: func(msg *Envelope) { msg.SessionID += "-other" }},
		{name: "kind", mutate: func(msg *Envelope) { msg.Kind = "SignRequest" }},
		{name: "from", mutate: func(msg *Envelope) { msg.From = "node-c" }},
		{name: "chain", mutate: func(msg *Envelope) { msg.Chain = "eth" }},
		{name: "vault_id", mutate: func(msg *Envelope) { msg.VaultID++ }},
		{name: "sign_algo", mutate: func(msg *Envelope) { msg.SignAlgo = pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128 }},
		{name: "epoch", mutate: func(msg *Envelope) { msg.Epoch++ }},
		{name: "round", mutate: func(msg *Envelope) { msg.Round++ }},
		{name: "payload", mutate: func(msg *Envelope) { msg.Payload[0] ^= 0xff }},
		{name: "tweaks", mutate: func(msg *Envelope) { msg.Tweaks[0][0] ^= 0xff }},
	}

	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			mutated := *base
			mutated.Payload = cloneBytes(base.Payload)
			mutated.Tweaks = cloneMessages(base.Tweaks)
			tc.mutate(&mutated)

			candidate, err := PBEnvelopeFromRoast(&mutated)
			if err != nil {
				t.Fatalf("encode mutated envelope: %v", err)
			}
			                                                                   
			                                                               
			candidate.From = signed.From
			candidate.To = signed.To
			candidate.Kind = signed.Kind
			candidate.JobId = signed.JobId
			candidate.Seq = signed.Seq
			candidate.Sig = cloneBytes(signed.Sig)

			if security.VerifyFrostEnvelope(&privateKey.PublicKey, candidate) {
				t.Fatal("signature accepted a mutated inner envelope field")
			}
		})
	}
}
