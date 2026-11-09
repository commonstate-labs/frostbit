package workers

import (
	"bytes"
	"context"
	chainpkg "dex/frost/chain"
	"dex/frost/chain/evm"
	"dex/frost/core/curve"
	"dex/keys"
	"dex/pb"
	"math/big"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
)

type transitionCaptureSubmitter struct {
	transitionTx *pb.FrostVaultTransitionSignedTx
	submitCalls  int
}

func (s *transitionCaptureSubmitter) Submit(tx any) (txID string, err error) {
	return "test", nil
}

func (s *transitionCaptureSubmitter) SubmitDkgCommitTx(ctx context.Context, tx *pb.FrostVaultDkgCommitTx) error {
	return nil
}

func (s *transitionCaptureSubmitter) SubmitDkgShareTx(ctx context.Context, tx *pb.FrostVaultDkgShareTx) error {
	return nil
}

func (s *transitionCaptureSubmitter) SubmitDkgComplaintTx(ctx context.Context, tx *pb.FrostVaultDkgComplaintTx) error {
	return nil
}

func (s *transitionCaptureSubmitter) SubmitDkgRevealTx(ctx context.Context, tx *pb.FrostVaultDkgRevealTx) error {
	return nil
}

func (s *transitionCaptureSubmitter) SubmitDkgValidationSignedTx(ctx context.Context, tx *pb.FrostVaultDkgValidationSignedTx) error {
	return nil
}

func (s *transitionCaptureSubmitter) SubmitTransitionSignedTx(ctx context.Context, tx *pb.FrostVaultTransitionSignedTx) error {
	s.submitCalls++
	s.transitionTx = tx
	return nil
}

func (s *transitionCaptureSubmitter) SubmitWithdrawClaimTx(ctx context.Context, tx *pb.FrostWithdrawClaimTx) error {
	return nil
}

func (s *transitionCaptureSubmitter) SubmitWithdrawSignedTx(ctx context.Context, tx *pb.FrostWithdrawSignedTx) error {
	return nil
}

func mustMarshalMsg(t *testing.T, msg proto.Message) []byte {
	t.Helper()
	data, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	return data
}

