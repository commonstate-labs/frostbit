package roast

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"testing"
	"time"

	"dex/frost/runtime/session"
	"dex/frost/runtime/types"
	"dex/logs"
	"dex/pb"
)

type deterministicNonceFactory struct {
	hiding  *big.Int
	binding *big.Int
}

func (f *deterministicNonceFactory) NewROASTExecutor(int32) (ROASTExecutor, error) {
	return &deterministicNonceExecutor{
		secp256k1Executor: newSecp256k1Executor(),
		hiding:            new(big.Int).Set(f.hiding),
		binding:           new(big.Int).Set(f.binding),
	}, nil
}

func (f *deterministicNonceFactory) NewDKGExecutor(int32) (DKGExecutor, error) {
	return nil, nil
}

type deterministicNonceExecutor struct {
	*secp256k1Executor
	hiding  *big.Int
	binding *big.Int
}

type incrementingNonceFactory struct {
	mu   sync.Mutex
	next int64
}

func (f *incrementingNonceFactory) NewROASTExecutor(int32) (ROASTExecutor, error) {
	return &incrementingNonceExecutor{secp256k1Executor: newSecp256k1Executor(), factory: f}, nil
}

func (*incrementingNonceFactory) NewDKGExecutor(int32) (DKGExecutor, error) { return nil, nil }

type incrementingNonceExecutor struct {
	*secp256k1Executor
	factory *incrementingNonceFactory
}

func (e *incrementingNonceExecutor) GenerateNoncePair() (hiding, binding *big.Int, hidingPoint, bindingPoint CurvePoint, err error) {
	e.factory.mu.Lock()
	hiding = big.NewInt(e.factory.next)
	binding = big.NewInt(e.factory.next + 1)
	e.factory.next += 2
	e.factory.mu.Unlock()
	return hiding, binding, e.ScalarBaseMult(hiding), e.ScalarBaseMult(binding), nil
}

type countingNonceStore struct {
	mu    sync.Mutex
	store *session.MemoryNonceStore
	saves int
}

