package vm

import (
	"testing"

	"dex/frost/artifact"
	"dex/frost/chain/evm"
	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

func TestHasClaimedOldEpochWithdrawUsesPhysicalAccountJobIndex(t *testing.T) {
	sv := NewMockStateView()
	transition := &pb.VaultTransitionState{Chain: "eth", VaultId: 3, EpochId: 8}
	job := &pb.FrostWithdrawJobState{
		JobId: "mixed-active", Chain: "eth", Asset: "", VaultId: 3, KeyEpoch: 7,
		Status: frostWithdrawJobStatusClaimed, WithdrawIds: []string{"withdraw-1"}, BatchSeq: "9",
	}
	data, err := proto.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyFrostWithdrawActiveVaultJob("eth", 3), []byte(job.JobId))
	sv.Set(keys.KeyFrostWithdrawJob(job.JobId), data)

	waiting, err := hasClaimedOldEpochWithdraw(sv, transition)
	if err != nil || !waiting {
		t.Fatalf("mixed physical Vault Claim was not seen by migration gate: waiting=%v err=%v", waiting, err)
	}

	job.KeyEpoch = 8
	data, _ = proto.Marshal(job)
	sv.Set(keys.KeyFrostWithdrawJob(job.JobId), data)
	if waiting, err = hasClaimedOldEpochWithdraw(sv, transition); err == nil || waiting {
		t.Fatalf("wrong-epoch active marker did not fail closed: waiting=%v err=%v", waiting, err)
	}
}

func TestAccountEpochHandoverGateRequiresCombinedWithdrawalArtifact(t *testing.T) {
	sv := NewMockStateView()
	migration, err := storeFrostArtifact(sv, artifact.Metadata{
		Kind: artifact.KindMigration, Chain: "eth", VaultID: 0, Epoch: 2, Height: 10, SourceID: "migration-2",
	}, []byte("signed-migration-proof"))
	if err != nil {
		t.Fatal(err)
	}
	if complete, err := accountEpochHandoverCompleted(sv, "eth", 0, 2); err != nil || complete {
		t.Fatalf("migration proof alone opened next DKG: complete=%v err=%v", complete, err)
	}

	job := &pb.FrostWithdrawJobState{
		JobId: "handover-job", Chain: "eth", VaultId: 0, KeyEpoch: 2, Status: frostWithdrawJobStatusSigned,
		WithdrawIds: []string{"withdraw-2"}, BatchSeq: "1", IncludesHandover: true,
	}
	jobData, err := proto.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyFrostPermanentWithdrawJob(job.JobId), jobData)
	raw := append(append([]byte(nil), evm.WithdrawBatchWithHandoversMethodID()...), make([]byte, 32)...)
	handover, err := storeFrostArtifact(sv, artifact.Metadata{
		Kind: artifact.KindWithdraw, Chain: "eth", VaultID: 0, Epoch: 2, Height: 11, SourceID: job.JobId,
		ParentIDs: []string{migration.Metadata.ArtifactID},
	}, raw)
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyFrostPermanentWithdrawArtifact(job.WithdrawIds[0]), []byte(handover.Metadata.ArtifactID))
	sv.Set(keys.KeyFrostVaultHandoverConsumed("eth", 0, 2), []byte(job.JobId))
	if complete, err := accountEpochHandoverCompleted(sv, "eth", 0, 2); err != nil || !complete {
		t.Fatalf("valid combined handover batch did not open next DKG: complete=%v err=%v", complete, err)
	}
}
