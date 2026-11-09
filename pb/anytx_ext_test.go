package pb

import "testing"

func TestAnyTxWithdrawCancelBaseAndKind(t *testing.T) {
	base := &BaseMessage{TxId: "cancel-tx", FromAddress: "alice"}
	tx := &AnyTx{Content: &AnyTx_FrostWithdrawCancelTx{FrostWithdrawCancelTx: &FrostWithdrawCancelTx{
		Base: base, WithdrawId: "withdraw-1",
	}}}
	if tx.GetBase() != base {
		t.Fatal("withdraw cancel base was not exposed by AnyTx")
	}
	if got := tx.GetKind(); got != "frost_withdraw_cancel" {
		t.Fatalf("withdraw cancel kind=%q", got)
	}
	if got := tx.GetTxId(); got != base.TxId {
		t.Fatalf("withdraw cancel tx id=%q", got)
	}
}