type blockingBatchNonceStore struct {
	backing *session.MemoryNonceStore
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingBatchNonceStore() *blockingBatchNonceStore {
	return &blockingBatchNonceStore{
		backing: session.NewMemoryNonceStore(),
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (s *blockingBatchNonceStore) SaveNonce(nonce, msgHash []byte, keyEpoch uint64) error {
	return s.backing.SaveNonce(nonce, msgHash, keyEpoch)
}

func (s *blockingBatchNonceStore) SaveNonceBatch(records []session.NonceRecord) error {
	s.once.Do(func() { close(s.entered) })
	<-s.release
	return s.backing.SaveNonceBatch(records)
}

func (s *blockingBatchNonceStore) GetNonceBind(nonce []byte) ([]byte, uint64, bool) {
	return s.backing.GetNonceBind(nonce)
}

func (s *countingNonceStore) SaveNonce(nonce, msgHash []byte, keyEpoch uint64) error {
	if err := s.store.SaveNonce(nonce, msgHash, keyEpoch); err != nil {
		return err
	}
	s.mu.Lock()
	s.saves++
	s.mu.Unlock()
	return nil
}

func (s *countingNonceStore) GetNonceBind(nonce []byte) ([]byte, uint64, bool) {
	return s.store.GetNonceBind(nonce)
}

func (s *countingNonceStore) saveCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saves
}

type commitmentOrderMessenger struct {
	store *countingNonceStore
	sent  int
}

func (m *commitmentOrderMessenger) Send(_ NodeID, msg *types.RoastEnvelope) error {
	if msg.Kind == "NonceCommit" {
		if got := m.store.saveCount(); got != 2 {
			return fmt.Errorf("nonce commitment sent after %d durable binds, want 2", got)
		}
		m.sent++
	}
	return nil
}

func (*commitmentOrderMessenger) Broadcast([]NodeID, *types.RoastEnvelope) error { return nil }

func (e *deterministicNonceExecutor) GenerateNoncePair() (hiding, binding *big.Int, hidingPoint, bindingPoint CurvePoint, err error) {
	hiding = new(big.Int).Set(e.hiding)
	binding = new(big.Int).Set(e.binding)
	return hiding, binding, e.ScalarBaseMult(hiding), e.ScalarBaseMult(binding), nil
}

func TestParticipantBurnsBothNonceScalarsAcrossCloseAndRestart(t *testing.T) {
	tests := []struct {
		name          string
		restartHiding int64
		restartBind   int64
	}{
		{name: "hiding scalar reused", restartHiding: 11, restartBind: 33},
		{name: "binding scalar reused", restartHiding: 33, restartBind: 22},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			backing := session.NewMemoryNonceStore()
			beforeRestartStore := session.NewSessionStore(backing)
			provider := &fixedCommitteeVaultProvider{committee: []SignerInfo{{ID: "node-a", Index: 0, Weight: 1}}}

			beforeRestart := newDeterministicParticipant(t, beforeRestartStore, provider, 11, 22)
			first := nonceRequest("job-before-crash", []byte("message-a"))
			if err := beforeRestart.HandleRoastNonceRequest(first); err != nil {
				t.Fatalf("initial nonce request: %v", err)
			}
			beforeRestart.CloseSession(first.SessionID)

			afterRestartMessenger := &recordingMessenger{}
			afterRestart := newDeterministicParticipantWithMessenger(
				t,
				session.NewSessionStore(backing),
				provider,
				afterRestartMessenger,
				tc.restartHiding,
				tc.restartBind,
			)
			err := afterRestart.HandleRoastNonceRequest(nonceRequest("job-after-crash", []byte("message-b")))
			if !errors.Is(err, session.ErrNonceAlreadyBound) {
				t.Fatalf("reused nonce scalar = %v, want ErrNonceAlreadyBound", err)
			}
			if got := afterRestartMessenger.sendCount("NonceCommit"); got != 0 {
				t.Fatalf("reused scalar emitted nonce commitment: got=%d", got)
			}
		})
	}
}

func TestParticipantPersistsNoncePairBeforeSendingCommitment(t *testing.T) {
	backing := &countingNonceStore{store: session.NewMemoryNonceStore()}
	messenger := &commitmentOrderMessenger{store: backing}
	provider := &fixedCommitteeVaultProvider{committee: []SignerInfo{{ID: "node-a", Index: 0, Weight: 1}}}
	participant := newDeterministicParticipantWithMessenger(
		t,
		session.NewSessionStore(backing),
		provider,
		messenger,
		11,
		22,
	)
	if err := participant.HandleRoastNonceRequest(nonceRequest("job-persist-before-send", []byte("message"))); err != nil {
		t.Fatalf("nonce request: %v", err)
	}
	if messenger.sent != 1 {
		t.Fatalf("nonce commitment sends=%d want=1", messenger.sent)
	}
}

func TestParticipantRejectsSameScalarForBothNonceRoles(t *testing.T) {
	backing := session.NewMemoryNonceStore()
	messenger := &recordingMessenger{}
	provider := &fixedCommitteeVaultProvider{committee: []SignerInfo{{ID: "node-a", Index: 0, Weight: 1}}}
	participant := newDeterministicParticipantWithMessenger(
		t,
		session.NewSessionStore(backing),
		provider,
		messenger,
		11,
		11,
	)
	if err := participant.HandleRoastNonceRequest(nonceRequest("job-duplicate-pair-scalar", []byte("message"))); !errors.Is(err, session.ErrNonceAlreadyBound) {
		t.Fatalf("same hiding/binding scalar = %v, want ErrNonceAlreadyBound", err)
	}
	if got := messenger.sendCount("NonceCommit"); got != 0 {
		t.Fatalf("invalid nonce pair emitted commitment: got=%d", got)
	}
}

