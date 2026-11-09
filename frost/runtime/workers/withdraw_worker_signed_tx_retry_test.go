package workers

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"dex/frost/runtime/planning"
	"dex/logs"
	"dex/pb"
	"dex/utils"

	"google.golang.org/protobuf/proto"
)

type signedTxRetrySubmitter struct {
	testTxSubmitter
	mu        sync.Mutex
	attempts  []*pb.FrostWithdrawSignedTx
	failFirst bool
}

func (s *signedTxRetrySubmitter) SubmitWithdrawSignedTx(_ context.Context, tx *pb.FrostWithdrawSignedTx) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts = append(s.attempts, proto.Clone(tx).(*pb.FrostWithdrawSignedTx))
	if s.failFirst && len(s.attempts) == 1 {
		return errors.New("injected submit failure")
	}
	return nil
}

func (s *signedTxRetrySubmitter) snapshot() []*pb.FrostWithdrawSignedTx {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*pb.FrostWithdrawSignedTx, len(s.attempts))
	for i, tx := range s.attempts {
		out[i] = proto.Clone(tx).(*pb.FrostWithdrawSignedTx)
	}
	return out
}

func TestWaitAndSubmitReusesExactSignedTxAfterSubmitFailure(t *testing.T) {
	identity := utils.GenerateTestIdentity(11)
	submitter := &signedTxRetrySubmitter{failFirst: true}
	signing := &testSigningService{result: &SignedPackage{
		SessionID: "job-stable-signed-tx",
		JobID:     "job-stable-signed-tx",
		Signature: []byte("aggregate-signature"),
	}}
	worker := NewWithdrawWorker(
		newTestStateReader(),
		newWorkerRecoveryAdapterFactory(),
		submitter,
		&testLogReporter{},
		signing,
		&testVaultProvider{},
		1,
		identity.Address,
		testLocalIdentity(t, identity.PrivateKeyHex),
		logs.NewNodeLogger("withdraw-signed-retry", 0),
	)
	job := &planning.Job{
		JobID:       "job-stable-signed-tx",
		Chain:       "eth",
		Asset:       "native",
		VaultID:     2,
		KeyEpoch:    3,
		WithdrawIDs: []string{"withdraw-stable-signed-tx"},
		TemplateHash: []byte{
			0x01, 0x02, 0x03,
		},
		TemplateData: []byte("canonical-template"),
	}

	if worker.waitAndSubmit(context.Background(), job, job.JobID) {
		t.Fatal("first submit should report the injected failure")
	}
	if !worker.waitAndSubmit(context.Background(), job, job.JobID) {
		t.Fatal("second submit should reuse the cached transaction and succeed")
	}

	attempts := submitter.snapshot()
	if len(attempts) != 2 {
		t.Fatalf("submit attempts=%d, want 2", len(attempts))
	}
	firstID := attempts[0].GetBase().GetTxId()
	if firstID == "" || attempts[1].GetBase().GetTxId() != firstID {
		t.Fatalf("retry changed txID: first=%q second=%q", firstID, attempts[1].GetBase().GetTxId())
	}
	if attempts[0].GetBase().GetNonce() != attempts[1].GetBase().GetNonce() {
		t.Fatalf("retry changed Base.Nonce: first=%d second=%d", attempts[0].GetBase().GetNonce(), attempts[1].GetBase().GetNonce())
	}
	if !proto.Equal(attempts[0], attempts[1]) {
		t.Fatal("retry did not submit the exact same FrostWithdrawSignedTx")
	}
}

type blockingSigningService struct {
	mu      sync.Mutex
	started int
	startCh chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingSigningService() *blockingSigningService {
	return &blockingSigningService{
		startCh: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (s *blockingSigningService) StartSigningSession(_ context.Context, params *SigningSessionParams) (string, error) {
	s.mu.Lock()
	s.started++
	s.mu.Unlock()
	s.once.Do(func() { close(s.startCh) })
	return params.JobID, nil
}

func (s *blockingSigningService) GetSessionStatus(sessionID string) (*SessionStatus, error) {
	return &SessionStatus{SessionID: sessionID, State: "COLLECTING_NONCES", Progress: 0.25}, nil
}

func (s *blockingSigningService) CancelSession(string) error { return nil }

func (s *blockingSigningService) WaitForCompletion(_ context.Context, sessionID string, _ time.Duration) (*SignedPackage, error) {
	<-s.release
	return &SignedPackage{SessionID: sessionID, JobID: sessionID, Signature: []byte("aggregate-signature")}, nil
}

func (s *blockingSigningService) starts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.started
}

func TestWithdrawWorkerKeepsOnePipelineWhileROASTRetriesInternally(t *testing.T) {
	reader := newTestStateReader()
	job := seedRecoveredAccountClaim(t, reader, "80", "80")
	submitter := newRecoveryTxSubmitter()
	signing := newBlockingSigningService()
	identity := utils.GenerateTestIdentity(12)
	worker := NewWithdrawWorker(
		reader,
		newWorkerRecoveryAdapterFactory(),
		submitter,
		&testLogReporter{},
		signing,
		&recoveryVaultProvider{},
		1,
		"member1",
		testLocalIdentity(t, identity.PrivateKeyHex),
		logs.NewNodeLogger("withdraw-single-pipeline", 0),
	)

	first, err := worker.ProcessOnce(context.Background(), job.Chain, job.Asset)
	if err != nil || first == nil {
		t.Fatalf("first ProcessOnce: job=%+v err=%v", first, err)
	}
	select {
	case <-signing.startCh:
	case <-time.After(2 * time.Second):
		t.Fatal("signing pipeline did not start")
	}

	second, err := worker.ProcessOnce(context.Background(), job.Chain, job.Asset)
	if err != nil {
		t.Fatalf("second ProcessOnce: %v", err)
	}
	if second != nil {
		t.Fatalf("active ROAST job started a second worker pipeline: %+v", second)
	}
	if got := signing.starts(); got != 1 {
		t.Fatalf("signing sessions=%d, want 1", got)
	}

	close(signing.release)
	select {
	case <-submitter.signedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("released signing pipeline did not submit")
	}
}

func TestWithdrawWorkerRestartSkipsRealPendingSignedTx(t *testing.T) {
	reader := newTestStateReader()
	job := seedRecoveredAccountClaim(t, reader, "80", "80")
	seedPendingWithdrawSignedTx(t, reader, job.JobID, "0xpending-before-restart")
	submitter := newRecoveryTxSubmitter()
	signing := &recoverySigningService{}
	worker := NewWithdrawWorker(
		reader,
		newWorkerRecoveryAdapterFactory(),
		submitter,
		&testLogReporter{},
		signing,
		&recoveryVaultProvider{},
		1,
		"member1",
		testLocalIdentity(t, utils.GenerateTestIdentity(13).PrivateKeyHex),
		logs.NewNodeLogger("withdraw-pending-restart", 0),
	)

	worker.processJobAsync(context.Background(), job)
	claims, signed := submitter.counts()
	started, _ := signing.snapshot()
	if claims != 0 || signed != 0 || started != 0 {
		t.Fatalf("restart rebuilt a pending signed tx: claims=%d signed=%d starts=%d", claims, signed, started)
	}
	if cached := worker.cachedWithdrawSignedTx(job.JobID); cached == nil || cached.GetBase().GetTxId() != "0xpending-before-restart" {
		t.Fatalf("restart did not recover real pending transaction: %+v", cached)
	}
}
