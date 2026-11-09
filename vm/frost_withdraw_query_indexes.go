package vm

import "dex/keys"

func frostWithdrawQueryKeys(withdraw *FrostWithdrawRequest, terminal bool) []string {
	return []string{
		keys.KeyFrostWithdrawQueryGlobalState(terminal, withdraw.RequestHeight, withdraw.Seq, withdraw.WithdrawID),
		keys.KeyFrostWithdrawQueryChainState(withdraw.Chain, terminal, withdraw.RequestHeight, withdraw.Seq, withdraw.WithdrawID),
		keys.KeyFrostWithdrawQueryChainVaultState(withdraw.Chain, withdraw.VaultID, terminal, withdraw.RequestHeight, withdraw.Seq, withdraw.WithdrawID),
	}
}

                                                                               
                                                                            
                                                                            
func finalizeFrostWithdrawQueryIndexes(withdraw *FrostWithdrawRequest) []WriteOp {
	ops := make([]WriteOp, 0, 9)
	for _, key := range frostWithdrawQueryKeys(withdraw, false) {
		ops = append(ops, deleteWriteOp(key, "frost_withdraw_query_finalize"))
	}
	for _, key := range []string{
		keys.KeyFrostWithdrawQueryGlobal(withdraw.RequestHeight, withdraw.Seq, withdraw.WithdrawID),
		keys.KeyFrostWithdrawQueryChain(withdraw.Chain, withdraw.RequestHeight, withdraw.Seq, withdraw.WithdrawID),
		keys.KeyFrostWithdrawQueryChainVault(withdraw.Chain, withdraw.VaultID, withdraw.RequestHeight, withdraw.Seq, withdraw.WithdrawID),
	} {
		ops = append(ops, deleteWriteOp(key, "frost_withdraw_query_legacy_cleanup"))
	}
	for _, key := range frostWithdrawQueryKeys(withdraw, true) {
		ops = append(ops, WriteOp{Key: key, Value: []byte(withdraw.WithdrawID), Category: "frost_withdraw_query_terminal"})
	}
	return ops
}
