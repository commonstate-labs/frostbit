package roast

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"dex/logs"
	"dex/pb"
)

func TestParticipantSigningValidatorRejectsBeforeNonceGeneration(t *testing.T) {
	messenger := &recordingMessenger{}
	participant := NewParticipant(
		"node-a",
		messenger,
		&fixedCommitteeVaultProvider{committee: []SignerInfo{{ID: "node-a", Index: 0, Weight: 1}}},
		&mockCryptoFactory{},
		nil,
		nil,
		logs.NewNodeLogger("participant-validator-test", 0),
	)
	participant.SetShare("btc", 1, 1, bytes.Repeat([]byte{0x11}, 32))

	wantErr := errors.New("rejected signing intent")
	validatorCalls := 0
	participant.SetSigningRequestValidator(func(*Envelope, [][]byte) error {
		validatorCalls++
		return wantErr
	})

	env := &Envelope{
		SessionID: "job-rejected-before-nonce",
		Kind:      "NonceRequest",
		From:      "node-a",
		Chain:     "btc",
		VaultID:   1,
		SignAlgo:  pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		Epoch:     1,
		Payload:   encodeRoastRequestPayload([][]byte{bytes.Repeat([]byte{0x22}, 32)}, nil),
	}

	err := participant.HandleRoastNonceRequest(env)
	if !errors.Is(err, wantErr) {
		t.Fatalf("unexpected validator error: got=%v want=%v", err, wantErr)
	}
	if validatorCalls != 1 {
		t.Fatalf("validator call count mismatch: got=%d want=1", validatorCalls)
	}
	if participant.GetSession(env.SessionID) != nil {
		t.Fatal("validator rejection must not create participant session")
	}
	if got := messenger.sendCount("NonceCommit"); got != 0 {
		t.Fatalf("validator rejection sent nonce commitment: got=%d", got)
	}
}

func TestParticipantRejectsMessageReplacementInSignRound(t *testing.T) {
	participant := NewParticipant(
		"node-a",
		&recordingMessenger{},
		&fixedCommitteeVaultProvider{committee: []SignerInfo{{ID: "node-a", Index: 0, Weight: 1}}},
		&mockCryptoFactory{},
		nil,
		nil,
		logs.NewNodeLogger("participant-message-binding-test", 0),
	)
	const jobID = "job-message-binding"
	participant.sessions[jobID] = &ParticipantSession{
		JobID: jobID, Chain: "btc", VaultID: 1, KeyEpoch: 1,
		SignAlgo: pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		Messages: [][]byte{bytes.Repeat([]byte{0x22}, 32)}, State: ParticipantStateNonceGenerated,
	}
	env := &Envelope{
		SessionID: jobID, Kind: "SignRequest", From: "node-a", Chain: "btc", VaultID: 1, Epoch: 1,
		SignAlgo: pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		Payload:  encodeRoastRequestPayload([][]byte{bytes.Repeat([]byte{0x33}, 32)}, []byte{0x01}),
	}
	if err := participant.HandleRoastSignRequest(env); err == nil {
		t.Fatal("sign round replaced the nonce-bound messages")
	}
}

