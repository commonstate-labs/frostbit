package vm

import (
	"strings"
	"testing"

	chainpkg "dex/frost/chain"
	"dex/frost/chain/evm"
	"dex/keys"
	"dex/pb"
)

func TestFrostWithdrawSignedRejectsAccountJobsWithoutClaim(t *testing.T) {
	for _, tc := range []struct {
		name      string
		tamperJob bool
		wantError string
	}{
		{name: "canonical-job-without-claim", wantError: "withdrawal Claim is missing or not active"},
		{name: "attacker-selected-job", tamperJob: true, wantError: "withdrawal Claim is missing or not active"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const (
				chainName  = "eth"
				asset      = "USDT"
				withdrawID = "wid_without_claim"
			)
			base := map[string][]byte{keys.KeyFrostWithdraw(withdrawID): mustMarshalProto(t, &pb.FrostWithdrawState{
				WithdrawId: withdrawID, Seq: 1, LiveSeq: 1, Chain: chainName, Asset: asset, VaultId: 0, KeyEpoch: 1,
				To: "0x1111111111111111111111111111111111111111", Amount: "100", GrossAmount: "100",
				FeeAmount: "1", NetAmount: "99", FeeVersion: 1, RequestHeight: 1, Status: string(WithdrawStatusQueued),
			})}
			seedAccountChainSignatureStub(t, base, chainName, asset, 0, []string{withdrawID})
			sv := newReadOnlyBaseStateView(base)
			template, firstSeq, err := buildCanonicalAccountTemplate(sv, chainName, "", 0, 1, []string{withdrawID})
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := evm.FromJSON(template.TemplateData)
			if err != nil {
				t.Fatal(err)
			}
			jobID := chainpkg.ComputeWithdrawJobID(chainName, "", 0, firstSeq, template.TemplateHash, 1)
			if tc.tamperJob {
				jobID = "attacker-selected-job"
			}
			tx := &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: &pb.FrostWithdrawSignedTx{
				Base:  &pb.BaseMessage{TxId: "signed_without_claim", FromAddress: "malicious-member", ExecutedHeight: 100},
				JobId: jobID, SignedPackageBytes: make([]byte, 96), GuardianSigs: [][]byte{make([]byte, 96)}, WithdrawIds: []string{withdrawID},
				VaultId: 0, KeyEpoch: 1, Chain: chainName, Asset: "", BatchSeq: decoded.BatchSeq,
				TemplateData: template.TemplateData, TemplateHash: template.TemplateHash,
			}}}

			if _, receipt, err := (&FrostWithdrawSignedTxHandler{}).DryRun(tx, sv); err == nil || receipt == nil || receipt.Status != "FAILED" || !strings.Contains(receipt.Error, tc.wantError) {
				t.Fatalf("account signed tx without claim should fail: err=%v receipt=%+v", err, receipt)
			}
			withdrawData, _, _ := sv.Get(keys.KeyFrostWithdraw(withdrawID))
			var withdraw pb.FrostWithdrawState
			if err := unmarshalProtoCompat(withdrawData, &withdraw); err != nil {
				t.Fatalf("decode withdraw: %v", err)
			}
			if withdraw.Status != string(WithdrawStatusQueued) || withdraw.JobId != "" {
				t.Fatalf("rejected signed tx mutated withdraw: %+v", &withdraw)
			}
		})
	}
}
