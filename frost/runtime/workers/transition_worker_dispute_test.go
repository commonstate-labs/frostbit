package workers

import (
	"context"
	"testing"

	"dex/frost/protocol"
	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

type complaintTestStateReader struct {
	data map[string][]byte
}

func (r *complaintTestStateReader) Get(key string) ([]byte, bool, error) {
	value, exists := r.data[key]
	return value, exists, nil
}

func (r *complaintTestStateReader) Scan(prefix string, fn func(string, []byte) bool) error {
	for key, value := range r.data {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix && !fn(key, value) {
			break
		}
	}
	return nil
}

type complaintTestDKGExecutor struct {
	DKGExecutor
}

type complaintTestCryptoFactory struct {
	CryptoExecutorFactory
}

func (complaintTestCryptoFactory) NewDKGExecutor(int32) (DKGExecutor, error) {
	return &complaintTestDKGExecutor{}, nil
}

type complaintCaptureSubmitter struct {
	TxSubmitter
	complaints []*pb.FrostVaultDkgComplaintTx
}

func (s *complaintCaptureSubmitter) SubmitDkgComplaintTx(_ context.Context, tx *pb.FrostVaultDkgComplaintTx) error {
	s.complaints = append(s.complaints, tx)
	return nil
}

type complaintTestLogger struct{}

func (complaintTestLogger) Trace(string, ...interface{})   {}
func (complaintTestLogger) Debug(string, ...interface{})   {}
func (complaintTestLogger) Verbose(string, ...interface{}) {}
func (complaintTestLogger) Info(string, ...interface{})    {}
func (complaintTestLogger) Warn(string, ...interface{})    {}
func (complaintTestLogger) Error(string, ...interface{})   {}
func (complaintTestLogger) GetBuffer() []*pb.LogLine       { return nil }

func TestVerifyAndComplainOnlyComplainsForPresentInvalidShare(t *testing.T) {
	const (
		localAddress = "local-miner"
		privateKey   = "1111111111111111111111111111111111111111111111111111111111111111"
		chain        = "btc"
		vaultID      = uint32(1)
		epochID      = uint64(2)
		sessionID    = "dkg-complaint-bond-test"
	)

	reader := &complaintTestStateReader{data: make(map[string][]byte)}
	                                                                   
	                                                                       
	                                                                
	reader.data[keys.KeyFrostVaultDkgCommit(chain, vaultID, epochID, sessionID, "dealer-a")] = mustComplaintProto(t, &pb.FrostVaultDkgCommitment{
		MinerAddress: "dealer-a",
	})
	reader.data[keys.KeyFrostVaultDkgShare(chain, vaultID, epochID, sessionID, "dealer-a", localAddress)] = mustComplaintProto(t, &pb.FrostVaultDkgShare{
		DealerId:   "dealer-a",
		ReceiverId: localAddress,
		Ciphertext: []byte{0x01, 0x02, 0x03},
	})

	submitter := &complaintCaptureSubmitter{}
	worker := &TransitionWorker{
		stateReader:   reader,
		txSubmitter:   submitter,
		cryptoFactory: complaintTestCryptoFactory{},
		localIdentity: testLocalIdentity(t, privateKey),
		localAddress:  localAddress,
		Logger:        complaintTestLogger{},
	}
	session := &DKGSession{
		Chain:     chain,
		VaultID:   vaultID,
		EpochID:   epochID,
		SessionID: sessionID,
		SignAlgo:  pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		MyIndex:   1,
		Committee: []string{localAddress, "dealer-a", "dealer-b"},
	}

	if err := worker.verifyAndComplain(context.Background(), session); err != nil {
		t.Fatalf("verifyAndComplain failed: %v", err)
	}
	if len(submitter.complaints) != 1 {
		t.Fatalf("submitted complaints: got=%d want=1", len(submitter.complaints))
	}

	for i, tx := range submitter.complaints {
		if tx.Bond != protocol.MinDkgComplaintBond {
			t.Fatalf("complaint %d bond: got=%q want=%q", i, tx.Bond, protocol.MinDkgComplaintBond)
		}
		if tx.Base == nil {
			t.Fatalf("complaint %d missing base", i)
		}
		if tx.Base.FromAddress != localAddress {
			t.Fatalf("complaint %d sender: got=%q want=%q", i, tx.Base.FromAddress, localAddress)
		}
		if tx.Base.TxId == "" {
			t.Fatalf("complaint %d missing tx id", i)
		}
		if len(tx.Base.Signature) == 0 {
			t.Fatalf("complaint %d missing signature", i)
		}
		anyTx := &pb.AnyTx{Content: &pb.AnyTx_FrostVaultDkgComplaintTx{FrostVaultDkgComplaintTx: tx}}
		if canonical := anyTx.GenerateTxID(); canonical != tx.Base.TxId {
			t.Fatalf("complaint %d tx id does not cover the submitted bond: got=%q want=%q", i, tx.Base.TxId, canonical)
		}
	}

	if got := submitter.complaints[0].DealerId; got != "dealer-a" {
		t.Fatalf("unexpected complaint dealer: %q", got)
	}
}

func TestRespondToPendingComplaintsRefusesEmptyRevealMaterial(t *testing.T) {
	const (
		chain        = "btc"
		vaultID      = uint32(1)
		epochID      = uint64(7)
		sessionID    = "btc_1_7_1"
		localAddress = "miner-a"
		receiver     = "miner-b"
	)
	complaint := &pb.FrostVaultDkgComplaint{
		DealerId: localAddress, ReceiverId: receiver, Status: "PENDING",
	}
	reader := &complaintTestStateReader{data: map[string][]byte{
		keys.KeyFrostVaultDkgComplaint(chain, vaultID, epochID, sessionID, localAddress, receiver): mustSessionProto(t, complaint),
	}}
	submitter := &sessionCaptureSubmitter{}
	worker := &TransitionWorker{
		stateReader: reader, txSubmitter: submitter,
		localShareStore: &sessionRevealStore{},
		localAddress:    localAddress, Logger: complaintTestLogger{},
	}
	session := &DKGSession{
		Chain: chain, VaultID: vaultID, EpochID: epochID, SessionID: sessionID,
		Committee: []string{localAddress, receiver},
	}

	if err := worker.respondToPendingComplaints(context.Background(), session); err != nil {
		t.Fatalf("respondToPendingComplaints failed: %v", err)
	}
	if len(submitter.reveals) != 0 {
		t.Fatalf("empty local material must not produce RevealTx: %+v", submitter.reveals)
	}
}

func TestVerifyAndComplainSkipsShareWithoutCommitment(t *testing.T) {
	const (
		localAddress = "local-miner"
		privateKey   = "1111111111111111111111111111111111111111111111111111111111111111"
	)
	reader := &complaintTestStateReader{data: make(map[string][]byte)}
	reader.data[keys.KeyFrostVaultDkgShare("btc", 1, 2, "session", "dealer-a", localAddress)] = mustComplaintProto(t, &pb.FrostVaultDkgShare{
		DealerId:   "dealer-a",
		ReceiverId: localAddress,
		Ciphertext: []byte{0x01},
	})
	submitter := &complaintCaptureSubmitter{}
	worker := &TransitionWorker{
		stateReader:   reader,
		txSubmitter:   submitter,
		cryptoFactory: complaintTestCryptoFactory{},
		localIdentity: testLocalIdentity(t, privateKey),
		localAddress:  localAddress,
		Logger:        complaintTestLogger{},
	}
	session := &DKGSession{
		Chain:     "btc",
		VaultID:   1,
		EpochID:   2,
		SessionID: "session",
		SignAlgo:  pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		MyIndex:   1,
		Committee: []string{localAddress, "dealer-a"},
	}

	if err := worker.verifyAndComplain(context.Background(), session); err != nil {
		t.Fatalf("verifyAndComplain failed: %v", err)
	}
	if len(submitter.complaints) != 0 {
		t.Fatalf("share without commitment must not produce complaint, got=%d", len(submitter.complaints))
	}
}

func mustComplaintProto(t *testing.T, message proto.Message) []byte {
	t.Helper()
	data, err := proto.Marshal(message)
	if err != nil {
		t.Fatalf("marshal test record: %v", err)
	}
	return data
}