func TestPlanMigrationJobsSignsOnlyFinalizedEVMIntent(t *testing.T) {
	reader := newTestStateReader()
	chain := "eth"
	vaultID := uint32(0)
	epochID := uint64(2)

	contract := "0x1111111111111111111111111111111111111111"
	guardian, _ := evm.CanonicalBN128PublicKey(curve.NewBN256Group().SerializePoint(curve.NewBN256Group().ScalarBaseMult(big.NewInt(29))))
	reader.data[keys.KeyFrostVaultConfig(chain, 0)] = mustMarshalMsg(t, &pb.FrostVaultConfig{AuthorityProtocol: 3, DepositDexChainId: 51025, GuardianPubkey: guardian, Chain: chain, VaultRefs: []string{contract}, EvmChainId: 1})
	newPub := make([]byte, 64)
	newPub[31] = 1
	newPub[63] = 2
	canonical, _, err := evm.BuildMigrationTemplateV2(evm.MigrationV2Params{Chain: chain, ChainID: 1, DexChainID: 51025, ContractAddr: contract, VaultID: vaultID, OldEpoch: 1, NewEpoch: epochID, NewGroupPubkey: newPub, GuardianPubkey: guardian, FrozenNextBatchSeq: "1"})
	if err != nil {
		t.Fatal(err)
	}
	intent := &pb.FrostVaultMigrationState{
		MigrationId: chainpkg.MigrationID(chain, vaultID, epochID), Chain: chain, VaultId: vaultID,
		OldEpoch: 1, NewEpoch: epochID, OldGroupPubkey: append([]byte(nil), newPub...), NewGroupPubkey: newPub,
		TemplateHash: canonical.TemplateHash, TemplateData: canonical.TemplateData, IntentHash: canonical.SigHashes[0],
		AuthorityProtocol: 3, FrozenNextBatchSeq: "1", Status: "INTENT_FINALIZED", IntentHeight: 10,
	}
	transition := &pb.VaultTransitionState{
		Chain: chain, VaultId: vaultID, EpochId: epochID, SignAlgo: pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128,
		DkgStatus: "KEY_READY", Lifecycle: "DRAINING", OldCommitteeMembers: []string{"local_addr"},
		OldGroupPubkey: intent.OldGroupPubkey, NewGroupPubkey: newPub, DkgThresholdT: 1,
		MigrationJobId: intent.MigrationId, MigrationStatus: "INTENT_FINALIZED",
		MigrationTemplateHash: intent.TemplateHash, MigrationIntentHash: intent.IntentHash,
	}
	reader.data[keys.KeyFrostVaultTransition(chain, vaultID, epochID)] = mustMarshalMsg(t, transition)
	reader.data[keys.KeyFrostVaultMigration(chain, vaultID, epochID)] = mustMarshalMsg(t, intent)
	reader.data[keys.KeyLatestHeight()] = []byte("11")

	signature := make([]byte, evm.SchnorrSigWithYSize)
	signature[31] = 1
	signature[63] = 2
	signature[95] = 1
	submitter := &transitionCaptureSubmitter{}
	worker := &TransitionWorker{
		stateReader: reader, txSubmitter: submitter,
		signingService: &testSigningService{result: &SignedPackage{Signature: signature, Signatures: [][]byte{signature}, GuardianSignatures: [][]byte{signature}}},
		vaultProvider: &sessionThresholdVaultProvider{
			threshold: 1,
			committee: []SignerInfo{{ID: "local_addr", Index: 1, PublicKey: []byte{1}}},
		},
		localAddress:  "local_addr",
		localIdentity: testLocalIdentity(t, "1111111111111111111111111111111111111111111111111111111111111111"),
		signTimeout:   time.Second,
	}

	if err := worker.PlanMigrationJobs(context.Background(), chain, vaultID, epochID); err != nil {
		t.Fatalf("PlanMigrationJobs failed: %v", err)
	}
	if submitter.transitionTx == nil {
		t.Fatalf("expected transition signed tx submitted")
	}
	if submitter.transitionTx.EpochId != epochID {
		t.Fatalf("unexpected epoch id: got=%d want=%d", submitter.transitionTx.EpochId, epochID)
	}
	if submitter.transitionTx.OldVaultId != vaultID || submitter.transitionTx.NewVaultId != vaultID {
		t.Fatalf("unexpected vault ids old=%d new=%d", submitter.transitionTx.OldVaultId, submitter.transitionTx.NewVaultId)
	}
	if !bytes.Equal(submitter.transitionTx.MsgHash, intent.IntentHash) {
		t.Fatalf("msg hash must equal finalized handover proof hash: got=%x want=%x", submitter.transitionTx.MsgHash, intent.IntentHash)
	}
	if !bytes.Equal(submitter.transitionTx.TemplateHash, intent.TemplateHash) || !bytes.Equal(submitter.transitionTx.TemplateData, intent.TemplateData) || len(submitter.transitionTx.RawTx) != 0 {
		t.Fatalf("submitted transaction must carry only the finalized handover proof, without standalone calldata: %+v", submitter.transitionTx)
	}
	if !bytes.Equal(submitter.transitionTx.Signature, signature) {
		t.Fatalf("signature mismatch: got=%x want=%x", submitter.transitionTx.Signature, signature)
	}
	if submitter.transitionTx.Base == nil || submitter.transitionTx.Base.TxId == "" {
		t.Fatalf("missing tx base/txid")
	}
}

