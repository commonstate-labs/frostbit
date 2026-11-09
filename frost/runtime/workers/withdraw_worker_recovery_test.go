package workers

import (
	"bytes"
	"context"
	"dex/frost/core/curve"
	"golang.org/x/crypto/sha3"
	"math/big"
	"sync"
	"testing"
	"time"

	chainpkg "dex/frost/chain"
	"dex/frost/chain/evm"
	"dex/frost/runtime/planning"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"dex/utils"

	"google.golang.org/protobuf/proto"
)

type recoveryTxSubmitter struct {
	testTxSubmitter
	mu          sync.Mutex
	claimCount  int
	signedCount int
	signedCh    chan *pb.FrostWithdrawSignedTx
}

func newRecoveryTxSubmitter() *recoveryTxSubmitter {
	return &recoveryTxSubmitter{signedCh: make(chan *pb.FrostWithdrawSignedTx, 1)}
}

func (s *recoveryTxSubmitter) SubmitWithdrawClaimTx(context.Context, *pb.FrostWithdrawClaimTx) error {
	s.mu.Lock()
	s.claimCount++
	s.mu.Unlock()
	return nil
}

func (s *recoveryTxSubmitter) SubmitWithdrawSignedTx(_ context.Context, tx *pb.FrostWithdrawSignedTx) error {
	s.mu.Lock()
	s.signedCount++
	s.mu.Unlock()
	select {
	case s.signedCh <- tx:
	default:
	}
	return nil
}

func (s *recoveryTxSubmitter) counts() (claims, signed int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.claimCount, s.signedCount
}

type recoverySigningService struct {
	mu      sync.Mutex
	started int
	params  *SigningSessionParams
}

func (s *recoverySigningService) StartSigningSession(_ context.Context, params *SigningSessionParams) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.started++
	cloned := *params
	cloned.Messages = cloneRecoveryMatrix(params.Messages)
	cloned.Tweaks = cloneRecoveryMatrix(params.Tweaks)
	s.params = &cloned
	return params.JobID, nil
}

func (s *recoverySigningService) GetSessionStatus(sessionID string) (*SessionStatus, error) {
	return &SessionStatus{SessionID: sessionID, State: "COMPLETE", Progress: 1}, nil
}

func (s *recoverySigningService) CancelSession(string) error { return nil }

func (s *recoverySigningService) WaitForCompletion(_ context.Context, sessionID string, _ time.Duration) (*SignedPackage, error) {
	s.mu.Lock()
	msg := bytes.Clone(s.params.Messages[0])
	s.mu.Unlock()
	return &SignedPackage{SessionID: sessionID, JobID: sessionID, Signature: workerAccountSignature(msg, 11), GuardianSignatures: [][]byte{workerAccountSignature(msg, 29)}}, nil
}

func (s *recoverySigningService) snapshot() (int, *SigningSessionParams) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.params == nil {
		return s.started, nil
	}
	cloned := *s.params
	cloned.Messages = cloneRecoveryMatrix(s.params.Messages)
	cloned.Tweaks = cloneRecoveryMatrix(s.params.Tweaks)
	return s.started, &cloned
}

func cloneRecoveryMatrix(in [][]byte) [][]byte {
	out := make([][]byte, len(in))
	for i := range in {
		out[i] = append([]byte(nil), in[i]...)
	}
	return out
}

type recoveryVaultProvider struct{ testVaultProvider }

func (*recoveryVaultProvider) VaultCommittee(string, uint32, uint64) ([]SignerInfo, error) {
	return []SignerInfo{{ID: "member1", Index: 0}}, nil
}

func (*recoveryVaultProvider) CalculateThreshold(string, uint32) (int, error) {
	return 1, nil
}

