package planning

import (
	"bytes"
	"testing"

	chainpkg "dex/frost/chain"
	"dex/frost/chain/evm"
	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

func TestPlanJobWindowRecoversFinalizedMixedClaimBeforeQueuedPlanning(t *testing.T) {
	t.Parallel()

	reader, job := newMixedClaimedRecoveryReader(t, 7)
	planner := NewJobWindowPlanner(reader, newRecoveryAdapterFactory(), 2)
	jobs, logs, err := planner.PlanJobWindow("ETH", "ignored-token-dimension")
	if err != nil {
		t.Fatalf("PlanJobWindow: %v", err)
	}
	if len(logs) != 0 {
		t.Fatalf("recovery unexpectedly produced planning logs: %+v", logs)
	}
	if len(jobs) != 1 {
		t.Fatalf("recovered jobs = %d, want 1", len(jobs))
	}
	got := jobs[0]
	if got.JobID != job.JobId || got.Chain != job.Chain || got.Asset != "" ||
		got.VaultID != job.VaultId || got.KeyEpoch != job.KeyEpoch || got.FirstSeq != job.FirstSeq ||
		got.BatchSeq != job.BatchSeq || got.IncludesHandover != job.IncludesHandover ||
		len(got.WithdrawIDs) != 1 || got.WithdrawIDs[0] != job.WithdrawIds[0] ||
		!bytes.Equal(got.TemplateHash, job.TemplateHash) || !bytes.Equal(got.TemplateData, job.TemplateData) ||
		len(got.RewardTokens) != 1 || got.RewardTokens[0] != job.RewardTokens[0] {
		t.Fatalf("recovered job differs from finalized claim:\n got=%+v\nwant=%+v", got, job)
	}
}

func TestRecoverMixedAccountJobRejectsInvalidFinalizedScope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*pb.FrostWithdrawJobState)
	}{
		{name: "asset dimension", mutate: func(job *pb.FrostWithdrawJobState) { job.Asset = "native" }},
		{name: "batch sequence mismatch", mutate: func(job *pb.FrostWithdrawJobState) { job.BatchSeq = "2" }},
		{name: "non canonical batch sequence", mutate: func(job *pb.FrostWithdrawJobState) { job.BatchSeq = "01" }},
		{name: "reward token mismatch", mutate: func(job *pb.FrostWithdrawJobState) {
			job.RewardTokens[0] = "0x0000000000000000000000000000000000000001"
		}},
		{name: "withdraw order mismatch", mutate: func(job *pb.FrostWithdrawJobState) { job.WithdrawIds[0] = "different-withdraw" }},
		{name: "invalid epoch", mutate: func(job *pb.FrostWithdrawJobState) { job.KeyEpoch = 0 }},
		{name: "invalid live range", mutate: func(job *pb.FrostWithdrawJobState) { job.LastSeq = job.FirstSeq - 1 }},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reader, job := newMixedClaimedRecoveryReader(t, 1)
			tt.mutate(job)
			reader.kvs[keys.KeyFrostWithdrawJob(job.JobId)] = marshalRecoveryProto(t, job)
			if _, err := NewJobWindowPlanner(reader, newRecoveryAdapterFactory(), 1).recoverMixedAccountJob("eth", 2, job.JobId); err == nil {
				t.Fatal("invalid finalized mixed job must fail closed")
			}
		})
	}
}

func TestRecoverMixedAccountJobRejectsTemplateDataHashMismatch(t *testing.T) {
	t.Parallel()

	reader, job := newMixedClaimedRecoveryReader(t, 1)
	job.TemplateData = append([]byte(nil), job.TemplateData...)
	job.TemplateData[len(job.TemplateData)-1] ^= 0x01
	reader.kvs[keys.KeyFrostWithdrawJob(job.JobId)] = marshalRecoveryProto(t, job)

	if _, err := NewJobWindowPlanner(reader, newRecoveryAdapterFactory(), 1).recoverMixedAccountJob("eth", 2, job.JobId); err == nil {
		t.Fatal("template data/hash mismatch must fail closed")
	}
}