func TestParticipantCompletedJobCannotRebindRepeatedRNGNonce(t *testing.T) {
	backing := session.NewMemoryNonceStore()
	messenger := &recordingMessenger{}
	provider := &fixedCommitteeVaultProvider{committee: []SignerInfo{{ID: "node-a", Index: 0, Weight: 1}}}
	participant := newDeterministicParticipantWithMessenger(
		t,
		session.NewSessionStore(backing),
		provider,
		messenger,
		11,
		22,
	)
	request := nonceRequest("job-reopened-after-share", []byte("same-message"))
	if err := participant.HandleRoastNonceRequest(request); err != nil {
		t.Fatalf("initial nonce request: %v", err)
	}
	sess := participant.GetSession(request.SessionID)
	if sess == nil {
		t.Fatal("participant session missing")
	}
	sess.mu.Lock()
	sess.State = ParticipantStateShareGenerated
	sess.mu.Unlock()

	                                                                          
	                                    
	request.Round++
	if err := participant.HandleRoastNonceRequest(request); !errors.Is(err, session.ErrNonceAlreadyBound) {
		t.Fatalf("completed job rebound repeated nonce = %v, want ErrNonceAlreadyBound", err)
	}
	if got := messenger.sendCount("NonceCommit"); got != 1 {
		t.Fatalf("completed job emitted a second commitment: got=%d want=1", got)
	}
}

func TestParticipantTerminalRetryReleasesOldLiveBindings(t *testing.T) {
	backing := session.NewMemoryNonceStore()
	liveStore := session.NewSessionStore(backing)
	provider := &fixedCommitteeVaultProvider{committee: []SignerInfo{{ID: "node-a", Index: 0, Weight: 1}}}
	participant := newDeterministicParticipant(t, liveStore, provider, 11, 22)

	const jobID = "job-terminal-active-cleanup"
	message := []byte("canonical-message")
	hiding := bytes.Repeat([]byte{0x61}, 32)
	binding := bytes.Repeat([]byte{0x62}, 32)
	hidingContext := nonceBindingContext(jobID, 0, nonceRoleHiding, message)
	bindingContext := nonceBindingContext(jobID, 0, nonceRoleBinding, message)
	if err := liveStore.BindNonceBatch([]session.NonceBinding{
		{Nonce: hiding, Message: hidingContext, KeyEpoch: 1},
		{Nonce: binding, Message: bindingContext, KeyEpoch: 1},
	}); err != nil {
		t.Fatalf("seed old live nonce pair: %v", err)
	}
	participant.sessions[jobID] = &ParticipantSession{
		JobID: jobID, Chain: "btc", VaultID: 1, KeyEpoch: 1,
		SignAlgo: pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		MyIndex:  0, MyShare: bytes.Repeat([]byte{0x11}, 32), Messages: [][]byte{message},
		HidingNonces: [][]byte{hiding}, BindingNonces: [][]byte{binding},
		State: ParticipantStateShareGenerated,
	}

	sess, err := participant.getOrCreateSession(
		jobID, "btc", 1, 1, pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		1, 0, bytes.Repeat([]byte{0x11}, 32), [][]byte{message}, nil,
	)
	if err != nil {
		t.Fatalf("reset terminal session: %v", err)
	}
	sess.mu.RLock()
	if sess.State != ParticipantStateInit || len(sess.HidingNonces) != 0 || len(sess.BindingNonces) != 0 || len(sess.GeneratedShares) != 0 {
		t.Fatalf("terminal reset retained old signing state: state=%s hiding=%d binding=%d shares=%d",
			sess.State.String(), len(sess.HidingNonces), len(sess.BindingNonces), len(sess.GeneratedShares))
	}
	sess.mu.RUnlock()
	if liveStore.ValidateNonceForMessage(hiding, hidingContext) || liveStore.ValidateNonceForMessage(binding, bindingContext) {
		t.Fatal("terminal reset leaked an old live nonce binding")
	}
	if _, _, exists := backing.GetNonceBind(hiding); !exists {
		t.Fatal("terminal reset deleted the hiding nonce tombstone")
	}
	if _, _, exists := backing.GetNonceBind(binding); !exists {
		t.Fatal("terminal reset deleted the binding nonce tombstone")
	}
}

