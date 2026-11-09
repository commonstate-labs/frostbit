package vm

import (
	"dex/pb"
	"testing"
)

func TestTxRequiresAccountNonceForAccountActions(t *testing.T) {
	base := func(id string) *pb.BaseMessage { return &pb.BaseMessage{TxId: id, FromAddress: "alice", Nonce: 999} }
	cases := []struct {
		name string
		tx   *pb.AnyTx
		want bool
	}{
		{name: "nil", tx: nil, want: false},
		{name: "malformed transfer still requires nonce", tx: &pb.AnyTx{Content: &pb.AnyTx_Transaction{Transaction: &pb.Transaction{Base: &pb.BaseMessage{TxId: "missing-account"}}}}, want: true},
		{name: "issue token", tx: &pb.AnyTx{Content: &pb.AnyTx_IssueTokenTx{IssueTokenTx: &pb.IssueTokenTx{Base: base("issue-token")}}}, want: true},
		{name: "freeze", tx: &pb.AnyTx{Content: &pb.AnyTx_FreezeTx{FreezeTx: &pb.FreezeTx{Base: base("freeze")}}}, want: true},
		{name: "transfer", tx: &pb.AnyTx{Content: &pb.AnyTx_Transaction{Transaction: &pb.Transaction{
			Base: base("transfer"),
		}}}, want: true},
		{name: "spot add order", tx: &pb.AnyTx{Content: &pb.AnyTx_OrderTx{OrderTx: &pb.OrderTx{
			Base: base("spot-add"), Op: pb.OrderOp_ADD,
		}}}, want: true},
		{name: "spot remove order", tx: &pb.AnyTx{Content: &pb.AnyTx_OrderTx{OrderTx: &pb.OrderTx{
			Base: base("spot-remove"), Op: pb.OrderOp_REMOVE,
		}}}, want: true},
		{name: "miner", tx: &pb.AnyTx{Content: &pb.AnyTx_MinerTx{MinerTx: &pb.MinerTx{Base: base("miner")}}}, want: true},
		{name: "witness stake", tx: &pb.AnyTx{Content: &pb.AnyTx_WitnessStakeTx{WitnessStakeTx: &pb.WitnessStakeTx{Base: base("witness-stake")}}}, want: true},
		{name: "witness request", tx: &pb.AnyTx{Content: &pb.AnyTx_WitnessRequestTx{WitnessRequestTx: &pb.WitnessRequestTx{Base: base("witness-request")}}}, want: true},
		{name: "witness challenge", tx: &pb.AnyTx{Content: &pb.AnyTx_WitnessChallengeTx{WitnessChallengeTx: &pb.WitnessChallengeTx{Base: base("witness-challenge")}}}, want: true},
		{name: "witness claim reward", tx: &pb.AnyTx{Content: &pb.AnyTx_WitnessClaimRewardTx{WitnessClaimRewardTx: &pb.WitnessClaimRewardTx{Base: base("witness-claim-reward")}}}, want: true},
		{name: "withdraw request", tx: &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawRequestTx{FrostWithdrawRequestTx: &pb.FrostWithdrawRequestTx{Base: base("withdraw-request")}}}, want: true},
		{name: "withdraw cancel", tx: &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawCancelTx{FrostWithdrawCancelTx: &pb.FrostWithdrawCancelTx{Base: base("withdraw-cancel")}}}, want: true},
		{name: "future order", tx: &pb.AnyTx{Content: &pb.AnyTx_FutureOrderTx{FutureOrderTx: &pb.FutureOrderTx{
			Base: base("future-order"),
		}}}, want: true},
		{name: "future add margin", tx: &pb.AnyTx{Content: &pb.AnyTx_FutureAddMarginTx{FutureAddMarginTx: &pb.FutureAddMarginTx{Base: base("future-add-margin")}}}, want: true},
		{name: "future remove margin", tx: &pb.AnyTx{Content: &pb.AnyTx_FutureRemoveMarginTx{FutureRemoveMarginTx: &pb.FutureRemoveMarginTx{Base: base("future-remove-margin")}}}, want: true},
		{name: "future cancel", tx: &pb.AnyTx{Content: &pb.AnyTx_FutureOrderCancelTx{FutureOrderCancelTx: &pb.FutureOrderCancelTx{
			Base: base("future-cancel"),
		}}}, want: true},
		{name: "mint token", tx: &pb.AnyTx{Content: &pb.AnyTx_MintTokenTx{MintTokenTx: &pb.MintTokenTx{Base: base("mint-token")}}}, want: true},
		{name: "burn token", tx: &pb.AnyTx{Content: &pb.AnyTx_BurnTokenTx{BurnTokenTx: &pb.BurnTokenTx{Base: base("burn-token")}}}, want: true},
		{name: "transfer ownership", tx: &pb.AnyTx{Content: &pb.AnyTx_TransferOwnershipTx{TransferOwnershipTx: &pb.TransferOwnershipTx{Base: base("transfer-ownership")}}}, want: true},
		{name: "set referrer", tx: &pb.AnyTx{Content: &pb.AnyTx_SetReferrerTx{SetReferrerTx: &pb.SetReferrerTx{Base: base("set-referrer")}}}, want: true},
		{name: "miner info", tx: &pb.AnyTx{Content: &pb.AnyTx_MinerInfoTx{MinerInfoTx: &pb.MinerInfoTx{Base: base("miner-info")}}}, want: true},
		{name: "witness claim stake", tx: &pb.AnyTx{Content: &pb.AnyTx_WitnessClaimStakeTx{WitnessClaimStakeTx: &pb.WitnessClaimStakeTx{Base: base("witness-claim-stake")}}}, want: true},
		{name: "withdraw signed", tx: &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: &pb.FrostWithdrawSignedTx{Base: base("withdraw-signed")}}}, want: false},
		{name: "withdraw claim", tx: &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawClaimTx{FrostWithdrawClaimTx: &pb.FrostWithdrawClaimTx{Base: base("withdraw-claim")}}}, want: false},
		{name: "dkg", tx: &pb.AnyTx{Content: &pb.AnyTx_FrostVaultDkgCommitTx{FrostVaultDkgCommitTx: &pb.FrostVaultDkgCommitTx{
			Base: base("dkg"),
		}}}, want: false},
		{name: "dkg share", tx: &pb.AnyTx{Content: &pb.AnyTx_FrostVaultDkgShareTx{FrostVaultDkgShareTx: &pb.FrostVaultDkgShareTx{Base: base("dkg-share")}}}, want: false},
		{name: "dkg complaint", tx: &pb.AnyTx{Content: &pb.AnyTx_FrostVaultDkgComplaintTx{FrostVaultDkgComplaintTx: &pb.FrostVaultDkgComplaintTx{Base: base("dkg-complaint")}}}, want: false},
		{name: "dkg reveal", tx: &pb.AnyTx{Content: &pb.AnyTx_FrostVaultDkgRevealTx{FrostVaultDkgRevealTx: &pb.FrostVaultDkgRevealTx{Base: base("dkg-reveal")}}}, want: false},
		{name: "dkg validation", tx: &pb.AnyTx{Content: &pb.AnyTx_FrostVaultDkgValidationSignedTx{FrostVaultDkgValidationSignedTx: &pb.FrostVaultDkgValidationSignedTx{Base: base("dkg-validation")}}}, want: false},
		{name: "vault transition", tx: &pb.AnyTx{Content: &pb.AnyTx_FrostVaultTransitionSignedTx{FrostVaultTransitionSignedTx: &pb.FrostVaultTransitionSignedTx{Base: base("vault-transition")}}}, want: false},
		{name: "witness vote", tx: &pb.AnyTx{Content: &pb.AnyTx_WitnessVoteTx{WitnessVoteTx: &pb.WitnessVoteTx{
			Base: base("vote"),
		}}}, want: false},
		{name: "arbitration vote", tx: &pb.AnyTx{Content: &pb.AnyTx_ArbitrationVoteTx{ArbitrationVoteTx: &pb.ArbitrationVoteTx{
			Base: base("arb-vote"),
		}}}, want: false},
		{name: "cert sign", tx: &pb.AnyTx{Content: &pb.AnyTx_CertSignTx{CertSignTx: &pb.CertSignTx{
			Base: base("cert-sign"),
		}}}, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := txRequiresAccountNonce(tc.tx); got != tc.want {
				t.Fatalf("txRequiresAccountNonce() = %v, want %v", got, tc.want)
			}
		})
	}
}
