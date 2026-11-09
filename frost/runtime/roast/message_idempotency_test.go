package roast

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	roastsession "dex/frost/runtime/session"
	"dex/frost/runtime/types"
	"dex/logs"
	"dex/pb"
)

type fixedCommitteeVaultProvider struct {
	committee   []SignerInfo
	groupPubkey []byte
}

func (p *fixedCommitteeVaultProvider) VaultCommittee(string, uint32, uint64) ([]SignerInfo, error) {
	return p.committee, nil
}

func (p *fixedCommitteeVaultProvider) VaultCurrentEpoch(string, uint32) uint64 { return 1 }

func (p *fixedCommitteeVaultProvider) VaultGroupPubkey(string, uint32, uint64) ([]byte, error) {
	return append([]byte(nil), p.groupPubkey...), nil
}

func (p *fixedCommitteeVaultProvider) CalculateThreshold(string, uint32) (int, error) { return 0, nil }

type recordingMessenger struct {
	mu              sync.Mutex
	sendKinds       []string
	sendRounds      []uint32
	broadcastKinds  []string
	broadcastRounds []uint32
}

func (m *recordingMessenger) Send(_ NodeID, msg *types.RoastEnvelope) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sendKinds = append(m.sendKinds, msg.Kind)
	m.sendRounds = append(m.sendRounds, msg.Round)
	return nil
}

func (m *recordingMessenger) Broadcast(_ []NodeID, msg *types.RoastEnvelope) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.broadcastKinds = append(m.broadcastKinds, msg.Kind)
	m.broadcastRounds = append(m.broadcastRounds, msg.Round)
	return nil
}

func (m *recordingMessenger) roundsForSend(kind string) []uint32 {
	m.mu.Lock()
	defer m.mu.Unlock()
	var rounds []uint32
	for i, got := range m.sendKinds {
		if got == kind {
			rounds = append(rounds, m.sendRounds[i])
		}
	}
	return rounds
}

func (m *recordingMessenger) roundsForBroadcast(kind string) []uint32 {
	m.mu.Lock()
	defer m.mu.Unlock()
	var rounds []uint32
	for i, got := range m.broadcastKinds {
		if got == kind {
			rounds = append(rounds, m.broadcastRounds[i])
		}
	}
	return rounds
}

func (m *recordingMessenger) sendCount(kind string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, got := range m.sendKinds {
		if got == kind {
			count++
		}
	}
	return count
}

func (m *recordingMessenger) broadcastCount(kind string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, got := range m.broadcastKinds {
		if got == kind {
			count++
		}
	}
	return count
}

func TestParticipantHandleRoastSignRequestResendsCachedShares(t *testing.T) {
	messenger := &recordingMessenger{}
	vaultProvider := &fixedCommitteeVaultProvider{
		committee: []SignerInfo{
			{ID: "node-a", Index: 0, Weight: 1},
		},
	}

	participant := NewParticipant(
		"node-a",
		messenger,
		vaultProvider,
		&mockCryptoFactory{},
		nil,
		nil,
		logs.NewNodeLogger("participant-idempotency-test", 0),
	)

	jobID := "job-dup-sign"
	participant.sessions[jobID] = &ParticipantSession{
		JobID:            jobID,
		VaultID:          1,
		Chain:            "btc",
		KeyEpoch:         1,
		SignAlgo:         pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		Messages:         [][]byte{{0x01}},
		AggregatedNonces: []byte{0x02},
		State:            ParticipantStateShareGenerated,
		GeneratedShares:  [][]byte{bytes.Repeat([]byte{0x42}, 32)},
	}

	env := &Envelope{
		SessionID: jobID,
		Kind:      "SignRequest",
		From:      "node-a",
		Chain:     "btc",
		VaultID:   1,
		SignAlgo:  pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		Epoch:     1,
		Payload:   encodeRoastRequestPayload([][]byte{{0x01}}, []byte{0x02}),
	}

	if err := participant.HandleRoastSignRequest(env); err != nil {
		t.Fatalf("first duplicate HandleRoastSignRequest failed: %v", err)
	}
	if err := participant.HandleRoastSignRequest(env); err != nil {
		t.Fatalf("second duplicate HandleRoastSignRequest failed: %v", err)
	}

	if got := messenger.sendCount("SigShare"); got != 2 {
		t.Fatalf("duplicate sign request should resend cached shares twice: got=%d want=2", got)
	}

	tampered := *env
	tampered.Payload = encodeRoastRequestPayload([][]byte{{0x02}}, []byte{0x02})
	if err := participant.HandleRoastSignRequest(&tampered); err == nil {
		t.Fatal("cached share must not be resent for a different signing message")
	}
	if got := messenger.sendCount("SigShare"); got != 2 {
		t.Fatalf("tampered retry sent an additional share: got=%d want=2", got)
	}

	tamperedNonces := *env
	tamperedNonces.Payload = encodeRoastRequestPayload([][]byte{{0x01}}, []byte{0x03})
	if err := participant.HandleRoastSignRequest(&tamperedNonces); err == nil {
		t.Fatal("cached share must not be resent for different aggregate nonces")
	}
	if got := messenger.sendCount("SigShare"); got != 2 {
		t.Fatalf("aggregate-nonce tampering sent an additional share: got=%d want=2", got)
	}
}

