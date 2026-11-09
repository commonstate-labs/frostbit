package vm

import (
	"reflect"
	"testing"

	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

func TestBTCIsolationLegacyDeliveredIntentReplaysAfterActivationAndHandover(t *testing.T) {
	sv, signed := btcDualSignedFixture(t)
	first := &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: signed}}
	if _, receipt, err := (&FrostWithdrawSignedTxHandler{}).DryRun(first, sv); err != nil || receipt.Status != "SUCCEED" {
		t.Fatalf("legacy delivery: %v %+v", err, receipt)
	}
	enableBTCIsolationForTest(t, sv)
	job, found, err := readBTCIsolationJob(sv, signed.JobId)
	if err != nil || !found {
		t.Fatalf("permanent job: %v", err)
	}
	vaultRaw, _, _ := sv.Get(keys.KeyFrostVaultState("btc", job.VaultId))
	var vault pb.FrostVaultState
	if err := proto.Unmarshal(vaultRaw, &vault); err != nil {
		t.Fatal(err)
	}
	oldCommittee := append([]string(nil), vault.CommitteeMembers...)
	vault.KeyEpoch++
	vault.CommitteeMembers = []string{"new-member"}
	sv.Set(keys.KeyFrostVaultState("btc", job.VaultId), mustMarshalProto(t, &vault))
	sv.Set(keys.KeyFrostVaultTransition("btc", job.VaultId, job.KeyEpoch+1), mustMarshalProto(t, &pb.VaultTransitionState{Chain: "btc", VaultId: job.VaultId, EpochId: job.KeyEpoch + 1, OldCommitteeMembers: oldCommittee}))
	claim := &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawClaimTx{FrostWithdrawClaimTx: &pb.FrostWithdrawClaimTx{
		Base: &pb.BaseMessage{TxId: "legacy-claim-replay", FromAddress: signed.Base.FromAddress}, JobId: job.JobId,
		Chain: "btc", Asset: job.Asset, VaultId: job.VaultId, KeyEpoch: job.KeyEpoch, WithdrawIds: job.WithdrawIds,
		TemplateHash: job.TemplateHash, TemplateData: job.TemplateData,
	}}}
	before := sv.Diff()
	if _, receipt, err := (&FrostWithdrawClaimTxHandler{}).DryRun(claim, sv); err != nil || receipt.Status != "SUCCEED" {
		t.Fatalf("legacy claim replay: %v %+v", err, receipt)
	}
	if _, receipt, err := (&FrostWithdrawSignedTxHandler{}).DryRun(first, sv); err != nil || receipt.Status != "SUCCEED" {
		t.Fatalf("legacy signed replay: %v %+v", err, receipt)
	}
	noChain := proto.Clone(first).(*pb.AnyTx)
	noChain.GetFrostWithdrawSignedTx().Chain = ""
	if _, receipt, err := (&FrostWithdrawSignedTxHandler{}).DryRun(noChain, sv); err != nil || receipt.Status != "SUCCEED" {
		t.Fatalf("legacy optional chain replay: %v %+v", err, receipt)
	}
	if !reflect.DeepEqual(before, sv.Diff()) {
		t.Fatal("legacy replay recreated spent state")
	}
	bad := proto.Clone(first).(*pb.AnyTx)
	bad.GetFrostWithdrawSignedTx().InputSigs[0][0] ^= 1
	if _, receipt, err := (&FrostWithdrawSignedTxHandler{}).DryRun(bad, sv); err == nil || receipt.Status != "FAILED" {
		t.Fatal("changed legacy signature replay accepted")
	}
	if !reflect.DeepEqual(before, sv.Diff()) {
		t.Fatal("rejected legacy replay changed state")
	}
}

func TestBTCIsolationLegacyActiveRecoveryIndexRemovedBySigned(t *testing.T) {
	sv, signed := btcDualSignedFixture(t)
	enableBTCIsolationForTest(t, sv)
	if err := migrateBTCIsolationV3(sv); err != nil {
		t.Fatal(err)
	}
	if err := processBTCIsolationAmountIndex(sv); err != nil {
		t.Fatal(err)
	}
	index := keys.KeyFrostBtcWithdrawActiveChild(signed.Asset, signed.JobId)
	if _, exists, _ := sv.Get(index); !exists {
		t.Fatal("legacy claim omitted by incremental recovery index")
	}
	if _, receipt, err := (&FrostWithdrawSignedTxHandler{}).DryRun(&pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: signed}}, sv); err != nil || receipt.Status != "SUCCEED" {
		t.Fatalf("legacy finish: %v %+v", err, receipt)
	}
	if _, exists, _ := sv.Get(index); exists {
		t.Fatal("completed legacy job retained active recovery pointer")
	}
	if err := processBTCIsolationAmountIndex(sv); err != nil {
		t.Fatal(err)
	}
	if _, exists, _ := sv.Get(index); exists {
		t.Fatal("backfill resurrected completed legacy job")
	}
}