func TestParticipantNonceAttemptsMatchCoordinatorRetryBudget(t *testing.T) {
	backing := session.NewMemoryNonceStore()
	messenger := &recordingMessenger{}
	provider := &fixedCommitteeVaultProvider{committee: []SignerInfo{{ID: "node-a", Index: 0, Weight: 1}}}
	participant := NewParticipant(
		"node-a",
		messenger,
		provider,
		&incrementingNonceFactory{next: 11},
		session.NewSessionStore(backing),
		nil,
		logs.NewNodeLogger("participant-retry-budget-test", 0),
	)
	participant.SetShare("btc", 1, 1, bytes.Repeat([]byte{0x11}, 32))
	request := nonceRequest("job-retry-budget", []byte("message"))

	                                                                         
	                                                               
	for attempt := 0; attempt < 1+DefaultSigningMaxRetries; attempt++ {
		request.Round = uint32(attempt)
		if err := participant.HandleRoastNonceRequest(request); err != nil {
			t.Fatalf("attempt %d rejected: %v", attempt, err)
		}
		sess := participant.GetSession(request.SessionID)
		if sess == nil {
			t.Fatalf("attempt %d lost participant session", attempt)
		}
		sess.mu.Lock()
		sess.State = ParticipantStateShareGenerated
		sess.mu.Unlock()
	}
	wantAttempts := 1 + DefaultSigningMaxRetries
	if got := messenger.sendCount("NonceCommit"); got != wantAttempts {
		t.Fatalf("nonce commitments=%d want=%d", got, wantAttempts)
	}
	if got := backing.Len(); got != wantAttempts*2 {
		t.Fatalf("burned tombstones=%d want=%d", got, wantAttempts*2)
	}

	request.Round = uint32(wantAttempts)
	if err := participant.HandleRoastNonceRequest(request); !errors.Is(err, ErrParticipantRetryLimit) {
		t.Fatalf("attempt past retry budget = %v, want ErrParticipantRetryLimit", err)
	}
	if got := messenger.sendCount("NonceCommit"); got != wantAttempts {
		t.Fatalf("over-budget request sent another commitment: got=%d", got)
	}
	if got := backing.Len(); got != wantAttempts*2 {
		t.Fatalf("over-budget request burned more tombstones: got=%d", got)
	}
}

func TestParticipantCloseSessionSerializesWithNonceGeneration(t *testing.T) {
	backing := newBlockingBatchNonceStore()
	liveStore := session.NewSessionStore(backing)
	messenger := &recordingMessenger{}
	provider := &fixedCommitteeVaultProvider{committee: []SignerInfo{{ID: "node-a", Index: 0, Weight: 1}}}
	participant := newDeterministicParticipantWithMessenger(t, liveStore, provider, messenger, 11, 22)
	request := nonceRequest("job-close-race", []byte("message"))

	handleDone := make(chan error, 1)
	go func() { handleDone <- participant.HandleRoastNonceRequest(request) }()
	select {
	case <-backing.entered:
	case <-time.After(time.Second):
		t.Fatal("nonce generation did not reach durability barrier")
	}

	closeDone := make(chan struct{})
	go func() {
		participant.CloseSession(request.SessionID)
		close(closeDone)
	}()
	select {
	case <-closeDone:
		t.Fatal("CloseSession passed a live nonce-generation operation")
	case <-time.After(20 * time.Millisecond):
	}

	close(backing.release)
	if err := <-handleDone; err != nil {
		t.Fatalf("nonce request failed: %v", err)
	}
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("CloseSession did not finish after nonce generation")
	}
	if participant.GetSession(request.SessionID) != nil {
		t.Fatal("closed participant session remains visible")
	}
	if got := messenger.sendCount("NonceCommit"); got != 1 {
		t.Fatalf("nonce commitments=%d want=1", got)
	}

	hiding := make([]byte, 32)
	binding := make([]byte, 32)
	big.NewInt(11).FillBytes(hiding)
	big.NewInt(22).FillBytes(binding)
	if liveStore.ValidateNonceForMessage(hiding, nonceBindingContext(request.SessionID, 0, nonceRoleHiding, []byte("message"))) ||
		liveStore.ValidateNonceForMessage(binding, nonceBindingContext(request.SessionID, 0, nonceRoleBinding, []byte("message"))) {
		t.Fatal("CloseSession left a generated nonce active")
	}
}