func TestWithdrawWorkerRestartResumesFinalizedAccountClaim(t *testing.T) {
	t.Parallel()

	reader := newTestStateReader()
	finalized := seedRecoveredAccountClaim(t, reader, "80", "80")
	submitter := newRecoveryTxSubmitter()
	signing := &recoverySigningService{}
	identity := utils.GenerateTestIdentity(1)
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
		logs.NewNodeLogger("withdraw-recovery", 0),
	)

	recovered, err := worker.ProcessOnce(context.Background(), finalized.Chain, finalized.Asset)
	if err != nil {
		t.Fatalf("ProcessOnce after restart: %v", err)
	}
	if recovered == nil || recovered.JobID != finalized.JobID {
		t.Fatalf("recovered job = %+v, want original %s", recovered, finalized.JobID)
	}

	var signedTx *pb.FrostWithdrawSignedTx
	select {
	case signedTx = <-submitter.signedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("recovered job did not continue to signed submission")
	}
	claims, signed := submitter.counts()
	if claims != 0 {
		t.Fatalf("restart submitted %d Claim txs; finalized Claim must not be submitted again", claims)
	}
	if signed != 1 || signedTx.JobId != finalized.JobID {
		t.Fatalf("signed submissions=%d tx=%+v, want one for original job", signed, signedTx)
	}
	started, params := signing.snapshot()
	if started != 1 || params == nil || params.JobID != finalized.JobID || params.VaultID != finalized.VaultID ||
		params.KeyEpoch != finalized.KeyEpoch {
		t.Fatalf("signing restart used different intent: started=%d params=%+v original=%+v", started, params, finalized)
	}
	if len(params.Messages) != 1 {
		t.Fatalf("recovered signing messages=%d, want 1", len(params.Messages))
	}

	                                                                           
	                                                                        
	pendingBytes, err := proto.Marshal(&pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: signedTx}})
	if err != nil {
		t.Fatalf("marshal pending signed tx: %v", err)
	}
	reader.data[keys.KeyPendingAnyTx(signedTx.GetBase().GetTxId())] = pendingBytes
	again, err := worker.ProcessOnce(context.Background(), finalized.Chain, finalized.Asset)
	if err != nil {
		t.Fatalf("duplicate recovery tick: %v", err)
	}
	if again != nil {
		t.Fatalf("duplicate recovery tick returned job %+v", again)
	}
	claims, signed = submitter.counts()
	started, _ = signing.snapshot()
	if claims != 0 || signed != 1 || started != 1 {
		t.Fatalf("duplicate recovery tick escaped cooldown: claims=%d signed=%d starts=%d", claims, signed, started)
	}
}

func TestWithdrawWorkerRejectsRecoveredAccountClaimWithoutReservation(t *testing.T) {
	t.Parallel()

	reader := newTestStateReader()
	job := seedRecoveredAccountClaim(t, reader, "", "")
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
		testLocalIdentity(t, utils.GenerateTestIdentity(1).PrivateKeyHex),
		logs.NewNodeLogger("withdraw-recovery-no-reservation", 0),
	)

	                                                                          
	                                                         
	worker.processJobAsync(context.Background(), job)
	claims, signed := submitter.counts()
	started, _ := signing.snapshot()
	if claims != 0 || signed != 0 || started != 0 {
		t.Fatalf("legacy unreserved Claim reached signing: claims=%d signed=%d starts=%d", claims, signed, started)
	}
}

func TestWithdrawWorkerNonCoordinatorDoesNotResumeFinalizedClaim(t *testing.T) {
	t.Parallel()

	reader := newTestStateReader()
	job := seedRecoveredAccountClaim(t, reader, "80", "80")
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
		"not-the-coordinator",
		testLocalIdentity(t, utils.GenerateTestIdentity(2).PrivateKeyHex),
		logs.NewNodeLogger("withdraw-recovery-non-coordinator", 0),
	)

	for tick := 0; tick < 2; tick++ {
		planned, err := worker.ProcessOnce(context.Background(), job.Chain, job.Asset)
		if err != nil {
			t.Fatalf("non-coordinator ProcessOnce tick %d: %v", tick, err)
		}
		if planned != nil {
			t.Fatalf("non-coordinator tick %d reported a created job: %+v", tick, planned)
		}
	}
	if _, active := worker.activeJobs.Load(job.JobID); active {
		t.Fatalf("non-coordinator job %s entered activeJobs", job.JobID)
	}
	claims, signed := submitter.counts()
	started, _ := signing.snapshot()
	if claims != 0 || signed != 0 || started != 0 {
		t.Fatalf("non-coordinator resumed finalized Claim: claims=%d signed=%d starts=%d", claims, signed, started)
	}
}

