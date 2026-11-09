package pb

                                                                              
                                                                         
                                                                             
func TxRequiresAccountNonce(tx *AnyTx) bool {
	if tx == nil {
		return false
	}
	switch tx.GetContent().(type) {
	case *AnyTx_WitnessVoteTx,
		*AnyTx_ArbitrationVoteTx,
		*AnyTx_FrostWithdrawSignedTx,
		*AnyTx_FrostWithdrawClaimTx,
		*AnyTx_FrostVaultDkgCommitTx,
		*AnyTx_FrostVaultDkgShareTx,
		*AnyTx_FrostVaultDkgComplaintTx,
		*AnyTx_FrostVaultDkgRevealTx,
		*AnyTx_FrostVaultDkgValidationSignedTx,
		*AnyTx_FrostVaultTransitionSignedTx,
		*AnyTx_CertSignTx:
		return false
	default:
		return true
	}
}
