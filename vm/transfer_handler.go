package vm

import (
	"dex/keys"
	"dex/pb"
	"dex/rent"
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/proto"
)

                                          
type TransferTxHandler struct{}

func (h *TransferTxHandler) Kind() string {
	return "transfer"
}

func (h *TransferTxHandler) DryRun(tx *pb.AnyTx, sv StateView) ([]WriteOp, *Receipt, error) {
	                       
	transferTx, ok := tx.GetContent().(*pb.AnyTx_Transaction)
	if !ok {
		return nil, &Receipt{
			TxID:   tx.GetTxId(),
			Status: "FAILED",
			Error:  "not a transfer transaction",
		}, fmt.Errorf("not a transfer transaction")
	}

	transfer := transferTx.Transaction
	if transfer == nil || transfer.Base == nil {
		return nil, &Receipt{
			TxID:   tx.GetTxId(),
			Status: "FAILED",
			Error:  "invalid transfer transaction",
		}, fmt.Errorf("invalid transfer transaction")
	}

	                     
	amount, err := parsePositiveBalanceStrict("transfer amount", transfer.Amount)
	if err != nil {
		return nil, &Receipt{
			TxID:   transfer.Base.TxId,
			Status: "FAILED",
			Error:  "invalid transfer amount",
		}, fmt.Errorf("invalid transfer amount: %s", transfer.Amount)
	}

	                                          
	if IsFrozen(sv, transfer.Base.FromAddress, transfer.TokenAddress) {
		return nil, &Receipt{
			TxID:   transfer.Base.TxId,
			Status: "FAILED",
			Error:  "sender account is frozen for this token",
		}, fmt.Errorf("sender account is frozen for token: %s", transfer.TokenAddress)
	}

	                           
	fromAccountKey := keys.KeyAccount(transfer.Base.FromAddress)
	fromAccountData, fromExists, err := sv.Get(fromAccountKey)
	if err != nil || !fromExists {
		return nil, &Receipt{
			TxID:   transfer.Base.TxId,
			Status: "FAILED",
			Error:  "sender account not found",
		}, fmt.Errorf("sender account not found")
	}

	var fromAccount pb.Account
	if err := unmarshalProtoCompat(fromAccountData, &fromAccount); err != nil {
		return nil, &Receipt{
			TxID:   transfer.Base.TxId,
			Status: "FAILED",
			Error:  "failed to parse sender account",
		}, err
	}

	                                             
	fromTokenBal := GetBalance(sv, transfer.Base.FromAddress, transfer.TokenAddress)
	fromBalance, err := parseBalanceStrict("sender balance", fromTokenBal.Balance)
	if err != nil {
		return nil, &Receipt{
			TxID:   transfer.Base.TxId,
			Status: "FAILED",
			Error:  "invalid sender balance",
		}, err
	}

	if fromBalance.Cmp(amount) < 0 {
		return nil, &Receipt{
			TxID:   transfer.Base.TxId,
			Status: "FAILED",
			Error:  fmt.Sprintf("insufficient balance: has %s, need %s", fromBalance.String(), amount.String()),
		}, fmt.Errorf("insufficient balance")
	}

	                                                                            
	                                                                             
	                                                                          
	                                                                          
	receiverSweeping, err := isRentReceiverSweeping(sv, transfer.To)
	if err != nil {
		return nil, &Receipt{
			TxID:   transfer.Base.TxId,
			Status: "FAILED",
			Error:  "failed to inspect receiver rent state",
		}, err
	}
	if receiverSweeping {
		return nil, &Receipt{
			TxID:   transfer.Base.TxId,
			Status: "FAILED",
			Error:  "receiver account is being swept for unpaid rent",
		}, fmt.Errorf("receiver account is being swept for unpaid rent")
	}

	                           
	toAccountKey := keys.KeyAccount(transfer.To)
	toAccountData, toExists, err := sv.Get(toAccountKey)
	if err != nil {
		                                                                          
		                                                                        
		                                                                   
		return nil, nil, fmt.Errorf("failed to read receiver account: %w", err)
	}

	var toAccount pb.Account
	if toExists {
		if err := unmarshalProtoCompat(toAccountData, &toAccount); err != nil {
			return nil, &Receipt{
				TxID:   transfer.Base.TxId,
				Status: "FAILED",
				Error:  "failed to parse receiver account",
			}, err
		}
	} else {
		toAccount = pb.Account{Address: transfer.To}
		rent.MarkOwnerInitialized(sv, transfer.To)
	}

	                                                                                      
	activated, err := maybeApplyRentActivationFee(sv, transfer)
	if err != nil {
		return nil, &Receipt{TxID: transfer.Base.TxId, Status: "FAILED", Error: err.Error()}, err
	}

	newFromBalance, err := SafeSub(fromBalance, amount)
	if err != nil {
		return nil, &Receipt{TxID: transfer.Base.TxId, Status: "FAILED", Error: "balance underflow"}, err
	}
	fromTokenBal.Balance = newFromBalance.String()
	SetBalance(sv, transfer.Base.FromAddress, transfer.TokenAddress, fromTokenBal)

	                        
	toTokenBal := GetBalance(sv, transfer.To, transfer.TokenAddress)
	toBalance, err := parseBalanceStrict("receiver balance", toTokenBal.Balance)
	if err != nil {
		return nil, &Receipt{
			TxID:   transfer.Base.TxId,
			Status: "FAILED",
			Error:  "invalid receiver balance",
		}, err
	}
	                                                
	newToBalance, err := SafeAdd(toBalance, amount)
	if err != nil {
		return nil, &Receipt{
			TxID:   transfer.Base.TxId,
			Status: "FAILED",
			Error:  "balance overflow",
		}, fmt.Errorf("receiver balance overflow: %w", err)
	}
	toTokenBal.Balance = newToBalance.String()
	SetBalance(sv, transfer.To, transfer.TokenAddress, toTokenBal)

	                                       
	ws := make([]WriteOp, 0)

	                                          
	updatedFromData, err := MarshalProto(&fromAccount)
	if err != nil {
		return nil, &Receipt{
			TxID:   transfer.Base.TxId,
			Status: "FAILED",
			Error:  "failed to marshal sender account",
		}, err
	}

	ws = append(ws, WriteOp{
		Key:      fromAccountKey,
		Value:    updatedFromData,
		Del:      false,
		Category: "account",
	})

	                                          
	updatedToData, err := MarshalProto(&toAccount)
	if err != nil {
		return nil, &Receipt{
			TxID:   transfer.Base.TxId,
			Status: "FAILED",
			Error:  "failed to marshal receiver account",
		}, err
	}

	ws = append(ws, WriteOp{
		Key:      toAccountKey,
		Value:    updatedToData,
		Del:      false,
		Category: "account",
	})

	                        
	ws = append(ws, balanceWriteOp(sv, transfer.Base.FromAddress, transfer.TokenAddress))

	                                 
	ws = append(ws, balanceWriteOp(sv, transfer.To, transfer.TokenAddress))
	if activated {
		ws = append(ws, balanceWriteOp(sv, transfer.Base.FromAddress, rent.FeeToken))
		ws = append(ws, balanceWriteOp(sv, transfer.To, rent.FeeToken))
	}

	                     
	historyKey := keys.KeyTransferHistory(transfer.Base.TxId)
	historyData, _ := proto.Marshal(transfer)
	ws = append(ws, WriteOp{Key: historyKey, Value: historyData, Del: false, Category: "history"})

	return ws, &Receipt{
		TxID:       transfer.Base.TxId,
		Status:     "SUCCEED",
		WriteCount: len(ws),
	}, nil
}

