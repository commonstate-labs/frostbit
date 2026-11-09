package workers

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"

	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

type sessionThresholdVaultProvider struct {
	VaultCommitteeProvider
	threshold int
	committee []SignerInfo
}

func (p *sessionThresholdVaultProvider) CalculateThreshold(string, uint32) (int, error) {
	return p.threshold, nil
}

func (p *sessionThresholdVaultProvider) VaultCommittee(string, uint32, uint64) ([]SignerInfo, error) {
	return append([]SignerInfo(nil), p.committee...), nil
}

type sessionNoCallCryptoFactory struct {
	CryptoExecutorFactory
	calls    int
	executor DKGExecutor
}

func (f *sessionNoCallCryptoFactory) NewDKGExecutor(int32) (DKGExecutor, error) {
	f.calls++
	return f.executor, nil
}

type sessionCaptureSubmitter struct {
	TxSubmitter
	commits       int
	commitC       chan *pb.FrostVaultDkgCommitTx
	shares        int
	reveals       []*pb.FrostVaultDkgRevealTx
	revealC       chan *pb.FrostVaultDkgRevealTx
	validations   []*pb.FrostVaultDkgValidationSignedTx
	validationC   chan *pb.FrostVaultDkgValidationSignedTx
	validationErr []error
}

func (s *sessionCaptureSubmitter) SubmitDkgCommitTx(_ context.Context, tx *pb.FrostVaultDkgCommitTx) error {
	s.commits++
	if s.commitC != nil {
		s.commitC <- tx
	}
	return nil
}

func (s *sessionCaptureSubmitter) SubmitDkgShareTx(context.Context, *pb.FrostVaultDkgShareTx) error {
	s.shares++
	return nil
}

func (s *sessionCaptureSubmitter) SubmitDkgRevealTx(_ context.Context, tx *pb.FrostVaultDkgRevealTx) error {
	s.reveals = append(s.reveals, tx)
	if s.revealC != nil {
		s.revealC <- tx
	}
	return nil
}

func (s *sessionCaptureSubmitter) SubmitDkgValidationSignedTx(_ context.Context, tx *pb.FrostVaultDkgValidationSignedTx) error {
	s.validations = append(s.validations, tx)
	if s.validationC != nil {
		s.validationC <- tx
	}
	if len(s.validationErr) >= len(s.validations) {
		return s.validationErr[len(s.validations)-1]
	}
	return nil
}

type sessionRevealStore struct {
	LocalShareStore
	hasSession bool
	share      []byte
	encRand    []byte
	pending    []byte
}

type finalizedScanStateReader struct {
	mu                sync.RWMutex
	data              map[string][]byte
	complaintPrefix   string
	firstComplaintRun chan struct{}
	firstOnce         sync.Once
}

func (r *finalizedScanStateReader) Get(key string) ([]byte, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, exists := r.data[key]
	return append([]byte(nil), value...), exists, nil
}

func (r *finalizedScanStateReader) Scan(prefix string, fn func(string, []byte) bool) error {
	r.mu.RLock()
	type record struct {
		key   string
		value []byte
	}
	records := make([]record, 0)
	for key, value := range r.data {
		if strings.HasPrefix(key, prefix) {
			records = append(records, record{key: key, value: append([]byte(nil), value...)})
		}
	}
	r.mu.RUnlock()
	for _, record := range records {
		if !fn(record.key, record.value) {
			break
		}
	}
	if prefix == r.complaintPrefix {
		r.firstOnce.Do(func() { close(r.firstComplaintRun) })
	}
	return nil
}

func (r *finalizedScanStateReader) Set(key string, value []byte) {
	r.mu.Lock()
	r.data[key] = append([]byte(nil), value...)
	r.mu.Unlock()
}

func (s *sessionRevealStore) HasDkgLocalSession(string, uint32, uint64, string) (bool, error) {
	return s.hasSession, nil
}

func (s *sessionRevealStore) ListDkgLocalSessions() ([]DkgLocalSessionRef, error) {
	return nil, nil
}

func (s *sessionRevealStore) LoadDkgRevealMaterial(string, uint32, uint64, string, int) ([]byte, []byte, error) {
	return append([]byte(nil), s.share...), append([]byte(nil), s.encRand...), nil
}

func (s *sessionRevealStore) LoadPendingLocalShare(string, uint32, uint64, string) ([]byte, error) {
	return append([]byte(nil), s.pending...), nil
}

