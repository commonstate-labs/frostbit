                  
package pb

func (m *AnyTx) GetBase() *BaseMessage {
	switch tx := m.GetContent().(type) {
	case *AnyTx_IssueTokenTx:
		return tx.IssueTokenTx.GetBase()
	case *AnyTx_FreezeTx:
		return tx.FreezeTx.GetBase()
	case *AnyTx_Transaction:
		return tx.Transaction.GetBase()
	case *AnyTx_OrderTx:
		return tx.OrderTx.GetBase()
	case *AnyTx_MinerTx:
		return tx.MinerTx.GetBase()
	case *AnyTx_WitnessStakeTx:
		return tx.WitnessStakeTx.GetBase()
	case *AnyTx_WitnessRequestTx:
		return tx.WitnessRequestTx.GetBase()
	case *AnyTx_WitnessVoteTx:
		return tx.WitnessVoteTx.GetBase()
	case *AnyTx_WitnessChallengeTx:
		return tx.WitnessChallengeTx.GetBase()
	case *AnyTx_ArbitrationVoteTx:
		return tx.ArbitrationVoteTx.GetBase()
	case *AnyTx_WitnessClaimRewardTx:
		return tx.WitnessClaimRewardTx.GetBase()
	case *AnyTx_WitnessClaimStakeTx:
		return tx.WitnessClaimStakeTx.GetBase()
	case *AnyTx_FrostWithdrawRequestTx:
		return tx.FrostWithdrawRequestTx.GetBase()
	case *AnyTx_FrostWithdrawSignedTx:
		return tx.FrostWithdrawSignedTx.GetBase()
	case *AnyTx_FrostWithdrawClaimTx:
		return tx.FrostWithdrawClaimTx.GetBase()
	case *AnyTx_FrostWithdrawCancelTx:
		return tx.FrostWithdrawCancelTx.GetBase()
	case *AnyTx_FrostVaultDkgCommitTx:
		return tx.FrostVaultDkgCommitTx.GetBase()
	case *AnyTx_FrostVaultDkgShareTx:
		return tx.FrostVaultDkgShareTx.GetBase()
	case *AnyTx_FrostVaultDkgComplaintTx:
		return tx.FrostVaultDkgComplaintTx.GetBase()
	case *AnyTx_FrostVaultDkgRevealTx:
		return tx.FrostVaultDkgRevealTx.GetBase()
	case *AnyTx_FrostVaultDkgValidationSignedTx:
		return tx.FrostVaultDkgValidationSignedTx.GetBase()
	case *AnyTx_FrostVaultTransitionSignedTx:
		return tx.FrostVaultTransitionSignedTx.GetBase()
	case *AnyTx_FutureOrderTx:
		return tx.FutureOrderTx.GetBase()
	case *AnyTx_FutureAddMarginTx:
		return tx.FutureAddMarginTx.GetBase()
	case *AnyTx_FutureRemoveMarginTx:
		return tx.FutureRemoveMarginTx.GetBase()
	case *AnyTx_MintTokenTx:
		return tx.MintTokenTx.GetBase()
	case *AnyTx_BurnTokenTx:
		return tx.BurnTokenTx.GetBase()
	case *AnyTx_TransferOwnershipTx:
		return tx.TransferOwnershipTx.GetBase()
	case *AnyTx_CertSignTx:
		return tx.CertSignTx.GetBase()
	case *AnyTx_SetReferrerTx:
		return tx.SetReferrerTx.GetBase()
	case *AnyTx_FutureOrderCancelTx:
		return tx.FutureOrderCancelTx.GetBase()
	case *AnyTx_MinerInfoTx:
		return tx.MinerInfoTx.GetBase()
	default:
		return nil
	}
}

func (m *AnyTx) GetTxId() string {
	if b := m.GetBase(); b != nil {
		return b.TxId
	}
	return ""
}

                                                                               
                                                                                                         
func (m *AnyTx) GetKind() string {
	switch m.GetContent().(type) {
	case *AnyTx_IssueTokenTx:
		return "issue_token"
	case *AnyTx_FreezeTx:
		return "freeze"
	case *AnyTx_Transaction:
		return "transfer"
	case *AnyTx_OrderTx:
		return "order"
	case *AnyTx_MinerTx:
		return "miner"
	case *AnyTx_WitnessStakeTx:
		return "witness_stake"
	case *AnyTx_WitnessRequestTx:
		return "witness_request"
	case *AnyTx_WitnessVoteTx:
		return "witness_vote"
	case *AnyTx_WitnessChallengeTx:
		return "witness_challenge"
	case *AnyTx_ArbitrationVoteTx:
		return "arbitration_vote"
	case *AnyTx_WitnessClaimRewardTx:
		return "witness_claim_reward"
	case *AnyTx_WitnessClaimStakeTx:
		return "witness_claim_stake"
	case *AnyTx_FrostWithdrawRequestTx:
		return "frost_withdraw_request"
	case *AnyTx_FrostWithdrawSignedTx:
		return "frost_withdraw_signed"
	case *AnyTx_FrostWithdrawClaimTx:
		return "frost_withdraw_claim"
	case *AnyTx_FrostWithdrawCancelTx:
		return "frost_withdraw_cancel"
	case *AnyTx_FrostVaultDkgCommitTx:
		return "frost_vault_dkg_commit"
	case *AnyTx_FrostVaultDkgShareTx:
		return "frost_vault_dkg_share"
	case *AnyTx_FrostVaultDkgComplaintTx:
		return "frost_vault_dkg_complaint"
	case *AnyTx_FrostVaultDkgRevealTx:
		return "frost_vault_dkg_reveal"
	case *AnyTx_FrostVaultDkgValidationSignedTx:
		return "frost_vault_dkg_validation_signed"
	case *AnyTx_FrostVaultTransitionSignedTx:
		return "frost_vault_transition_signed"
	case *AnyTx_FutureOrderTx:
		return "future_order"
	case *AnyTx_FutureAddMarginTx:
		return "future_add_margin"
	case *AnyTx_FutureRemoveMarginTx:
		return "future_remove_margin"
	case *AnyTx_MintTokenTx:
		return "mint_token"
	case *AnyTx_BurnTokenTx:
		return "burn_token"
	case *AnyTx_TransferOwnershipTx:
		return "transfer_ownership"
	case *AnyTx_CertSignTx:
		return "cert_sign"
	case *AnyTx_SetReferrerTx:
		return "set_referrer"
	case *AnyTx_FutureOrderCancelTx:
		return "future_order_cancel"
	case *AnyTx_MinerInfoTx:
		return "miner_info"
	default:
		return ""
	}
}