func TestCoordinatorHandleRoastSigShareIgnoresLateDuplicate(t *testing.T) {
	vaultProvider := &fixedCommitteeVaultProvider{
		committee: []SignerInfo{
			{ID: "node-a", Index: 0, Weight: 1},
		},
	}
	coord := NewCoordinator(
		"node-a",
		nil,
		vaultProvider,
		nil,
		logs.NewNodeLogger("coordinator-idempotency-test", 0),
	)

	jobID := "job-late-share"
	sess := roastsession.NewSession(roastsession.SessionParams{
		JobID:     jobID,
		VaultID:   1,
		Chain:     "btc",
		KeyEpoch:  1,
		SignAlgo:  int32(pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340),
		Messages:  [][]byte{{0x01}},
		Committee: []roastsession.Participant{{ID: "node-a", Index: 0}},
		Threshold: 0,
		MyIndex:   0,
	})
	sess.SetState(roastsession.SignSessionStateAggregating)
	coord.sessions[jobID] = sess

	env := &Envelope{
		SessionID: jobID,
		Kind:      "SigShare",
		From:      "node-a",
		Chain:     "btc",
		VaultID:   1,
		SignAlgo:  pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		Epoch:     1,
		Payload:   bytes.Repeat([]byte{0x24}, 32),
	}

	if err := coord.HandleRoastSigShare(env); err != nil {
		t.Fatalf("late HandleRoastSigShare returned error: %v", err)
	}
}