func TestParticipantNonceRequestRetryResendsOriginalCommitmentWithoutRebind(t *testing.T) {
	backing := session.NewMemoryNonceStore()
	messenger := &recordingMessenger{}
	provider := &fixedCommitteeVaultProvider{committee: []SignerInfo{{ID: "node-a", Index: 0, Weight: 1}}}
	participant := newDeterministicParticipantWithMessenger(
		t,
		session.NewSessionStore(backing),
		provider,
		messenger,
		11,
		22,
	)
	request := nonceRequest("job-live-retry", []byte("message"))
	if err := participant.HandleRoastNonceRequest(request); err != nil {
		t.Fatalf("initial nonce request: %v", err)
	}
	if err := participant.HandleRoastNonceRequest(request); err != nil {
		t.Fatalf("live retry: %v", err)
	}
	if got := messenger.sendCount("NonceCommit"); got != 2 {
		t.Fatalf("nonce commitment sends=%d want=2", got)
	}
	if got := backing.Len(); got != 2 {
		t.Fatalf("retry created new nonce tombstones: got=%d want=2", got)
	}
}

func TestParticipantHandlesStaleCurrentAndFutureAttemptRounds(t *testing.T) {
	backing := session.NewMemoryNonceStore()
	messenger := &recordingMessenger{}
	provider := &fixedCommitteeVaultProvider{committee: []SignerInfo{{ID: "node-a", Index: 0, Weight: 1}}}
	participant := NewParticipant(
		"node-a",
		messenger,
		provider,
		&incrementingNonceFactory{next: 11},
		session.NewSessionStore(backing),
		nil,
		logs.NewNodeLogger("participant-round-test", 0),
	)
	participant.SetShare("btc", 1, 1, bytes.Repeat([]byte{0x11}, 32))

	request := nonceRequest("job-participant-rounds", []byte("message"))
	request.Round = 3
	if err := participant.HandleRoastNonceRequest(request); err != nil {
		t.Fatalf("initial round 3 request: %v", err)
	}
	if err := participant.HandleRoastNonceRequest(request); err != nil {
		t.Fatalf("same-round transport retry: %v", err)
	}
	if got := messenger.roundsForSend("NonceCommit"); len(got) != 2 || got[0] != 3 || got[1] != 3 {
		t.Fatalf("same-round commitments=%v want=[3 3]", got)
	}
	if got := backing.Len(); got != 2 {
		t.Fatalf("same-round retry burned new nonces: got=%d want=2", got)
	}

	stale := *request
	stale.Round = 2
	if err := participant.HandleRoastNonceRequest(&stale); err != nil {
		t.Fatalf("stale nonce request should be ignored: %v", err)
	}
	if got := messenger.sendCount("NonceCommit"); got != 2 {
		t.Fatalf("stale nonce request sent commitment: got=%d want=2", got)
	}

	future := *request
	future.Round = 4
	if err := participant.HandleRoastNonceRequest(&future); err != nil {
		t.Fatalf("future attempt request: %v", err)
	}
	if got := messenger.roundsForSend("NonceCommit"); len(got) != 3 || got[2] != 4 {
		t.Fatalf("future-round commitments=%v want last=4", got)
	}
	if got := backing.Len(); got != 4 {
		t.Fatalf("future round did not burn exactly one fresh nonce pair: got=%d want=4", got)
	}

	sess := participant.GetSession(request.SessionID)
	if sess == nil {
		t.Fatal("participant session missing")
	}
	sess.mu.Lock()
	if sess.Round != 4 || sess.RetryCount != 1 {
		t.Fatalf("participant round state=(round=%d retry=%d) want=(4,1)", sess.Round, sess.RetryCount)
	}
	sess.AggregatedNonces = []byte{0x02}
	sess.GeneratedShares = [][]byte{bytes.Repeat([]byte{0x44}, 32)}
	sess.State = ParticipantStateShareGenerated
	sess.mu.Unlock()

	signRequest := &Envelope{
		SessionID: request.SessionID, Kind: "SignRequest", From: "node-a", Chain: "btc", VaultID: 1, Epoch: 1,
		SignAlgo: pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		Payload:  encodeRoastRequestPayload([][]byte{[]byte("message")}, []byte{0x02}),
	}
	signRequest.Round = 3
	if err := participant.HandleRoastSignRequest(signRequest); err != nil {
		t.Fatalf("stale sign request should be ignored: %v", err)
	}
	if got := messenger.sendCount("SigShare"); got != 0 {
		t.Fatalf("stale sign request sent share: got=%d", got)
	}
	signRequest.Round = 5
	if err := participant.HandleRoastSignRequest(signRequest); !errors.Is(err, ErrFutureRoastRound) {
		t.Fatalf("future sign request=%v want ErrFutureRoastRound", err)
	}
	if got := messenger.sendCount("SigShare"); got != 0 {
		t.Fatalf("future sign request sent share: got=%d", got)
	}
	signRequest.Round = 4
	if err := participant.HandleRoastSignRequest(signRequest); err != nil {
		t.Fatalf("current sign request: %v", err)
	}
	if got := messenger.roundsForSend("SigShare"); len(got) != 1 || got[0] != 4 {
		t.Fatalf("signature share rounds=%v want=[4]", got)
	}
}

