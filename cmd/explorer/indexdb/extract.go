package indexdb

import (
	"dex/pb"
)

                                               
func extractTxRecord(tx *pb.AnyTx, height uint64, txIndex int) *TxRecord {
	if tx == nil {
		return nil
	}

	record := &TxRecord{
		TxID:    tx.GetTxId(),
		TxType:  extractTxType(tx),
		Height:  height,
		TxIndex: txIndex,
	}

	                     
	if base := tx.GetBase(); base != nil {
		record.FromAddress = base.FromAddress
		record.Status = base.Status.String()
		record.Fee = base.Fee
		record.Nonce = base.Nonce
	}

	                      
	record.ToAddress, record.Value = extractToAndValue(tx)

	return record
}

                                                                                           
func ExtractTxType(tx *pb.AnyTx) string { return extractTxType(tx) }

                                   
func extractTxType(tx *pb.AnyTx) string {
	if tx == nil {
		return ""
	}
	switch tx.GetContent().(type) {
	case *pb.AnyTx_Transaction:
		return "Transaction"
	case *pb.AnyTx_IssueTokenTx:
		return "IssueToken"
	case *pb.AnyTx_FreezeTx:
		return "Freeze"
	case *pb.AnyTx_OrderTx:
		return "Order"
	case *pb.AnyTx_MinerTx:
		return "Miner"
	case *pb.AnyTx_WitnessStakeTx:
		return "WitnessStake"
	case *pb.AnyTx_WitnessRequestTx:
		return "WitnessRequest"
	case *pb.AnyTx_WitnessVoteTx:
		return "WitnessVote"
	case *pb.AnyTx_WitnessChallengeTx:
		return "WitnessChallenge"
	case *pb.AnyTx_ArbitrationVoteTx:
		return "ArbitrationVote"
	case *pb.AnyTx_WitnessClaimRewardTx:
		return "WitnessClaimReward"
	case *pb.AnyTx_FrostWithdrawRequestTx:
		return "FrostWithdrawRequest"
	case *pb.AnyTx_FrostWithdrawCancelTx:
		return "FrostWithdrawCancel"
	case *pb.AnyTx_FrostWithdrawClaimTx:
		return "FrostWithdrawClaim"
	case *pb.AnyTx_FrostWithdrawSignedTx:
		return "FrostWithdrawSigned"
	case *pb.AnyTx_FrostVaultDkgCommitTx:
		return "FrostVaultDkgCommit"
	case *pb.AnyTx_FrostVaultDkgShareTx:
		return "FrostVaultDkgShare"
	case *pb.AnyTx_FrostVaultDkgComplaintTx:
		return "FrostVaultDkgComplaint"
	case *pb.AnyTx_FrostVaultDkgRevealTx:
		return "FrostVaultDkgReveal"
	case *pb.AnyTx_FrostVaultDkgValidationSignedTx:
		return "FrostVaultDkgValidationSigned"
	case *pb.AnyTx_FrostVaultTransitionSignedTx:
		return "FrostVaultTransitionSigned"
	case *pb.AnyTx_FutureOrderTx:
		return "FutureOrder"
	case *pb.AnyTx_FutureAddMarginTx:
		return "FutureAddMargin"
	case *pb.AnyTx_FutureRemoveMarginTx:
		return "FutureRemoveMargin"
	case *pb.AnyTx_MintTokenTx:
		return "MintToken"
	case *pb.AnyTx_BurnTokenTx:
		return "BurnToken"
	case *pb.AnyTx_TransferOwnershipTx:
		return "TransferOwnership"
	case *pb.AnyTx_CertSignTx:
		return "CertSign"
	case *pb.AnyTx_SetReferrerTx:
		return "SetReferrer"
	case *pb.AnyTx_FutureOrderCancelTx:
		return "FutureOrderCancel"
	case *pb.AnyTx_MinerInfoTx:
		return "MinerInfo"
	}
	return "Unknown"
}

                                                                           
func ExtractToAndValue(tx *pb.AnyTx) (toAddress, value string) { return extractToAndValue(tx) }

                                                
func extractToAndValue(tx *pb.AnyTx) (toAddress, value string) {
	if tx == nil {
		return "", ""
	}
	switch c := tx.GetContent().(type) {
	case *pb.AnyTx_Transaction:
		t := c.Transaction
		return t.To, t.Amount
	case *pb.AnyTx_WitnessStakeTx:
		return "", c.WitnessStakeTx.Amount
	case *pb.AnyTx_MinerTx:
		return "", c.MinerTx.Amount
	case *pb.AnyTx_FreezeTx:
		return c.FreezeTx.TargetAddr, ""
	case *pb.AnyTx_OrderTx:
		o := c.OrderTx
		if o.Price != "" && o.Amount != "" {
			return "", o.Amount + " @ " + o.Price
		}
		return "", o.Amount
	case *pb.AnyTx_WitnessRequestTx:
		w := c.WitnessRequestTx
		return w.ReceiverAddress, w.Amount
	case *pb.AnyTx_FrostWithdrawRequestTx:
		f := c.FrostWithdrawRequestTx
		return f.To, f.Amount
	case *pb.AnyTx_FrostWithdrawCancelTx:
		return c.FrostWithdrawCancelTx.WithdrawId, ""
	case *pb.AnyTx_FutureOrderTx:
		f := c.FutureOrderTx
		side := "LONG"
		if f.Side == 2 {
			side = "SHORT"
		}
		return side, f.Quantity + " @ " + f.Price
	case *pb.AnyTx_FutureAddMarginTx:
		return c.FutureAddMarginTx.PositionId, c.FutureAddMarginTx.Amount
	case *pb.AnyTx_FutureRemoveMarginTx:
		return c.FutureRemoveMarginTx.PositionId, c.FutureRemoveMarginTx.Amount
	case *pb.AnyTx_SetReferrerTx:
		return c.SetReferrerTx.ReferrerAddress, ""
	case *pb.AnyTx_FutureOrderCancelTx:
		return c.FutureOrderCancelTx.OrderId, ""
	case *pb.AnyTx_MinerInfoTx:
		return c.MinerInfoTx.Endpoint, ""
	case *pb.AnyTx_MintTokenTx:
		return c.MintTokenTx.To, c.MintTokenTx.Amount
	case *pb.AnyTx_BurnTokenTx:
		return "", c.BurnTokenTx.Amount
	case *pb.AnyTx_TransferOwnershipTx:
		return c.TransferOwnershipTx.NewOwner, ""
	}
	return "", ""
}