type sessionCommitmentExecutor struct {
	DKGExecutor
	points [][]byte
}

func (e *sessionCommitmentExecutor) ComputeCommitments(PolynomialHandle) [][]byte {
	return e.points
}

type sessionPolynomial struct {
	PolynomialHandle
}

type sessionFullDKGExecutor struct {
	DKGExecutor
}

func (*sessionFullDKGExecutor) GeneratePolynomial(int) (PolynomialHandle, error) {
	return &sessionPolynomial{}, nil
}

func (*sessionFullDKGExecutor) ComputeCommitments(PolynomialHandle) [][]byte {
	return [][]byte{{0x01}}
}

func (*sessionFullDKGExecutor) EvaluateShare(PolynomialHandle, int) []byte {
	return []byte{0x02}
}

func (*sessionFullDKGExecutor) AggregateShares([][]byte) []byte {
	return []byte{0x03}
}

func (*sessionFullDKGExecutor) ComputeGroupPubkey([][]byte) []byte {
	return []byte{0x44}
}

func (*sessionFullDKGExecutor) SchnorrSign(*big.Int, []byte) ([]byte, error) {
	return []byte{0x55}, nil
}

type sessionPersistenceFailureStore struct {
	LocalShareStore
	revealErr  error
	pendingErr error
}

func (s *sessionPersistenceFailureStore) SaveDkgRevealMaterial(string, uint32, uint64, string, int, []byte, []byte) error {
	return s.revealErr
}

func (s *sessionPersistenceFailureStore) SavePendingLocalShare(string, uint32, uint64, string, []byte) error {
	return s.pendingErr
}

func TestResolveSessionThresholdUsesFrozenOnChainValue(t *testing.T) {
	worker := &TransitionWorker{
		vaultProvider: &sessionThresholdVaultProvider{threshold: 1},
	}
	transition := &pb.VaultTransitionState{
		Chain:         "btc",
		VaultId:       1,
		DkgN:          4,
		DkgThresholdT: 3,
	}

	got, err := worker.resolveSessionThreshold(transition, 4)
	if err != nil {
		t.Fatalf("resolveSessionThreshold failed: %v", err)
	}
	if got != 3 {
		t.Fatalf("threshold drifted from on-chain value: got=%d want=3", got)
	}

	if _, err := worker.resolveSessionThreshold(transition, 2); err == nil {
		t.Fatal("expected frozen threshold larger than committee to be rejected, not clamped")
	}
}

func TestDkgSessionCanStartAtPreTriggerHeight(t *testing.T) {
	tests := []struct {
		current uint64
		trigger uint64
		want    bool
	}{
		{current: 0, trigger: 0, want: true},
		{current: 3, trigger: 5, want: false},
		{current: 4, trigger: 5, want: true},
		{current: 5, trigger: 5, want: true},
		{current: 6, trigger: 5, want: true},
	}
	for _, test := range tests {
		if got := dkgSessionCanStart(test.current, test.trigger); got != test.want {
			t.Fatalf("dkgSessionCanStart(%d, %d) = %t, want %t", test.current, test.trigger, got, test.want)
		}
	}
}

func TestStartSessionDoesNotSubmitCommitmentTooEarly(t *testing.T) {
	const (
		chain        = "btc"
		vaultID      = uint32(1)
		epochID      = uint64(7)
		sessionID    = "btc_1_7_1"
		localAddress = "miner-a"
	)
	transition := &pb.VaultTransitionState{
		Chain: chain, VaultId: vaultID, EpochId: epochID,
		SignAlgo:     pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		DkgSessionId: sessionID, DkgStatus: "COMMITTING",
		NewCommitteeMembers: []string{localAddress}, DkgN: 1, DkgThresholdT: 1,
		TriggerHeight: 10, DkgCommitDeadline: 14,
	}
	reader := &complaintTestStateReader{data: map[string][]byte{
		keys.KeyLatestHeight():                                []byte("8"),
		keys.KeyFrostVaultTransition(chain, vaultID, epochID): mustSessionProto(t, transition),
	}}
	cryptoFactory := &sessionNoCallCryptoFactory{}
	submitter := &sessionCaptureSubmitter{}
	worker := &TransitionWorker{
		stateReader: reader, txSubmitter: submitter, cryptoFactory: cryptoFactory,
		vaultProvider: &sessionThresholdVaultProvider{
			threshold: 1, committee: []SignerInfo{{ID: localAddress}},
		},
		localAddress: localAddress, Logger: complaintTestLogger{}, sessions: make(map[string]*DKGSession),
	}

	if err := worker.StartSession(context.Background(), chain, vaultID, epochID, transition.SignAlgo); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}

	if cryptoFactory.calls != 0 || submitter.commits != 0 {
		t.Fatalf("too-early session generated or submitted a commitment: crypto=%d commits=%d", cryptoFactory.calls, submitter.commits)
	}
	if worker.GetSession(sessionID) != nil {
		t.Fatal("too-early session was registered in memory")
	}
}