func TestMigrationRoundCoordinatorUsesOneSharedJobPerRound(t *testing.T) {
	committee := makeCommittee("old-a", "old-b", "old-c")
	provider := &committeeVaultProvider{members: committee}
	intent := &pb.FrostVaultMigrationState{
		MigrationId: "migration_btc_0_2_test", Chain: "btc", VaultId: 0,
		OldEpoch: 1, NewEpoch: 2, IntentHeight: 10,
	}
	sharedJobID := ""
	coordinators := 0
	for _, member := range committee {
		reader := newTestStateReader()
		reader.data[keys.KeyLatestHeight()] = []byte("25")
		worker := &TransitionWorker{stateReader: reader, vaultProvider: provider, localAddress: string(member.ID)}
		jobID, shouldCoordinate, err := worker.migrationRoundCoordinatorJob(intent)
		if err != nil || jobID == "" {
			t.Fatalf("member %s did not resolve shared retry job: job=%q coordinate=%v err=%v", member.ID, jobID, shouldCoordinate, err)
		}
		if sharedJobID == "" {
			sharedJobID = jobID
		} else if jobID != sharedJobID {
			t.Fatalf("members resolved different jobs: got=%s want=%s", jobID, sharedJobID)
		}
		if shouldCoordinate {
			coordinators++
		}
	}
	if coordinators != 1 {
		t.Fatalf("shared round job selected %d coordinators, want 1", coordinators)
	}
	reader := newTestStateReader()
	reader.data[keys.KeyLatestHeight()] = []byte("25")
	outsider := &TransitionWorker{stateReader: reader, vaultProvider: provider, localAddress: "outsider"}
	if jobID, shouldCoordinate, err := outsider.migrationRoundCoordinatorJob(intent); err != nil || shouldCoordinate || jobID != "" {
		t.Fatalf("outsider received migration job: job=%q coordinate=%v err=%v", jobID, shouldCoordinate, err)
	}
}

func TestPlanMigrationOnceRetriesAtMostEveryThreeFinalizedBlocks(t *testing.T) {
	reader := newTestStateReader()
	reader.data[keys.KeyLatestHeight()] = []byte("11")
	worker := &TransitionWorker{
		stateReader:          reader,
		plannedMigrations:    map[string]bool{},
		migrationLastAttempt: map[string]uint64{"btc_0_2": 10},
	}
	transition := &pb.VaultTransitionState{Chain: "btc", VaultId: 0, EpochId: 2}
	if err := worker.planMigrationOnce(context.Background(), transition); err != nil {
		t.Fatalf("retry inside three-block window should be throttled: %v", err)
	}
	reader.data[keys.KeyLatestHeight()] = []byte("13")
	if err := worker.planMigrationOnce(context.Background(), transition); err == nil || err.Error() != "transition state not found" {
		t.Fatalf("retry at three-block boundary did not run: %v", err)
	}
}

func TestPlanMigrationJobsResubmitsExactCachedPackageWithoutResigning(t *testing.T) {
	reader := newTestStateReader()
	intent := &pb.FrostVaultMigrationState{MigrationId: "migration-btc-0-2", Chain: "btc", VaultId: 0, OldEpoch: 1, NewEpoch: 2, Status: "INTENT_FINALIZED", TemplateHash: []byte{1}, IntentHash: []byte{2}}
	transition := &pb.VaultTransitionState{Chain: "btc", VaultId: 0, EpochId: 2, DkgStatus: "KEY_READY", Lifecycle: "DRAINING", MigrationStatus: "INTENT_FINALIZED", MigrationJobId: intent.MigrationId, MigrationTemplateHash: intent.TemplateHash, MigrationIntentHash: intent.IntentHash, OldCommitteeMembers: []string{"local_addr"}}
	reader.data[keys.KeyFrostVaultTransition("btc", 0, 2)] = mustMarshalMsg(t, transition)
	reader.data[keys.KeyFrostVaultMigration("btc", 0, 2)] = mustMarshalMsg(t, intent)
	submitter := &transitionCaptureSubmitter{}
	worker := &TransitionWorker{stateReader: reader, txSubmitter: submitter, localAddress: "local_addr"}
	cached := &pb.FrostVaultTransitionSignedTx{Base: &pb.BaseMessage{TxId: "stable-migration-tx"}, MigrationId: intent.MigrationId}
	worker.rememberMigrationSignedTx(intent.MigrationId, cached)

	for i := 0; i < 2; i++ {
		if err := worker.PlanMigrationJobs(context.Background(), "btc", 0, 2); err != nil {
			t.Fatal(err)
		}
	}
	if submitter.submitCalls != 2 || submitter.transitionTx.GetBase().GetTxId() != "stable-migration-tx" {
		t.Fatalf("cached package was not retried exactly: calls=%d tx=%+v", submitter.submitCalls, submitter.transitionTx)
	}
}