func TestParticipantCrashAfterNonceCommitRejectsReusedScalar(t *testing.T) {
	backing := session.NewMemoryNonceStore()
	provider := &fixedCommitteeVaultProvider{committee: []SignerInfo{{ID: "node-a", Index: 0, Weight: 1}}}
	beforeCrashMessenger := &recordingMessenger{}
	beforeCrash := newDeterministicParticipantWithMessenger(
		t,
		session.NewSessionStore(backing),
		provider,
		beforeCrashMessenger,
		11,
		22,
	)
	if err := beforeCrash.HandleRoastNonceRequest(nonceRequest("job-before-crash", []byte("message-a"))); err != nil {
		t.Fatalf("nonce request before crash: %v", err)
	}
	if got := beforeCrashMessenger.sendCount("NonceCommit"); got != 1 {
		t.Fatalf("commitment count before crash=%d want=1", got)
	}

	                                                                     
	                                                           
	afterCrashMessenger := &recordingMessenger{}
	afterCrash := newDeterministicParticipantWithMessenger(
		t,
		session.NewSessionStore(backing),
		provider,
		afterCrashMessenger,
		11,
		33,
	)
	if err := afterCrash.HandleRoastNonceRequest(nonceRequest("job-after-crash", []byte("message-b"))); !errors.Is(err, session.ErrNonceAlreadyBound) {
		t.Fatalf("reused scalar after crash = %v, want ErrNonceAlreadyBound", err)
	}
	if got := afterCrashMessenger.sendCount("NonceCommit"); got != 0 {
		t.Fatalf("post-crash reused scalar emitted commitment: got=%d", got)
	}
}

