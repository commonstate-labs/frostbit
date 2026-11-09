package vm

import (
	"testing"

	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

func TestFrostWithdrawSignedCompletedJobIgnoresArchiveAndLegacyCount(t *testing.T) {
	const (
		jobID      = "completed-job"
		withdrawID = "completed-withdraw"
	)

	tx := &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: &pb.FrostWithdrawSignedTx{
		Base:               &pb.BaseMessage{TxId: "completed-job-resubmit", FromAddress: "coordinator-2", ExecutedHeight: 200},
		JobId:              jobID,
		SignedPackageBytes: []byte("another-valid-package"),
		WithdrawIds:        []string{withdrawID},
		VaultId:            1,
		KeyEpoch:           1,
		Chain:              "btc",
		Asset:              "BTC",
	}}}

	archivedPackage, err := proto.Marshal(&pb.FrostSignedPackage{
		JobId:              jobID,
		Idx:                0,
		SignedPackageBytes: []byte("first-package"),
		SubmitHeight:       100,
		Submitter:          "coordinator-1",
	})
	if err != nil {
		t.Fatalf("marshal archived package: %v", err)
	}
	archivedWithdraw, err := proto.Marshal(&pb.FrostWithdrawState{
		WithdrawId: withdrawID,
		JobId:      jobID,
		Status:     string(WithdrawStatusSigned),
		Chain:      "btc",
		Asset:      "BTC",
	})
	if err != nil {
		t.Fatalf("marshal archived withdraw: %v", err)
	}
	archivedJob, err := proto.Marshal(&pb.FrostWithdrawJobState{
		JobId:       jobID,
		WithdrawIds: []string{withdrawID},
		Status:      frostWithdrawJobStatusSigned,
		Chain:       "btc",
		Asset:       "BTC",
	})
	if err != nil {
		t.Fatalf("marshal archived job: %v", err)
	}

	tests := []struct {
		name        string
		withArchive bool
	}{
		{name: "checkpoint_without_archive"},
		{name: "full_node_with_archive", withArchive: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sv := NewMockStateView()
			if tt.withArchive {
				sv.Set(keys.KeyArchiveFrostSignedPackage(jobID, 0), archivedPackage)
				sv.Set(keys.KeyArchiveFrostWithdraw(withdrawID), archivedWithdraw)
				sv.Set(keys.KeyArchiveFrostWithdrawJob(jobID), archivedJob)
			}
			ops, receipt, err := (&FrostWithdrawSignedTxHandler{}).DryRun(tx, sv)
			if err == nil {
				t.Fatal("completed job resubmit unexpectedly succeeded")
			}
			if receipt == nil || receipt.Status != "FAILED" {
				t.Fatalf("receipt = %+v, want FAILED", receipt)
			}
			wantError := "withdraw not found: " + withdrawID
			if receipt.Error != wantError || err.Error() != wantError {
				t.Fatalf("error = receipt %q / returned %q, want %q", receipt.Error, err.Error(), wantError)
			}
			if len(ops) != 0 {
				t.Fatalf("completed job resubmit returned %d writes, want 0", len(ops))
			}
		})
	}
}