func TestStartSessionSubmitsAtPreTriggerHeight(t *testing.T) {
	const (
		chain        = "btc"
		vaultID      = uint32(1)
		epochID      = uint64(7)
		sessionID    = "btc_1_7_1"
		localAddress = "miner-a"
	)
	transition := &pb.VaultTransitionState{
		Chain: chain, VaultId: vaultID, EpochId: epochID,
		SignAlgo:     pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		DkgSessionId: sessionID, DkgStatus: "COMMITTING",
		NewCommitteeMembers: []string{localAddress}, DkgN: 1, DkgThresholdT: 1,
		TriggerHeight: 10, DkgCommitDeadline: 14, DkgSharingDeadline: 19, DkgDisputeDeadline: 24,
	}
	reader := &complaintTestStateReader{data: map[string][]byte{
		keys.KeyLatestHeight():                                []byte("9"),
		keys.KeyFrostVaultTransition(chain, vaultID, epochID): mustSessionProto(t, transition),
	}}
	submitter := &sessionCaptureSubmitter{commitC: make(chan *pb.FrostVaultDkgCommitTx, 1)}
	worker := &TransitionWorker{
		stateReader: reader, txSubmitter: submitter,
		cryptoFactory: &sessionNoCallCryptoFactory{executor: &sessionFullDKGExecutor{}},
		vaultProvider: &sessionThresholdVaultProvider{
			threshold: 1, committee: []SignerInfo{{ID: localAddress}},
		},
		localIdentity: testLocalIdentity(t, "0000000000000000000000000000000000000000000000000000000000000001"),
		localAddress:  localAddress, Logger: complaintTestLogger{}, sessions: make(map[string]*DKGSession),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := worker.StartSession(ctx, chain, vaultID, epochID, transition.SignAlgo); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	select {
	case tx := <-submitter.commitC:
		if tx.SessionId != sessionID {
			t.Fatalf("submitted session = %s, want %s", tx.SessionId, sessionID)
		}
	case <-time.After(time.Second):
		t.Fatal("pre-trigger session did not submit a commitment for the trigger block")
	}
	cancel()
	waitForDkgSessionExit(t, worker, sessionID)
}

func TestStartSessionDoesNotRegenerateFinalizedOwnCommitment(t *testing.T) {
	const (
		chain        = "btc"
		vaultID      = uint32(1)
		epochID      = uint64(7)
		sessionID    = "btc_1_7_1"
		localAddress = "miner-a"
	)
	transition := &pb.VaultTransitionState{
		Chain:               chain,
		VaultId:             vaultID,
		EpochId:             epochID,
		SignAlgo:            pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		DkgSessionId:        sessionID,
		DkgStatus:           "COMMITTING",
		DkgN:                1,
		DkgThresholdT:       1,
		NewCommitteeMembers: []string{localAddress},
		DkgCommitDeadline:   10,
		DkgSharingDeadline:  20,
		DkgDisputeDeadline:  30,
	}
	reader := &complaintTestStateReader{data: map[string][]byte{
		keys.KeyFrostVaultTransition(chain, vaultID, epochID): mustSessionProto(t, transition),
		keys.KeyFrostVaultDkgCommit(chain, vaultID, epochID, sessionID, localAddress): mustSessionProto(t, &pb.FrostVaultDkgCommitment{
			MinerAddress: localAddress,
		}),
	}}
	cryptoFactory := &sessionNoCallCryptoFactory{}
	submitter := &sessionCaptureSubmitter{}
	worker := &TransitionWorker{
		stateReader:   reader,
		txSubmitter:   submitter,
		cryptoFactory: cryptoFactory,
		                                                                   
		                                                                      
		                                                                        
		                                                                        
		                                                                     
		localShareStore: &sessionRevealStore{},
		vaultProvider: &sessionThresholdVaultProvider{
			threshold: 1,
			committee: []SignerInfo{{ID: localAddress}},
		},
		localAddress: localAddress,
		Logger:       complaintTestLogger{},
		sessions:     make(map[string]*DKGSession),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := worker.StartSession(ctx, chain, vaultID, epochID, transition.SignAlgo); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	if cryptoFactory.calls != 0 || submitter.commits != 0 || submitter.shares != 0 {
		t.Fatalf("existing commitment must short-circuit before crypto/submit: crypto=%d commits=%d shares=%d",
			cryptoFactory.calls, submitter.commits, submitter.shares)
	}
	if session := worker.GetSession(sessionID); session == nil || session.Phase != "REVEAL_ONLY" {
		t.Fatalf("existing commitment must register only a reveal responder: %+v", session)
	}
	cancel()
	waitForDkgSessionExit(t, worker, sessionID)
}

func TestRunSessionRechecksFinalizedOwnCommitment(t *testing.T) {
	const (
		chain        = "btc"
		vaultID      = uint32(1)
		epochID      = uint64(7)
		sessionID    = "btc_1_7_1"
		localAddress = "miner-a"
	)
	reader := &complaintTestStateReader{data: map[string][]byte{
		keys.KeyFrostVaultDkgCommit(chain, vaultID, epochID, sessionID, localAddress): []byte{0x01},
	}}
	cryptoFactory := &sessionNoCallCryptoFactory{}
	submitter := &sessionCaptureSubmitter{}
	session := &DKGSession{
		Chain:     chain,
		VaultID:   vaultID,
		EpochID:   epochID,
		SessionID: sessionID,
	}
	worker := &TransitionWorker{
		stateReader:   reader,
		txSubmitter:   submitter,
		cryptoFactory: cryptoFactory,
		localAddress:  localAddress,
		Logger:        complaintTestLogger{},
		sessions:      map[string]*DKGSession{sessionID: session},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	worker.runSession(ctx, session)
	if cryptoFactory.calls != 0 || submitter.commits != 0 || submitter.shares != 0 {
		t.Fatalf("runSession guard must short-circuit before crypto/submit: crypto=%d commits=%d shares=%d",
			cryptoFactory.calls, submitter.commits, submitter.shares)
	}
	if worker.GetSession(sessionID) != nil {
		t.Fatal("stopped committed session must be removed from the in-memory session map")
	}
}

func TestRestartedDkgTranscriptRespondsToComplaintWithoutRegeneration(t *testing.T) {
	const (
		chain        = "btc"
		vaultID      = uint32(1)
		epochID      = uint64(7)
		sessionID    = "btc_1_7_1"
		localAddress = "miner-a"
		receiver     = "miner-b"
	)
	tests := []struct {
		name          string
		hasMarker     bool
		hasCommitment bool
	}{
		{name: "crash after local transcript before commitment finalization", hasMarker: true},
		{name: "restart after finalized commitment", hasCommitment: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transition := &pb.VaultTransitionState{
				Chain: chain, VaultId: vaultID, EpochId: epochID,
				SignAlgo:     pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
				DkgSessionId: sessionID, DkgStatus: "RESOLVING", DkgN: 2, DkgThresholdT: 2,
				NewCommitteeMembers: []string{localAddress, receiver},
				DkgCommitDeadline:   10, DkgSharingDeadline: 12, DkgDisputeDeadline: 20,
			}
			complaint := &pb.FrostVaultDkgComplaint{
				DealerId: localAddress, ReceiverId: receiver, Status: "PENDING",
			}
			transitionKey := keys.KeyFrostVaultTransition(chain, vaultID, epochID)
			readerData := map[string][]byte{
				transitionKey: mustSessionProto(t, transition),
				keys.KeyFrostVaultTransitionActive(chain, vaultID, epochID):                                []byte(transitionKey),
				keys.KeyFrostVaultDkgComplaint(chain, vaultID, epochID, sessionID, localAddress, receiver): mustSessionProto(t, complaint),
				keys.KeyLatestHeight(): []byte("15"),
			}
			if test.hasCommitment {
				readerData[keys.KeyFrostVaultDkgCommit(chain, vaultID, epochID, sessionID, localAddress)] = mustSessionProto(t, &pb.FrostVaultDkgCommitment{
					MinerAddress: localAddress,
				})
			}
			share := []byte{0x11, 0x22}
			encRand := bytes.Repeat([]byte{0x33}, 32)
			store := &sessionRevealStore{hasSession: test.hasMarker, share: share, encRand: encRand}
			submitter := &sessionCaptureSubmitter{revealC: make(chan *pb.FrostVaultDkgRevealTx, 1)}
			cryptoFactory := &sessionNoCallCryptoFactory{}
			worker := &TransitionWorker{
				stateReader: readerDataReader(readerData), txSubmitter: submitter,
				cryptoFactory: cryptoFactory, localShareStore: store,
				vaultProvider: &sessionThresholdVaultProvider{
					threshold: 2,
					committee: []SignerInfo{{ID: localAddress}, {ID: receiver}},
				},
				localAddress: localAddress, Logger: complaintTestLogger{},
				sessions: make(map[string]*DKGSession), plannedMigrations: make(map[string]bool), warnedExpired: make(map[string]bool),
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			if err := worker.StartPendingSessions(ctx); err != nil {
				t.Fatalf("StartPendingSessions failed: %v", err)
			}
			select {
			case reveal := <-submitter.revealC:
				if reveal.SessionId != sessionID || reveal.ReceiverId != receiver ||
					!bytes.Equal(reveal.Share, share) || !bytes.Equal(reveal.EncRand, encRand) {
					t.Fatalf("unexpected reveal tx: %+v", reveal)
				}
			case <-time.After(time.Second):
				t.Fatal("restarted reveal-only responder did not scan complaint immediately")
			}
			if cryptoFactory.calls != 0 || submitter.commits != 0 || submitter.shares != 0 {
				t.Fatalf("reveal-only recovery regenerated transcript: crypto=%d commits=%d shares=%d",
					cryptoFactory.calls, submitter.commits, submitter.shares)
			}
		})
	}
}

func TestFinalizedScanImmediatelyPokesExistingRevealResponder(t *testing.T) {
	const (
		chain        = "btc"
		vaultID      = uint32(1)
		epochID      = uint64(7)
		sessionID    = "btc_1_7_1"
		localAddress = "miner-a"
		receiver     = "miner-b"
	)
	transition := &pb.VaultTransitionState{
		Chain: chain, VaultId: vaultID, EpochId: epochID,
		SignAlgo:     pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		DkgSessionId: sessionID, DkgStatus: "RESOLVING", DkgN: 2, DkgThresholdT: 2,
		NewCommitteeMembers: []string{localAddress, receiver},
		DkgCommitDeadline:   10, DkgSharingDeadline: 12, DkgDisputeDeadline: 20,
	}
	transitionKey := keys.KeyFrostVaultTransition(chain, vaultID, epochID)
	complaintPrefix := keys.KeyFrostVaultDkgComplaintPrefix(chain, vaultID, epochID, sessionID) + localAddress + "_"
	reader := &finalizedScanStateReader{
		data: map[string][]byte{
			transitionKey: mustSessionProto(t, transition),
			keys.KeyFrostVaultTransitionActive(chain, vaultID, epochID): []byte(transitionKey),
			keys.KeyLatestHeight(): []byte("15"),
		},
		complaintPrefix: complaintPrefix, firstComplaintRun: make(chan struct{}),
	}
	share := []byte{0x11, 0x22}
	encRand := bytes.Repeat([]byte{0x33}, 32)
	store := &sessionRevealStore{hasSession: true, share: share, encRand: encRand}
	submitter := &sessionCaptureSubmitter{revealC: make(chan *pb.FrostVaultDkgRevealTx, 1)}
	worker := &TransitionWorker{
		stateReader: reader, txSubmitter: submitter,
		cryptoFactory: &sessionNoCallCryptoFactory{}, localShareStore: store,
		vaultProvider: &sessionThresholdVaultProvider{
			threshold: 2,
			committee: []SignerInfo{{ID: localAddress}, {ID: receiver}},
		},
		localAddress: localAddress, Logger: complaintTestLogger{},
		sessions: make(map[string]*DKGSession), plannedMigrations: make(map[string]bool), warnedExpired: make(map[string]bool),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := worker.StartPendingSessions(ctx); err != nil {
		t.Fatalf("initial StartPendingSessions failed: %v", err)
	}
	select {
	case <-reader.firstComplaintRun:
	case <-time.After(time.Second):
		t.Fatal("initial reveal responder scan did not complete")
	}

	complaint := &pb.FrostVaultDkgComplaint{DealerId: localAddress, ReceiverId: receiver, Status: "PENDING"}
	reader.Set(keys.KeyFrostVaultDkgComplaint(chain, vaultID, epochID, sessionID, localAddress, receiver), mustSessionProto(t, complaint))
	if err := worker.StartPendingSessions(ctx); err != nil {
		t.Fatalf("finalized-event StartPendingSessions failed: %v", err)
	}
	select {
	case reveal := <-submitter.revealC:
		if reveal.SessionId != sessionID || reveal.ReceiverId != receiver {
			t.Fatalf("unexpected finalized-event reveal: %+v", reveal)
		}
	case <-time.After(time.Second):
		t.Fatal("existing responder waited for 3s ticker instead of finalized-event poke")
	}
}

func TestRestartedDkgPendingShareRetriesDroppedValidationAck(t *testing.T) {
	const (
		chain        = "btc"
		vaultID      = uint32(1)
		epochID      = uint64(7)
		sessionID    = "btc_1_7_1"
		localAddress = "miner-a"
		peerAddress  = "miner-b"
	)
	transition := &pb.VaultTransitionState{
		Chain: chain, VaultId: vaultID, EpochId: epochID,
		SignAlgo:     pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		DkgSessionId: sessionID, DkgStatus: "RESOLVING", DkgN: 2, DkgThresholdT: 2,
		NewCommitteeMembers: []string{localAddress, peerAddress},
		DkgCommitDeadline:   10, DkgSharingDeadline: 12, DkgDisputeDeadline: 20,
	}
	transitionKey := keys.KeyFrostVaultTransition(chain, vaultID, epochID)
	readerData := map[string][]byte{
		transitionKey: mustSessionProto(t, transition),
		keys.KeyFrostVaultTransitionActive(chain, vaultID, epochID): []byte(transitionKey),
		keys.KeyLatestHeight(): []byte("21"),
		keys.KeyFrostVaultDkgCommit(chain, vaultID, epochID, sessionID, localAddress): mustSessionProto(t, &pb.FrostVaultDkgCommitment{
			MinerAddress: localAddress, AI0: []byte{0x01},
		}),
		keys.KeyFrostVaultDkgCommit(chain, vaultID, epochID, sessionID, peerAddress): mustSessionProto(t, &pb.FrostVaultDkgCommitment{
			MinerAddress: peerAddress, AI0: []byte{0x02},
		}),
	}
	store := &sessionRevealStore{hasSession: true, pending: []byte{0x09}}
	submitter := &sessionCaptureSubmitter{
		validationC:   make(chan *pb.FrostVaultDkgValidationSignedTx, 2),
		validationErr: []error{errors.New("dropped ack response"), nil},
	}
	cryptoFactory := &sessionNoCallCryptoFactory{executor: &sessionFullDKGExecutor{}}
	worker := &TransitionWorker{
		stateReader: readerDataReader(readerData), txSubmitter: submitter,
		cryptoFactory: cryptoFactory, localShareStore: store,
		vaultProvider: &sessionThresholdVaultProvider{
			threshold: 2,
			committee: []SignerInfo{{ID: localAddress}, {ID: peerAddress}},
		},
		localIdentity: testLocalIdentity(t, "0000000000000000000000000000000000000000000000000000000000000001"),
		localAddress:  localAddress, Logger: complaintTestLogger{},
		sessions: make(map[string]*DKGSession), plannedMigrations: make(map[string]bool), warnedExpired: make(map[string]bool),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := worker.StartPendingSessions(ctx); err != nil {
		t.Fatalf("first recovery scan failed: %v", err)
	}
	first := waitSessionValidation(t, submitter.validationC)
	if first.SessionId != sessionID || !bytes.Equal(first.NewGroupPubkey, []byte{0x44}) {
		t.Fatalf("unexpected first recovered validation: %+v", first)
	}
	waitForDkgSessionExit(t, worker, sessionID)

	if err := worker.StartPendingSessions(ctx); err != nil {
		t.Fatalf("second recovery scan failed: %v", err)
	}
	second := waitSessionValidation(t, submitter.validationC)
	if second.SessionId != sessionID || !bytes.Equal(second.NewGroupPubkey, first.NewGroupPubkey) ||
		!bytes.Equal(second.MsgHash, first.MsgHash) {
		t.Fatalf("validation retry changed recovered transcript: first=%+v second=%+v", first, second)
	}
	if len(submitter.validations) != 2 {
		t.Fatalf("dropped validation ack was not retried: %d", len(submitter.validations))
	}
	if submitter.commits != 0 || submitter.shares != 0 {
		t.Fatalf("validation recovery regenerated DKG traffic: commits=%d shares=%d", submitter.commits, submitter.shares)
	}
}

func waitSessionValidation(t *testing.T, ch <-chan *pb.FrostVaultDkgValidationSignedTx) *pb.FrostVaultDkgValidationSignedTx {
	t.Helper()
	select {
	case tx := <-ch:
		return tx
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for recovered validation")
		return nil
	}
}

func waitForDkgSessionExit(t *testing.T, worker *TransitionWorker, sessionID string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for worker.GetSession(sessionID) != nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if worker.GetSession(sessionID) != nil {
		t.Fatalf("session %s did not leave recovery map", sessionID)
	}
}

func readerDataReader(data map[string][]byte) StateReader {
	return &complaintTestStateReader{data: data}
}

func TestOwnFinalizedCommitmentMustMatchInMemoryTranscript(t *testing.T) {
	const (
		chain        = "btc"
		vaultID      = uint32(1)
		epochID      = uint64(7)
		sessionID    = "btc_1_7_1"
		localAddress = "miner-a"
	)
	finalizedPoints := [][]byte{{0x01}, {0x02}}
	reader := &complaintTestStateReader{data: map[string][]byte{
		keys.KeyFrostVaultDkgCommit(chain, vaultID, epochID, sessionID, localAddress): mustSessionProto(t, &pb.FrostVaultDkgCommitment{
			MinerAddress:     localAddress,
			SignAlgo:         pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
			CommitmentPoints: finalizedPoints,
			AI0:              finalizedPoints[0],
		}),
	}}
	executor := &sessionCommitmentExecutor{points: [][]byte{{0x01}, {0xff}}}
	worker := &TransitionWorker{
		stateReader:   reader,
		cryptoFactory: &sessionNoCallCryptoFactory{executor: executor},
		localAddress:  localAddress,
	}
	session := &DKGSession{
		Chain:      chain,
		VaultID:    vaultID,
		EpochID:    epochID,
		SessionID:  sessionID,
		SignAlgo:   pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		Polynomial: &sessionPolynomial{},
	}

	if matches, _ := worker.ownFinalizedCommitmentMatchesSession(session); matches {
		t.Fatal("shares must not be submitted for a polynomial that differs from the finalized commitment")
	}
	executor.points = finalizedPoints
	if matches, reason := worker.ownFinalizedCommitmentMatchesSession(session); !matches {
		t.Fatalf("matching finalized commitment rejected: %s", reason)
	}
}

func TestDkgPersistenceFailuresAbortProtocolMessages(t *testing.T) {
	const localAddress = "miner-a"
	executor := &sessionFullDKGExecutor{}
	session := &DKGSession{
		Chain: "btc", VaultID: 1, EpochID: 7, SessionID: "s1",
		SignAlgo:  pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		Committee: []string{localAddress}, MyIndex: 1, Threshold: 1,
		Polynomial: &sessionPolynomial{},
	}

	t.Run("reveal persistence failure aborts commitment", func(t *testing.T) {
		submitter := &sessionCaptureSubmitter{}
		worker := &TransitionWorker{
			stateReader: &complaintTestStateReader{data: map[string][]byte{}},
			txSubmitter: submitter, cryptoFactory: &sessionNoCallCryptoFactory{executor: executor},
			localShareStore: &sessionPersistenceFailureStore{revealErr: errors.New("disk full")},
			localAddress:    localAddress, Logger: complaintTestLogger{},
		}
		if err := worker.submitCommitment(context.Background(), session); err == nil {
			t.Fatal("expected reveal persistence failure")
		}
		if submitter.commits != 0 {
			t.Fatal("commitment must not be submitted without durable reveal material")
		}
	})

	t.Run("pending share persistence failure aborts key validation", func(t *testing.T) {
		worker := &TransitionWorker{
			stateReader:     &complaintTestStateReader{data: map[string][]byte{}},
			cryptoFactory:   &sessionNoCallCryptoFactory{executor: executor},
			localShareStore: &sessionPersistenceFailureStore{pendingErr: errors.New("disk full")},
			localIdentity:   testLocalIdentity(t, "0000000000000000000000000000000000000000000000000000000000000001"),
			localAddress:    localAddress, Logger: complaintTestLogger{},
		}
		if err := worker.generateKey(context.Background(), session); err == nil {
			t.Fatal("expected pending share persistence failure")
		}
		if session.Phase == "KEY_READY" {
			t.Fatal("session must not advance to validation after pending share persistence failure")
		}
	})
}

func TestSubmitSharesSkipsDealerSelfShare(t *testing.T) {
	submitter := &sessionCaptureSubmitter{}
	worker := &TransitionWorker{
		txSubmitter: submitter, localAddress: "miner-a", Logger: complaintTestLogger{},
	}
	session := &DKGSession{
		Chain: "btc", VaultID: 1, EpochID: 7, SessionID: "s1",
		Committee:   []string{"miner-a"},
		LocalShares: map[int][]byte{1: {0x01}}, EncRands: map[int][]byte{1: {0x02}},
	}
	if err := worker.submitShares(context.Background(), session); err != nil {
		t.Fatalf("submitShares failed: %v", err)
	}
	if submitter.shares != 0 {
		t.Fatalf("dealer self-share should not be submitted on chain: %d", submitter.shares)
	}
}

func TestSessionStillCurrentRejectsStaleTranscript(t *testing.T) {
	const (
		chain        = "btc"
		vaultID      = uint32(1)
		epochID      = uint64(7)
		sessionID    = "btc_1_7_1"
		localAddress = "miner-b"
	)
	baseTransition := &pb.VaultTransitionState{
		Chain:               chain,
		VaultId:             vaultID,
		EpochId:             epochID,
		DkgSessionId:        sessionID,
		DkgStatus:           "SHARING",
		NewCommitteeMembers: []string{"miner-a", localAddress, "miner-c"},
		DkgCommitDeadline:   10,
		DkgSharingDeadline:  20,
		DkgDisputeDeadline:  30,
	}

	tests := []struct {
		name   string
		mutate func(*pb.VaultTransitionState)
		want   bool
	}{
		{name: "same active transcript", want: true},
		{
			name: "fresh retry session",
			mutate: func(ts *pb.VaultTransitionState) {
				ts.DkgSessionId = "btc_1_7_2"
				ts.DkgStatus = "NOT_STARTED"
			},
		},
		{
			name: "failed transition",
			mutate: func(ts *pb.VaultTransitionState) {
				ts.DkgStatus = "FAILED"
			},
		},
		{
			name: "local member removed",
			mutate: func(ts *pb.VaultTransitionState) {
				ts.NewCommitteeMembers = []string{"miner-a", "miner-c"}
			},
		},
		{
			name: "committee order changed without new session",
			mutate: func(ts *pb.VaultTransitionState) {
				ts.NewCommitteeMembers = []string{localAddress, "miner-a", "miner-c"}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transition := proto.Clone(baseTransition).(*pb.VaultTransitionState)
			if test.mutate != nil {
				test.mutate(transition)
			}
			reader := &complaintTestStateReader{data: map[string][]byte{
				keys.KeyFrostVaultTransition(chain, vaultID, epochID): mustSessionProto(t, transition),
			}}
			worker := &TransitionWorker{
				stateReader:  reader,
				localAddress: localAddress,
			}
			session := &DKGSession{
				Chain:       chain,
				VaultID:     vaultID,
				EpochID:     epochID,
				SessionID:   sessionID,
				Committee:   []string{"miner-a", localAddress, "miner-c"},
				MyIndex:     2,
				Threshold:   2,
				SignAlgo:    pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
				Commitments: make(map[string][]byte),
			}

			got, reason := worker.sessionStillCurrent(session)
			if got != test.want {
				t.Fatalf("sessionStillCurrent=%v want=%v reason=%q", got, test.want, reason)
			}
			if !got && reason == "" {
				t.Fatal("stale session rejection must include a reason")
			}
		})
	}
}

func mustSessionProto(t *testing.T, message proto.Message) []byte {
	t.Helper()
	data, err := proto.Marshal(message)
	if err != nil {
		t.Fatalf("marshal session test record: %v", err)
	}
	return data
}