func seedRecoveredAccountClaim(t *testing.T, reader *testStateReader, reservedAmount, aggregateReserved string) *planning.Job {
	t.Helper()

	const (
		chainName  = "eth"
		asset      = "native"
		withdrawID = "withdraw-restart-1"
		vaultID    = uint32(2)
		keyEpoch   = uint64(9)
		firstSeq   = uint64(7)
		amount     = "80"
	)
	result, err := evm.NewETHAdapter().BuildWithdrawTemplate(chainpkg.WithdrawTemplateParams{AuthorityProtocol: 3, DexChainID: 51025, GuardianPubkey: workerAccountGuardianKey(),
		Chain:        chainName,
		Asset:        asset,
		VaultID:      vaultID,
		KeyEpoch:     keyEpoch,
		BatchSeq:     "1",
		WithdrawIDs:  []string{withdrawID},
		Nonce:        firstSeq,
		ContractAddr: "0x00000000000000000000000000000000000000aa",
		Outputs: []chainpkg.WithdrawOutput{{
			WithdrawID:   withdrawID,
			To:           "0x00000000000000000000000000000000000000bb",
			Amount:       amount,
			BroadcastFee: "1",
		}},
	})
	if err != nil {
		t.Fatalf("build recovery template: %v", err)
	}
	decoded, err := evm.FromJSON(result.TemplateData)
	if err != nil {
		t.Fatalf("decode recovery template: %v", err)
	}
	jobID := chainpkg.ComputeWithdrawJobID(chainName, "", vaultID, firstSeq, result.TemplateHash, keyEpoch)
	jobState := &pb.FrostWithdrawJobState{
		JobId:          jobID,
		Chain:          chainName,
		Asset:          "",
		VaultId:        vaultID,
		KeyEpoch:       keyEpoch,
		WithdrawIds:    []string{withdrawID},
		FirstSeq:       firstSeq,
		LastSeq:        firstSeq,
		TemplateHash:   append([]byte(nil), result.TemplateHash...),
		TemplateData:   append([]byte(nil), result.TemplateData...),
		Status:         "CLAIMED",
		ReservedAmount: reservedAmount,
		BatchSeq:       "1",
		RewardTokens:   append([]string(nil), decoded.RewardTokens...),
	}
	withdraw := &pb.FrostWithdrawState{
		WithdrawId:  withdrawID,
		Seq:         firstSeq,
		Chain:       chainName,
		Asset:       asset,
		To:          "0x00000000000000000000000000000000000000bb",
		Amount:      "80",
		GrossAmount: "80",
		FeeAmount:   "1",
		NetAmount:   "79",
		FeeVersion:  1,
		Status:      "SIGNING",
		JobId:       jobID,
		VaultId:     vaultID,
		LiveSeq:     firstSeq,
		KeyEpoch:    keyEpoch,
	}
	reader.data[keys.KeyFrostVaultConfig(chainName, 0)] = mustMarshalRecoveryProto(t, &pb.FrostVaultConfig{AuthorityProtocol: 3, DepositDexChainId: 51025, GuardianPubkey: workerAccountGuardianKey(), Chain: chainName, VaultCount: vaultID + 1})
	reader.data[keys.KeyFrostWithdrawJob(jobID)] = mustMarshalRecoveryProto(t, jobState)
	reader.data[keys.KeyFrostWithdraw(withdrawID)] = mustMarshalRecoveryProto(t, withdraw)
	reader.data[keys.KeyFrostWithdrawActiveVaultJob(chainName, vaultID)] = []byte(jobID)
	reader.data[keys.KeyFrostWithdrawLiveHead(chainName, vaultID)] = []byte("7")
	reader.data[keys.KeyFrostWithdrawLiveTail(chainName, vaultID)] = []byte("7")
	reader.data[keys.KeyFrostWithdrawLivePending(chainName, vaultID)] = []byte("1")
	reader.data[keys.KeyFrostWithdrawLiveIndex(chainName, vaultID, firstSeq)] = []byte(withdrawID)
	if aggregateReserved != "" {
		reader.data[keys.KeyFrostVaultReservedBalance(chainName, asset, vaultID)] = []byte(aggregateReserved)
	}
	return &planning.Job{
		JobID:        jobID,
		Chain:        chainName,
		Asset:        "",
		VaultID:      vaultID,
		KeyEpoch:     keyEpoch,
		WithdrawIDs:  []string{withdrawID},
		TemplateHash: append([]byte(nil), result.TemplateHash...),
		TemplateData: append([]byte(nil), result.TemplateData...),
		FirstSeq:     firstSeq,
		BatchSeq:     "1",
		RewardTokens: append([]string(nil), decoded.RewardTokens...),
	}
}

func newWorkerRecoveryAdapterFactory() *chainpkg.DefaultAdapterFactory {
	factory := chainpkg.NewDefaultAdapterFactory()
	factory.RegisterAdapter(evm.NewETHAdapter())
	return factory
}

func mustMarshalRecoveryProto(t *testing.T, msg proto.Message) []byte {
	t.Helper()
	data, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal recovery proto: %v", err)
	}
	return data
}

func workerAccountGuardianKey() []byte {
	g := curve.NewBN256Group()
	key, _ := evm.CanonicalBN128PublicKey(g.SerializePoint(g.ScalarBaseMult(big.NewInt(29))))
	return key
}
func workerAccountSignature(message []byte, key int64) []byte {
	g := curve.NewBN256Group()
	sk := big.NewInt(key)
	pub, _ := evm.CanonicalBN128PublicKey(g.SerializePoint(g.ScalarBaseMult(sk)))
	raw := g.SerializePoint(g.ScalarBaseMult(sk))
	if !bytes.Equal(raw, pub) {
		sk.Sub(g.Order(), sk)
	}
	nonce := big.NewInt(7919)
	R := g.ScalarBaseMult(nonce)
	h := sha3.NewLegacyKeccak256()
	h.Write(g.SerializePoint(R))
	h.Write(pub)
	h.Write(message)
	e := new(big.Int).SetBytes(h.Sum(nil))
	e.Mod(e, g.Order())
	z := new(big.Int).Mul(e, sk)
	z.Add(z, nonce)
	z.Mod(z, g.Order())
	return append(g.SerializePoint(R), z.FillBytes(make([]byte, 32))...)
}
