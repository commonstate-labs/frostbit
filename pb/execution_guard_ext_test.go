package pb

import (
	"strings"
	"testing"
)

func guardedTransfer(targetHeight uint64, onlyProposer string) *AnyTx {
	return &AnyTx{Content: &AnyTx_Transaction{Transaction: &Transaction{Base: &BaseMessage{
		TxId:         "guarded-transfer",
		FromAddress:  "alice",
		TargetHeight: targetHeight,
		OnlyProposer: onlyProposer,
	}}}}
}

func TestValidateTxExecutionGuardForAccountTransaction(t *testing.T) {
	tests := []struct {
		name       string
		tx         *AnyTx
		height     uint64
		proposer   string
		wantErrSub string
	}{
		{name: "legacy unrestricted", tx: guardedTransfer(0, ""), height: 12, proposer: "miner-a"},
		{name: "exact match", tx: guardedTransfer(12, "miner-a"), height: 12, proposer: "miner-a"},
		{name: "earlier height rejected", tx: guardedTransfer(12, "miner-a"), height: 11, proposer: "miner-a", wantErrSub: "target_height"},
		{name: "later height rejected", tx: guardedTransfer(12, "miner-a"), height: 13, proposer: "miner-a", wantErrSub: "target_height"},
		{name: "other proposer rejected", tx: guardedTransfer(12, "miner-a"), height: 12, proposer: "miner-b", wantErrSub: "only_proposer"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateTxExecutionGuard(tc.tx, tc.height, tc.proposer)
			if tc.wantErrSub == "" {
				if err != nil {
					t.Fatalf("ValidateTxExecutionGuard() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Fatalf("ValidateTxExecutionGuard() error = %v, want substring %q", err, tc.wantErrSub)
			}
		})
	}
}

func TestValidateTxExecutionGuardIgnoresProtocolNonceExemptTransaction(t *testing.T) {
	tx := &AnyTx{Content: &AnyTx_CertSignTx{CertSignTx: &CertSignTx{Base: &BaseMessage{
		TxId:         "cert-sign",
		FromAddress:  "miner-a",
		TargetHeight: 99,
		OnlyProposer: "miner-a",
	}}}}
	if err := ValidateTxExecutionGuard(tx, 1, "miner-b"); err != nil {
		t.Fatalf("protocol nonce-exempt transaction must ignore account execution guard: %v", err)
	}
}