func TestNonceBindingContextSeparatesJobsTasksAndFieldBoundaries(t *testing.T) {
	base := nonceBindingContext("job-ab", 1, nonceRoleHiding, []byte("message"))
	cases := [][]byte{
		nonceBindingContext("job-ab", 2, nonceRoleHiding, []byte("message")),
		nonceBindingContext("job-ac", 1, nonceRoleHiding, []byte("message")),
		nonceBindingContext("job-ab", 1, nonceRoleBinding, []byte("message")),
		nonceBindingContext("job-ab", 1, nonceRoleHiding, []byte("message-2")),
		                                                       
		nonceBindingContext("job-a", 1, nonceRoleHiding, []byte("bmessage")),
	}
	for i, candidate := range cases {
		if bytes.Equal(base, candidate) {
			t.Fatalf("binding context collision at case %d", i)
		}
	}
}

func TestParticipantRestartSafelyRejectsOldSignRequest(t *testing.T) {
	backing := session.NewMemoryNonceStore()
	liveStore := session.NewSessionStore(backing)
	provider := &fixedCommitteeVaultProvider{committee: []SignerInfo{{ID: "node-a", Index: 0, Weight: 1}}}
	messenger := &recordingMessenger{}
	participant := newDeterministicParticipantWithMessenger(t, liveStore, provider, messenger, 11, 22)

	const jobID = "job-share-sent-before-crash"
	participant.sessions[jobID] = &ParticipantSession{
		JobID:            jobID,
		Chain:            "btc",
		VaultID:          1,
		KeyEpoch:         1,
		SignAlgo:         pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		Messages:         [][]byte{{0x01}},
		AggregatedNonces: []byte{0x02},
		State:            ParticipantStateShareGenerated,
		GeneratedShares:  [][]byte{bytes.Repeat([]byte{0x44}, 32)},
	}
	signRequest := &Envelope{
		SessionID: jobID,
		Kind:      "SignRequest",
		From:      "node-a",
		Chain:     "btc",
		VaultID:   1,
		Epoch:     1,
		SignAlgo:  pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		Payload:   encodeRoastRequestPayload([][]byte{{0x01}}, []byte{0x02}),
	}
	if err := participant.HandleRoastSignRequest(signRequest); err != nil {
		t.Fatalf("send cached share before crash: %v", err)
	}

	                                                                        
	                                                                          
	afterRestart := newDeterministicParticipantWithMessenger(
		t,
		session.NewSessionStore(backing),
		provider,
		messenger,
		11,
		22,
	)
	if err := afterRestart.HandleRoastSignRequest(signRequest); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("old sign request after restart = %v, want ErrSessionNotFound", err)
	}
	if got := messenger.sendCount("SigShare"); got != 1 {
		t.Fatalf("restart generated an additional signature share: got=%d want=1", got)
	}
}

func newDeterministicParticipant(t *testing.T, store *session.SessionStore, provider VaultCommitteeProvider, hiding, binding int64) *Participant {
	t.Helper()
	return newDeterministicParticipantWithMessenger(t, store, provider, &recordingMessenger{}, hiding, binding)
}

func newDeterministicParticipantWithMessenger(t *testing.T, store *session.SessionStore, provider VaultCommitteeProvider, messenger RoastMessenger, hiding, binding int64) *Participant {
	t.Helper()
	participant := NewParticipant(
		"node-a",
		messenger,
		provider,
		&deterministicNonceFactory{hiding: big.NewInt(hiding), binding: big.NewInt(binding)},
		store,
		nil,
		logs.NewNodeLogger("participant-nonce-lifecycle-test", 0),
	)
	participant.SetShare("btc", 1, 1, bytes.Repeat([]byte{0x11}, 32))
	return participant
}

func nonceRequest(jobID string, message []byte) *Envelope {
	return &Envelope{
		SessionID: jobID,
		Kind:      "NonceRequest",
		From:      "node-a",
		Chain:     "btc",
		VaultID:   1,
		Epoch:     1,
		SignAlgo:  pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		Payload:   encodeRoastRequestPayload([][]byte{message}, nil),
	}
}