func TestCoordinatorLoopDoesNotRebroadcastImmediateDuplicateSignRequest(t *testing.T) {
	messenger := &recordingMessenger{}
	vaultProvider := &fixedCommitteeVaultProvider{
		committee: []SignerInfo{
			{ID: "node-a", Index: 0, Weight: 1},
		},
	}
	coord := NewCoordinator(
		"node-a",
		messenger,
		vaultProvider,
		&mockCryptoFactory{},
		logs.NewNodeLogger("coordinator-loop-test", 0),
	)
	coord.config.NonceCollectTimeout = 2 * time.Second
	coord.config.ShareCollectTimeout = 2 * time.Second

	jobID := "job-single-sign-request"
	sess := roastsession.NewSession(roastsession.SessionParams{
		JobID:     jobID,
		VaultID:   1,
		Chain:     "btc",
		KeyEpoch:  1,
		SignAlgo:  int32(pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340),
		Messages:  [][]byte{{0x01}},
		Committee: []roastsession.Participant{{ID: "node-a", Index: 0}},
		Threshold: 0,
		MyIndex:   0,
	})
	if err := sess.Start(); err != nil {
		t.Fatalf("sess.Start failed: %v", err)
	}
	if err := sess.AddNonce(0, [][]byte{{0x11}}, [][]byte{{0x22}}); err != nil {
		t.Fatalf("sess.AddNonce failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		coord.runCoordinatorLoop(ctx, sess)
		close(done)
	}()

	time.Sleep(450 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runCoordinatorLoop did not exit after cancel")
	}

	if got := messenger.broadcastCount("SignRequest"); got != 1 {
		t.Fatalf("sign request broadcast count mismatch: got=%d want=1", got)
	}
}

func TestCoordinatorRoundAwareMessagesRejectMismatches(t *testing.T) {
	coord := NewCoordinator(
		"node-a",
		nil,
		&fixedCommitteeVaultProvider{committee: []SignerInfo{{ID: "node-a", Index: 0, Weight: 1}}},
		nil,
		logs.NewNodeLogger("coordinator-round-test", 0),
	)

	const jobID = "job-round-aware-messages"
	sess := roastsession.NewSession(roastsession.SessionParams{
		JobID: jobID, VaultID: 1, Chain: "btc", KeyEpoch: 1,
		SignAlgo:  int32(pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340),
		Messages:  [][]byte{{0x01}},
		Committee: []roastsession.Participant{{ID: "node-a", Index: 0}},
		Threshold: 1,
	})
	if err := sess.Start(); err != nil {
		t.Fatalf("start session: %v", err)
	}
	if !sess.ResetForRetry(2) {
		t.Fatal("advance to round 1")
	}
	coord.sessions[jobID] = sess

	nonceEnv := &Envelope{
		SessionID: jobID, Kind: "NonceCommit", From: "node-a", Chain: "btc", VaultID: 1, Epoch: 1,
		SignAlgo: pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		Payload:  bytes.Repeat([]byte{0x02}, 66),
	}
	for _, mismatched := range []uint32{0, 2} {
		nonceEnv.Round = mismatched
		if err := coord.HandleRoastNonceCommit(nonceEnv); err != nil {
			t.Fatalf("round %d nonce mismatch should be ignored: %v", mismatched, err)
		}
		if _, ok := sess.GetNonce(0); ok {
			t.Fatalf("round %d nonce was recorded in current round", mismatched)
		}
	}
	nonceEnv.Round = 1
	if err := coord.HandleRoastNonceCommit(nonceEnv); err != nil {
		t.Fatalf("current nonce: %v", err)
	}
	if nonce, ok := sess.GetNonce(0); !ok || nonce.Round != 1 {
		t.Fatalf("current nonce missing or wrong round: ok=%v nonce=%+v", ok, nonce)
	}

	sess.SetState(roastsession.SignSessionStateCollectingShares)
	shareEnv := &Envelope{
		SessionID: jobID, Kind: "SigShare", From: "node-a", Chain: "btc", VaultID: 1, Epoch: 1,
		SignAlgo: pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		Payload:  bytes.Repeat([]byte{0x03}, 32),
	}
	for _, mismatched := range []uint32{0, 2} {
		shareEnv.Round = mismatched
		if err := coord.HandleRoastSigShare(shareEnv); err != nil {
			t.Fatalf("round %d share mismatch should be ignored: %v", mismatched, err)
		}
		if _, ok := sess.GetShare(0); ok {
			t.Fatalf("round %d share was recorded in current round", mismatched)
		}
	}
	shareEnv.Round = 1
	if err := coord.HandleRoastSigShare(shareEnv); err != nil {
		t.Fatalf("current share: %v", err)
	}
	if share, ok := sess.GetShare(0); !ok || share.Round != 1 {
		t.Fatalf("current share missing or wrong round: ok=%v share=%+v", ok, share)
	}
}

func TestCoordinatorBroadcastsCurrentAttemptRound(t *testing.T) {
	messenger := &recordingMessenger{}
	coord := NewCoordinator(
		"node-a",
		messenger,
		&fixedCommitteeVaultProvider{committee: []SignerInfo{{ID: "node-a", Index: 0, Weight: 1}}},
		nil,
		logs.NewNodeLogger("coordinator-broadcast-round-test", 0),
	)
	sess := roastsession.NewSession(roastsession.SessionParams{
		JobID: "job-broadcast-round", VaultID: 1, Chain: "btc", KeyEpoch: 1,
		SignAlgo:  int32(pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340),
		Messages:  [][]byte{{0x01}},
		Committee: []roastsession.Participant{{ID: "node-a", Index: 0}},
		Threshold: 1,
	})
	if err := sess.Start(); err != nil {
		t.Fatalf("start session: %v", err)
	}
	if !sess.ResetForRetry(2) {
		t.Fatal("advance to round 1")
	}

	coord.broadcastNonceRequest(sess)
	sess.CachedNoncePayload = []byte{0x01}
	coord.broadcastSignRequest(sess)

	if got := messenger.roundsForBroadcast("NonceRequest"); len(got) != 1 || got[0] != 1 {
		t.Fatalf("nonce request rounds=%v want=[1]", got)
	}
	if got := messenger.roundsForBroadcast("SignRequest"); len(got) != 1 || got[0] != 1 {
		t.Fatalf("sign request rounds=%v want=[1]", got)
	}
}
