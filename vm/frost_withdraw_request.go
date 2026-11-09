                               
                                    
package vm

import (
	"crypto/sha256"
	frostchain "dex/frost/chain"
	"dex/keys"
	"dex/pb"
	"dex/protocol"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"
)

                                           
type WithdrawRequestStatus string

const (
	WithdrawStatusQueued    WithdrawRequestStatus = "QUEUED"
	WithdrawStatusSigning   WithdrawRequestStatus = "SIGNING"
	WithdrawStatusSigned    WithdrawRequestStatus = "SIGNED"
	WithdrawStatusCancelled WithdrawRequestStatus = "CANCELLED"
)

                                                            
type FrostWithdrawRequest struct {
	WithdrawID           string                `json:"withdraw_id"`
	TxID                 string                `json:"tx_id"`
	Seq                  uint64                `json:"seq"`
	Chain                string                `json:"chain"`
	Asset                string                `json:"asset"`
	To                   string                `json:"to"`
	Amount               string                `json:"amount"`
	BroadcastFee         string                `json:"broadcast_fee"`
	NetAmount            string                `json:"net_amount"`
	FeeVersion           uint32                `json:"fee_version"`
	LiveSeq              uint64                `json:"live_seq"`
	KeyEpoch             uint64                `json:"key_epoch"`
	RequestHeight        uint64                `json:"request_height"`
	Status               WithdrawRequestStatus `json:"status"`
	JobID                string                `json:"job_id,omitempty"`                               
	VaultID              uint32                `json:"vault_id,omitempty"`
	SignedTxID           string                `json:"signed_tx_id,omitempty"`                                   
	Requester            string                `json:"requester,omitempty"`
	CancelTxID           string                `json:"cancel_tx_id,omitempty"`
	CancelHeight         uint64                `json:"cancel_height,omitempty"`
	BtcProtocolVersion   uint32                `json:"btc_protocol_version,omitempty"`
	BtcUnallocatedAmount string                `json:"btc_unallocated_amount,omitempty"`
	BtcClaimedAmount     string                `json:"btc_claimed_amount,omitempty"`
	BtcSignedAmount      string                `json:"btc_signed_amount,omitempty"`
	BtcRefundedAmount    string                `json:"btc_refunded_amount,omitempty"`
	BtcMaxTotalFee       string                `json:"btc_max_total_fee,omitempty"`
	BtcCommittedFee      string                `json:"btc_committed_fee,omitempty"`
	BtcMaxParts          uint32                `json:"btc_max_parts,omitempty"`
	BtcNextPartSeq       uint64                `json:"btc_next_part_seq,omitempty"`
}

                                                                  
type FrostWithdrawRequestTxHandler struct{}

func (h *FrostWithdrawRequestTxHandler) Kind() string {
	return "frost_withdraw_request"
}