func TestParticipantRejectsNonCurrentCoordinator(t *testing.T) {
	committee := []SignerInfo{
		{ID: "node-a", Index: 0, Weight: 1},
		{ID: "node-b", Index: 1, Weight: 1},
	}
	const (
		jobID = "job-wrong-coordinator"
		chain = "btc"
		vault = uint32(1)
		epoch = uint64(1)
	)

	newParticipant := func(messenger *recordingMessenger) *Participant {
		return NewParticipant(
			"node-a",
			messenger,
			&fixedCommitteeVaultProvider{committee: committee},
			&mockCryptoFactory{},
			nil,
			nil,
			logs.NewNodeLogger("participant-coordinator-binding-test", 0),
		)
	}
	wrongCoordinator := func(participant *Participant) NodeID {
		current := participant.computeCoordinatorIndex(jobID, epoch, committee, chain)
		return committee[(current+1)%len(committee)].ID
	}

	t.Run("nonce request", func(t *testing.T) {
		messenger := &recordingMessenger{}
		participant := newParticipant(messenger)
		participant.SetShare(chain, vault, epoch, bytes.Repeat([]byte{0x11}, 32))

		env := &Envelope{
			SessionID: jobID,
			Kind:      "NonceRequest",
			From:      wrongCoordinator(participant),
			Chain:     chain,
			VaultID:   vault,
			SignAlgo:  pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
			Epoch:     epoch,
			Payload:   encodeRoastRequestPayload([][]byte{bytes.Repeat([]byte{0x22}, 32)}, nil),
		}

		err := participant.HandleRoastNonceRequest(env)
		if err == nil || !strings.Contains(err.Error(), "request from non-current coordinator") {
			t.Fatalf("unexpected coordinator error: %v", err)
		}
		if participant.GetSession(jobID) != nil {
			t.Fatal("rejected coordinator must not create a participant session")
		}
		if got := messenger.sendCount("NonceCommit"); got != 0 {
			t.Fatalf("rejected coordinator sent nonce commitment: got=%d", got)
		}
	})

	t.Run("sign request", func(t *testing.T) {
		messenger := &recordingMessenger{}
		participant := newParticipant(messenger)
		participant.sessions[jobID] = &ParticipantSession{
			JobID:    jobID,
			Chain:    chain,
			VaultID:  vault,
			KeyEpoch: epoch,
			SignAlgo: pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
			Messages: [][]byte{bytes.Repeat([]byte{0x22}, 32)},
			State:    ParticipantStateNonceGenerated,
		}

		env := &Envelope{
			SessionID: jobID,
			Kind:      "SignRequest",
			From:      wrongCoordinator(participant),
			Chain:     chain,
			VaultID:   vault,
			SignAlgo:  pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
			Epoch:     epoch,
			Payload:   encodeRoastRequestPayload([][]byte{bytes.Repeat([]byte{0x22}, 32)}, []byte{0x01}),
		}

		err := participant.HandleRoastSignRequest(env)
		if err == nil || !strings.Contains(err.Error(), "request from non-current coordinator") {
			t.Fatalf("unexpected coordinator error: %v", err)
		}
		if got := participant.GetSession(jobID).State; got != ParticipantStateNonceGenerated {
			t.Fatalf("rejected coordinator changed session state: got=%s", got.String())
		}
		if got := messenger.sendCount("SigShare"); got != 0 {
			t.Fatalf("rejected coordinator sent signature share: got=%d", got)
		}
	})
}

func TestParticipantSignRequestRejectsEnvelopeContextMutations(t *testing.T) {
	const jobID = "job-envelope-context-binding"
	message := bytes.Repeat([]byte{0x22}, 32)
	base := Envelope{
		SessionID: jobID,
		Kind:      "SignRequest",
		From:      "node-a",
		Chain:     "btc",
		VaultID:   1,
		SignAlgo:  pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		Epoch:     1,
		Round:     2,
		Payload:   encodeRoastRequestPayload([][]byte{message}, []byte{0x01}),
	}

	mutations := []struct {
		name   string
		mutate func(*Envelope)
	}{
		{name: "session_id", mutate: func(env *Envelope) { env.SessionID = jobID + "-other" }},
		{name: "chain", mutate: func(env *Envelope) { env.Chain = "eth" }},
		{name: "vault_id", mutate: func(env *Envelope) { env.VaultID++ }},
		{name: "epoch", mutate: func(env *Envelope) { env.Epoch++ }},
		{name: "sign_algo", mutate: func(env *Envelope) { env.SignAlgo = pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128 }},
	}

	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			messenger := &recordingMessenger{}
			participant := NewParticipant(
				"node-a",
				messenger,
				&fixedCommitteeVaultProvider{committee: []SignerInfo{{ID: "node-a", Index: 0, Weight: 1}}},
				&mockCryptoFactory{},
				nil,
				nil,
				logs.NewNodeLogger("participant-envelope-context-test", 0),
			)
			participant.sessions[jobID] = &ParticipantSession{
				JobID:    jobID,
				Chain:    base.Chain,
				VaultID:  base.VaultID,
				KeyEpoch: base.Epoch,
				SignAlgo: base.SignAlgo,
				Round:    base.Round,
				Messages: [][]byte{cloneBytes(message)},
				State:    ParticipantStateNonceGenerated,
			}

			env := base
			env.Payload = cloneBytes(base.Payload)
			tc.mutate(&env)

			if err := participant.HandleRoastSignRequest(&env); err == nil {
				t.Fatal("mutated envelope context was accepted")
			}
			if got := participant.GetSession(jobID).State; got != ParticipantStateNonceGenerated {
				t.Fatalf("context rejection changed session state: got=%s", got.String())
			}
			if got := messenger.sendCount("SigShare"); got != 0 {
				t.Fatalf("context rejection sent signature share: got=%d", got)
			}
		})
	}
}
