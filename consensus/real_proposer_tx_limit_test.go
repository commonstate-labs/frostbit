package consensus

import (
	"dex/pb"
	"testing"
)

func TestRealBlockProposerAdaptiveTxLimitUsesSoftTarget(t *testing.T) {
	p := &RealBlockProposer{
		targetTxsPerBlock:   512,
		maxTxsPerBlock:      2500,
		maxTxsLimitPerBlock: 2500,
	}

	cases := []struct {
		name    string
		pending int
		want    int
	}{
		{name: "below catchup", pending: 1000, want: 512},
		{name: "moderate backlog", pending: 2048, want: 768},
		{name: "heavy backlog", pending: 4096, want: 1024},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := p.adaptiveTxLimit(tc.pending); got != tc.want {
				t.Fatalf("adaptiveTxLimit(%d) = %d, want %d", tc.pending, got, tc.want)
			}
		})
	}
}

func TestRealBlockProposerSelectNonceReadyTxsPreservesAccountOrderForNonceRequiredTxs(t *testing.T) {
	addr := "alice"
	transferNonce0 := &pb.AnyTx{Content: &pb.AnyTx_Transaction{Transaction: &pb.Transaction{
		Base: &pb.BaseMessage{TxId: "transfer-0", FromAddress: addr, Nonce: 0},
	}}}
	orderNonce1 := &pb.AnyTx{Content: &pb.AnyTx_OrderTx{OrderTx: &pb.OrderTx{
		Base: &pb.BaseMessage{TxId: "order-1", FromAddress: addr, Nonce: 1},
		Op:   pb.OrderOp_ADD,
	}}}

	p := &RealBlockProposer{}
	got := p.selectNonceReadyTxs([]*pb.AnyTx{orderNonce1, transferNonce0}, 2)
	if len(got) != 2 {
		t.Fatalf("selected %d txs, want 2", len(got))
	}
	if got[0].GetTxId() != "transfer-0" || got[1].GetTxId() != "order-1" {
		t.Fatalf("selected order = [%s %s], want [transfer-0 order-1]", got[0].GetTxId(), got[1].GetTxId())
	}
}

func TestRealBlockProposerSelectNonceReadyTxsBypassesProtocolNonce(t *testing.T) {
	addr := "alice"
	blockedNormal := &pb.AnyTx{Content: &pb.AnyTx_Transaction{Transaction: &pb.Transaction{
		Base: &pb.BaseMessage{TxId: "normal-100", FromAddress: addr, Nonce: 100},
	}}}
	dkg := &pb.AnyTx{Content: &pb.AnyTx_FrostVaultDkgCommitTx{FrostVaultDkgCommitTx: &pb.FrostVaultDkgCommitTx{
		Base: &pb.BaseMessage{TxId: "dkg-999", FromAddress: addr, Nonce: 999},
	}}}
	vote := &pb.AnyTx{Content: &pb.AnyTx_WitnessVoteTx{WitnessVoteTx: &pb.WitnessVoteTx{
		Base: &pb.BaseMessage{TxId: "vote-999", FromAddress: addr, Nonce: 999},
	}}}
	certSign := &pb.AnyTx{Content: &pb.AnyTx_CertSignTx{CertSignTx: &pb.CertSignTx{
		Base: &pb.BaseMessage{TxId: "cert-sign-999", FromAddress: addr, Nonce: 999},
	}}}

	p := &RealBlockProposer{}
	got := p.selectNonceReadyTxs([]*pb.AnyTx{blockedNormal, dkg, vote, certSign}, 4)
	if len(got) != 3 {
		t.Fatalf("selected %d txs, want 3", len(got))
	}
	seen := make(map[string]bool, len(got))
	for _, tx := range got {
		seen[tx.GetTxId()] = true
	}
	for _, txID := range []string{"dkg-999", "vote-999", "cert-sign-999"} {
		if !seen[txID] {
			t.Fatalf("selected txs missing %s: %#v", txID, seen)
		}
	}
	if seen["normal-100"] {
		t.Fatal("nonce-blocked transfer should not be selected")
	}
}

func TestProposalTxPriorityTreatsCertSignAsProtocolTx(t *testing.T) {
	tx := &pb.AnyTx{Content: &pb.AnyTx_CertSignTx{CertSignTx: &pb.CertSignTx{
		Base: &pb.BaseMessage{TxId: "cert-sign"},
	}}}
	if got := proposalTxPriority(tx); got != 0 {
		t.Fatalf("CertSignTx proposal priority = %d, want 0", got)
	}
}