func (h *FrostWithdrawRequestTxHandler) DryRun(tx *pb.AnyTx, sv StateView) (resultOps []WriteOp, resultReceipt *Receipt, resultErr error) {
	reqTx, ok := tx.GetContent().(*pb.AnyTx_FrostWithdrawRequestTx)
	if !ok {
		return nil, &Receipt{TxID: tx.GetTxId(), Status: "FAILED", Error: "not a frost withdraw request transaction"}, errors.New("not a frost withdraw request transaction")
	}

	req := reqTx.FrostWithdrawRequestTx
	if req == nil || req.Base == nil {
		return nil, &Receipt{TxID: tx.GetTxId(), Status: "FAILED", Error: "invalid frost withdraw request transaction"}, errors.New("invalid frost withdraw request transaction")
	}

	txID := req.Base.TxId
	to := strings.TrimSpace(req.To)
	amount := req.Amount
	requestHeight := req.Base.ExecutedHeight

	                                                                                                       
	tokenAddr := strings.TrimSpace(req.Asset)
	if txID == "" || tokenAddr == "" || to == "" || amount == "" {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "missing required fields"}, errors.New("missing required fields")
	}

	                                                                          
	                                                     
	txRefKey := keys.KeyFrostWithdrawTxRef(txID)
	existingWithdrawID, exists, err := sv.Get(txRefKey)
	if err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to read withdrawal reference"}, err
	}
	if exists && len(existingWithdrawID) > 0 {
		return nil, &Receipt{TxID: txID, Status: "SUCCEED", Error: "", WriteCount: 0}, nil
	}

	tokenKey := keys.KeyToken(tokenAddr)
	tokenData, tokenExists, err := sv.Get(tokenKey)
	if err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to read token record"}, err
	}
	if !tokenExists {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "token not found: " + tokenAddr}, fmt.Errorf("token not found: %s", tokenAddr)
	}
	var tokenRec pb.Token
	if err := unmarshalProtoCompat(tokenData, &tokenRec); err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "invalid token record"}, err
	}
	chain := frostchain.NormalizeChain(tokenRec.NativeChain)
	asset := tokenRec.Address
	if chain == "" {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "token has no native chain"}, fmt.Errorf("token has no native chain: %s", tokenAddr)
	}
	if requestedChain := strings.TrimSpace(req.Chain); requestedChain != "" && frostchain.NormalizeChain(requestedChain) != chain {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "requested chain does not match token native chain"}, fmt.Errorf("requested chain %s does not match token native chain %s", requestedChain, chain)
	}

	                                                                           
	amountBI, err := frostchain.ParseWithdrawAmount(amount)
	if err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "invalid amount format"}, err
	}
	amount = amountBI.String()
	feeBI := new(big.Int)
	feeVersion := uint32(0)
	btcV3 := false
	if chain == frostchain.ChainBTC {
		active, gateErr := frostchain.BTCIsolationActive(sv.Get)
		if gateErr != nil {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: gateErr.Error()}, gateErr
		}
		if active {
			maxFee, feeErr := parseBTCIsolationAmount("maximum total fee", req.BtcMaxTotalFee)
			if req.BtcProtocolVersion != protocol.BTCIsolationVersion || feeErr != nil || maxFee == 0 ||
				!amountBI.IsUint64() || maxFee >= amountBI.Uint64() || req.BtcMaxParts == 0 || req.BtcMaxParts > protocol.BTCIsolationMaxParts {
				err := errors.New("BTC v3 request requires explicit valid cumulative fee and part budgets")
				return nil, &Receipt{TxID: txID, Status: "FAILED", Error: err.Error()}, err
			}
			btcV3 = true
		} else if req.BtcProtocolVersion != 0 || req.BtcMaxTotalFee != "" || req.BtcMaxParts != 0 {
			err := errors.New("BTC v3 isolation is not active")
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: err.Error()}, err
		}
	} else {
		if req.BtcProtocolVersion != 0 || req.BtcMaxTotalFee != "" || req.BtcMaxParts != 0 {
			err := errors.New("BTC budgets are invalid for account-chain withdrawal")
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: err.Error()}, err
		}
		feeBI = accountWithdrawBroadcastFee(amountBI)
		feeVersion = accountWithdrawFeeVersion
		if feeBI.Sign() <= 0 || feeBI.Cmp(amountBI) >= 0 {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "account-chain gross amount is too small for protocol fee"}, errors.New("invalid account-chain gross amount")
		}
	}
	broadcastFee := feeBI.String()
	netAmount := new(big.Int).Sub(new(big.Int).Set(amountBI), feeBI).String() // safe-math-ok: 手续费经过金额与比例校验，净额仍由提现准入检查
	to, err = validateFrostWithdrawAdmission(sv, chain, asset, to, &tokenRec, amountBI)
	if err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: err.Error()}, err
	}
	if chain == frostchain.ChainBTC {
		if err := rejectFrostWithdrawDuringHandover(sv, chain); err != nil {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: err.Error()}, err
		}
	}

	var assignedVault uint32
	var assignedEpoch uint64
	var liveSeq uint64
	var accountAllocation *accountWithdrawAllocation
	if btcV3 {
		snap := sv.Snapshot()
		defer func() {
			if resultErr != nil || resultReceipt == nil || resultReceipt.Status != "SUCCEED" {
				_ = sv.Revert(snap)
			}
		}()
	}
	if chain != frostchain.ChainBTC {
		accountAllocation, err = selectAndReserveAccountWithdraw(sv, chain, asset, amountBI)
		if err != nil {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: err.Error()}, err
		}
		assignedVault, assignedEpoch, liveSeq = accountAllocation.vaultID, accountAllocation.keyEpoch, accountAllocation.liveSeq
	}

	                                                                   
	userAddr := req.Base.FromAddress
	if IsFrozen(sv, userAddr, tokenAddr) {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "account is frozen for this token"}, fmt.Errorf("account is frozen for token: %s", tokenAddr)
	}
	userBal := GetBalance(sv, userAddr, tokenAddr)
	currentBal, err := parseBalanceStrict("user balance", userBal.Balance)
	if err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "invalid user balance"}, err
	}
	if currentBal.Cmp(amountBI) < 0 {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: fmt.Sprintf("insufficient balance for withdraw: has %s, need %s", currentBal, amountBI)}, fmt.Errorf("insufficient balance")
	}
	newBal, err := SafeSub(currentBal, amountBI)
	if err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "balance underflow"}, err
	}
	userBal.Balance = newBal.String()
	SetBalance(sv, userAddr, tokenAddr, userBal)

	                                                                                 
	if tokenRec.Type == pb.TokenType_TOKEN_BRIDGED {
		if err := adjustBridgedTotalSupply(sv, tokenAddr, new(big.Int).Neg(amountBI)); err != nil {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "total supply underflow"}, err
		}
	}

	                                            
	seqKey := keys.KeyFrostWithdrawFIFOSeq(chain, asset)
	if btcV3 {
		seqKey = keys.KeyFrostBtcWithdrawParentSeq(asset)
	}
	newSeq := liveSeq
	if chain == frostchain.ChainBTC {
		currentSeq := uint64(0)
		if !btcV3 {
			currentSeq = readUint64FromState(sv, seqKey)
		}
		if btcV3 {
			raw, found, err := sv.Get(seqKey)
			if err != nil {
				return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to read BTC parent sequence"}, err
			}
			if !found {
				currentSeq, err = readWithdrawQueueUint64(sv, keys.KeyFrostWithdrawFIFOSeq(chain, asset))
				if err != nil {
					return nil, &Receipt{TxID: txID, Status: "FAILED", Error: err.Error()}, err
				}
			} else {
				currentSeq, err = strconv.ParseUint(string(raw), 10, 64)
				if err != nil || currentSeq == 0 || strconv.FormatUint(currentSeq, 10) != string(raw) {
					err := errors.New("invalid BTC parent sequence")
					return nil, &Receipt{TxID: txID, Status: "FAILED", Error: err.Error()}, err
				}
			}
		}
		if currentSeq == ^uint64(0) {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "withdraw sequence exhausted"}, errors.New("withdraw sequence exhausted")
		}
		newSeq = currentSeq + 1
	}

	                                                                  
	withdrawID := generateWithdrawID(chain, asset, newSeq, requestHeight)
	if chain != frostchain.ChainBTC {
		withdrawID = generateAccountWithdrawID(chain, assignedVault, newSeq, requestHeight)
	}

	                         
	withdrawReq := &FrostWithdrawRequest{
		WithdrawID:    withdrawID,
		TxID:          txID,
		Seq:           newSeq,
		Chain:         chain,
		Asset:         asset,
		To:            to,
		Amount:        amount,
		BroadcastFee:  broadcastFee,
		NetAmount:     netAmount,
		FeeVersion:    feeVersion,
		LiveSeq:       liveSeq,
		KeyEpoch:      assignedEpoch,
		RequestHeight: requestHeight,
		Status:        WithdrawStatusQueued,
		Requester:     userAddr,
		VaultID:       assignedVault,
	}
	if btcV3 {
		withdrawReq.BtcProtocolVersion, withdrawReq.BtcMaxTotalFee, withdrawReq.BtcMaxParts = protocol.BTCIsolationVersion, req.BtcMaxTotalFee, req.BtcMaxParts
		withdrawReq.BtcUnallocatedAmount, withdrawReq.BtcClaimedAmount, withdrawReq.BtcSignedAmount, withdrawReq.BtcRefundedAmount = amount, "0", "0", "0"
		withdrawReq.BtcCommittedFee, withdrawReq.BtcNextPartSeq = "0", 1
	}

	                            
	withdrawData, err := marshalWithdrawRequest(withdrawReq)
	if err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to marshal withdraw request"}, err
	}

	                     
	ops := make([]WriteOp, 0, 8)

	                              
	ops = append(ops, balanceWriteOp(sv, userAddr, tokenAddr))

	                                   
	withdrawKey := keys.KeyFrostWithdraw(withdrawID)
	ops = append(ops, WriteOp{
		Key:      withdrawKey,
		Value:    withdrawData,
		Category: "frost_withdraw",
	})

	if chain == frostchain.ChainBTC {
		indexKey := keys.KeyFrostWithdrawFIFOIndex(chain, asset, newSeq)
		if btcV3 {
			indexKey = keys.KeyFrostBtcWithdrawReady(asset, newSeq, withdrawID)
		}
		ops = append(ops, WriteOp{Key: indexKey, Value: []byte(withdrawID), Category: "frost_withdraw_fifo"}, WriteOp{Key: seqKey, Value: []byte(strconv.FormatUint(newSeq, 10)), Category: "frost_withdraw_seq"})
	} else {
		ops = append(ops, accountAllocation.writeOps(withdrawID)...)
	}

	                                                                
	ops = append(ops, WriteOp{
		Key:      txRefKey,
		Value:    []byte(withdrawID),
		Category: "frost_withdraw_ref",
	})

	                                                                         
	                                                                 
	ops = append(ops,
		WriteOp{
			Key:      keys.KeyFrostWithdrawQueryGlobalState(false, requestHeight, newSeq, withdrawID),
			Value:    []byte(withdrawID),
			Category: "frost_withdraw_query",
		},
		WriteOp{
			Key:      keys.KeyFrostWithdrawQueryChainState(chain, false, requestHeight, newSeq, withdrawID),
			Value:    []byte(withdrawID),
			Category: "frost_withdraw_query",
		},
		WriteOp{
			Key:      keys.KeyFrostWithdrawQueryChainVaultState(chain, withdrawReq.VaultID, false, requestHeight, newSeq, withdrawID),
			Value:    []byte(withdrawID),
			Category: "frost_withdraw_query",
		},
	)

	                              
	applyFrostWithdrawTransactionOps(sv, ops)

	if btcV3 {
		return nil, &Receipt{TxID: txID, Status: "SUCCEED", WriteCount: len(ops)}, nil
	}
	return ops, &Receipt{TxID: txID, Status: "SUCCEED", WriteCount: len(ops)}, nil
}