func (h *TransferTxHandler) Apply(tx *pb.AnyTx) error {
	return ErrNotImplemented
}

func isRentReceiverSweeping(sv StateView, owner string) (bool, error) {
	if !rent.IsRentManagedAccount(owner) {
		return false, nil
	}
	raw, exists, err := sv.Get(keys.KeyRentState(owner))
	if err != nil {
		return false, fmt.Errorf("read receiver rent state: %w", err)
	}
	if !exists {
		return false, nil
	}
	var state rent.RentState
	if err := json.Unmarshal(raw, &state); err != nil {
		return false, fmt.Errorf("decode receiver rent state: %w", err)
	}
	return state.Mode == rent.RentModeSweeping, nil
}

func maybeApplyRentActivationFee(sv StateView, transfer *pb.Transaction) (bool, error) {
	if transfer == nil || transfer.Base == nil || transfer.TokenAddress == rent.FeeToken || !rent.IsRentManagedAccount(transfer.To) {
		return false, nil
	}
	summary, err := rent.NewService(rentStateViewStore{sv: sv}, rent.WithClaimMarkerFastPath()).NominalFBAsset(transfer.To)
	if err != nil {
		return false, err
	}
	nominal, err := parseBalanceStrict("receiver nominal FB", summary.Total)
	if err != nil {
		return false, err
	}
	if nominal.Sign() > 0 {
		return false, nil
	}
	service := rent.NewService(rentStateViewStore{sv: sv}, rent.WithClaimMarkerFastPath())
	policy := rent.DefaultPolicy()
	evaluation, err := service.IsExempt(transfer.To, policy)
	if err != nil {
		return false, err
	}
	if evaluation.ExemptAsset != "" {
		return false, nil
	}
	                                                                         
	                                                                    
	for _, asset := range policy.ExemptAssets {
		if asset.Token != transfer.TokenAddress {
			continue
		}
		held, err := service.NominalAsset(transfer.To, asset.Token)
		if err != nil {
			return false, err
		}
		total, err := parseBalanceStrict("receiver nominal asset", held.Total)
		if err != nil {
			return false, err
		}
		incoming, err := parseBalanceStrict("transfer amount", transfer.Amount)
		if err != nil {
			return false, err
		}
		if total.Add(total, incoming).Cmp(asset.Minimum) >= 0 {
			return false, nil
		}
		break
	}
	fee, err := parseBalanceStrict("rent activation fee", rent.DefaultActivationFeeRaw)
	if err != nil {
		return false, err
	}
	fromFB := GetBalance(sv, transfer.Base.FromAddress, rent.FeeToken)
	fromAvail, err := parseBalanceStrict("sender FB balance", fromFB.Balance)
	if err != nil {
		return false, err
	}
	if fromAvail.Cmp(fee) < 0 {
		return false, fmt.Errorf("insufficient FB for rent activation: has %s, need %s", fromAvail, fee)
	}
	nextFrom, _ := SafeSub(fromAvail, fee)
	fromFB.Balance = nextFrom.String()
	SetBalance(sv, transfer.Base.FromAddress, rent.FeeToken, fromFB)
	toFB := GetBalance(sv, transfer.To, rent.FeeToken)
	toAvail, err := parseBalanceStrict("receiver FB balance", toFB.Balance)
	if err != nil {
		return false, err
	}
	nextTo, err := SafeAdd(toAvail, fee)
	if err != nil {
		return false, err
	}
	toFB.Balance = nextTo.String()
	SetBalance(sv, transfer.To, rent.FeeToken, toFB)
	return true, nil
}