func TestPlanJobWindowFailsClosedWhenPhysicalActiveJobIsMissing(t *testing.T) {
	t.Parallel()

	reader, job := newMixedClaimedRecoveryReader(t, 1)
	delete(reader.kvs, keys.KeyFrostWithdrawJob(job.JobId))
	if _, _, err := NewJobWindowPlanner(reader, newRecoveryAdapterFactory(), 1).PlanJobWindow("eth", ""); err == nil {
		t.Fatal("missing physical active job must fail closed")
	}
}

func newMixedClaimedRecoveryReader(t *testing.T, liveSeq uint64) (*testStateReader, *pb.FrostWithdrawJobState) {
	t.Helper()
	const (
		chainName = "eth"
		vaultID   = uint32(2)
		keyEpoch  = uint64(3)
		withdraw  = "withdraw-recovery"
		batchSeq  = "1"
	)
	template := buildRecoveryTemplate(t, withdraw, liveSeq, keyEpoch, batchSeq)
	decoded, err := evm.FromJSON(template.TemplateData)
	if err != nil {
		t.Fatal(err)
	}
	jobID := chainpkg.ComputeWithdrawJobID(chainName, "", vaultID, liveSeq, template.TemplateHash, keyEpoch)
	job := &pb.FrostWithdrawJobState{
		JobId: jobID, Chain: chainName, VaultId: vaultID, KeyEpoch: keyEpoch,
		WithdrawIds: []string{withdraw}, FirstSeq: liveSeq, LastSeq: liveSeq,
		TemplateHash: template.TemplateHash, TemplateData: template.TemplateData,
		Status: "CLAIMED", ReservedAmount: "80", BatchSeq: batchSeq,
		RewardTokens: append([]string(nil), decoded.RewardTokens...), IncludesHandover: true,
	}
	reader := &testStateReader{kvs: map[string][]byte{
		keys.KeyFrostVaultConfig(chainName, 0):                  marshalRecoveryProto(t, &pb.FrostVaultConfig{AuthorityProtocol: 3, DepositDexChainId: 51025, GuardianPubkey: accountPlanningGuardianKey(), Chain: chainName, VaultCount: 3}),
		keys.KeyFrostWithdrawActiveVaultJob(chainName, vaultID): []byte(jobID),
		keys.KeyFrostWithdrawJob(jobID):                         marshalRecoveryProto(t, job),
	}}
	return reader, job
}

func buildRecoveryTemplate(t *testing.T, withdrawID string, liveSeq, keyEpoch uint64, batchSeq string) *chainpkg.TemplateResult {
	t.Helper()
	result, err := evm.NewETHAdapter().BuildWithdrawTemplate(chainpkg.WithdrawTemplateParams{AuthorityProtocol: 3, DexChainID: 51025, GuardianPubkey: accountPlanningGuardianKey(),
		Chain: "eth", VaultID: 2, KeyEpoch: keyEpoch, WithdrawIDs: []string{withdrawID},
		Nonce: liveSeq, BatchSeq: batchSeq,
		ContractAddr: "0x00000000000000000000000000000000000000aa",
		Outputs: []chainpkg.WithdrawOutput{{
			WithdrawID: withdrawID, To: "0x00000000000000000000000000000000000000bb", Amount: "80",
		}},
	})
	if err != nil {
		t.Fatalf("build recovery template: %v", err)
	}
	return result
}

func newRecoveryAdapterFactory() *chainpkg.DefaultAdapterFactory {
	factory := chainpkg.NewDefaultAdapterFactory()
	factory.RegisterAdapter(evm.NewETHAdapter())
	return factory
}

func marshalRecoveryProto(t *testing.T, msg proto.Message) []byte {
	t.Helper()
	data, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal recovery fixture: %v", err)
	}
	return data
}