func (h *FrostWithdrawRequestTxHandler) Apply(tx *pb.AnyTx) error {
	return ErrNotImplemented
}

                                                       
                                                           
func generateWithdrawID(chain, asset string, seq, requestHeight uint64) string {
	data := chain + "|" + asset + "|" + strconv.FormatUint(seq, 10) + "|" + strconv.FormatUint(requestHeight, 10)
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}

func generateAccountWithdrawID(chain string, vaultID uint32, seq, requestHeight uint64) string {
	data := chain + "|vault:" + strconv.FormatUint(uint64(vaultID), 10) + "|" + strconv.FormatUint(seq, 10) + "|" + strconv.FormatUint(requestHeight, 10)
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

                                                      
func readUint64FromState(sv StateView, key string) uint64 {
	data, exists, err := sv.Get(key)
	if err != nil || !exists || len(data) == 0 {
		return 0
	}
	n, err := strconv.ParseUint(string(data), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

                                                                
                                            
func marshalWithdrawRequest(req *FrostWithdrawRequest) ([]byte, error) {
	state := &pb.FrostWithdrawState{
		WithdrawId:           req.WithdrawID,
		TxId:                 req.TxID,
		Seq:                  req.Seq,
		Chain:                req.Chain,
		Asset:                req.Asset,
		To:                   req.To,
		Amount:               req.Amount,
		GrossAmount:          req.Amount,
		FeeAmount:            req.BroadcastFee,
		NetAmount:            req.NetAmount,
		FeeVersion:           req.FeeVersion,
		LiveSeq:              req.LiveSeq,
		KeyEpoch:             req.KeyEpoch,
		RequestHeight:        req.RequestHeight,
		Status:               string(req.Status),
		JobId:                req.JobID,
		VaultId:              req.VaultID,
		SignedTxId:           req.SignedTxID,
		Requester:            req.Requester,
		CancelTxId:           req.CancelTxID,
		CancelHeight:         req.CancelHeight,
		BtcProtocolVersion:   req.BtcProtocolVersion,
		BtcUnallocatedAmount: req.BtcUnallocatedAmount,
		BtcClaimedAmount:     req.BtcClaimedAmount,
		BtcSignedAmount:      req.BtcSignedAmount,
		BtcRefundedAmount:    req.BtcRefundedAmount,
		BtcMaxTotalFee:       req.BtcMaxTotalFee,
		BtcCommittedFee:      req.BtcCommittedFee,
		BtcMaxParts:          req.BtcMaxParts,
		BtcNextPartSeq:       req.BtcNextPartSeq,
	}
	return proto.Marshal(state)
}

                                                                     
func unmarshalWithdrawRequest(data []byte) (*FrostWithdrawRequest, error) {
	state := &pb.FrostWithdrawState{}
	if err := unmarshalProtoCompat(data, state); err != nil {
		return nil, err
	}
	broadcastFee := strings.TrimSpace(state.FeeAmount)
	if broadcastFee == "" {
		broadcastFee = "0"
	}
	return &FrostWithdrawRequest{
		WithdrawID:           state.WithdrawId,
		TxID:                 state.TxId,
		Seq:                  state.Seq,
		Chain:                state.Chain,
		Asset:                state.Asset,
		To:                   state.To,
		Amount:               firstNonEmpty(state.GrossAmount, state.Amount),
		BroadcastFee:         broadcastFee,
		NetAmount:            state.NetAmount,
		FeeVersion:           state.FeeVersion,
		LiveSeq:              state.LiveSeq,
		KeyEpoch:             state.KeyEpoch,
		RequestHeight:        state.RequestHeight,
		Status:               WithdrawRequestStatus(state.Status),
		JobID:                state.JobId,
		VaultID:              state.VaultId,
		SignedTxID:           state.SignedTxId,
		Requester:            state.Requester,
		CancelTxID:           state.CancelTxId,
		CancelHeight:         state.CancelHeight,
		BtcProtocolVersion:   state.BtcProtocolVersion,
		BtcUnallocatedAmount: state.BtcUnallocatedAmount,
		BtcClaimedAmount:     state.BtcClaimedAmount,
		BtcSignedAmount:      state.BtcSignedAmount,
		BtcRefundedAmount:    state.BtcRefundedAmount,
		BtcMaxTotalFee:       state.BtcMaxTotalFee,
		BtcCommittedFee:      state.BtcCommittedFee,
		BtcMaxParts:          state.BtcMaxParts,
		BtcNextPartSeq:       state.BtcNextPartSeq,
	}, nil
}
