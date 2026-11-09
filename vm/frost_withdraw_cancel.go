package vm

import (
	"dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/frost/chain/evm"
	"dex/keys"
	"dex/pb"
	"dex/txverify"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"
)

                                                                         
                                                                             
                                                                 
type FrostWithdrawCancelTxHandler struct{}

func (h *FrostWithdrawCancelTxHandler) Kind() string { return "frost_withdraw_cancel" }

func (h *FrostWithdrawCancelTxHandler) DryRun(tx *pb.AnyTx, sv StateView) ([]WriteOp, *Receipt, error) {
	cancelWrapper, ok := tx.GetContent().(*pb.AnyTx_FrostWithdrawCancelTx)
	if !ok || cancelWrapper.FrostWithdrawCancelTx == nil || cancelWrapper.FrostWithdrawCancelTx.Base == nil {
		return nil, &Receipt{TxID: tx.GetTxId(), Status: "FAILED", Error: "invalid frost withdraw cancel transaction"}, errors.New("invalid frost withdraw cancel transaction")
	}
	cancel := cancelWrapper.FrostWithdrawCancelTx
	base := cancel.Base
	withdrawID := strings.TrimSpace(cancel.WithdrawId)
	if withdrawID == "" {
		return nil, &Receipt{TxID: base.TxId, Status: "FAILED", Error: "missing withdraw_id"}, errors.New("missing withdraw_id")
	}
	if strings.TrimSpace(base.FromAddress) == "" {
		return nil, &Receipt{TxID: base.TxId, Status: "FAILED", Error: "missing cancel requester"}, errors.New("missing cancel requester")
	}

	withdrawKey := keys.KeyFrostWithdraw(withdrawID)
	withdrawData, exists, err := sv.Get(withdrawKey)
	if err != nil {
		return nil, &Receipt{TxID: base.TxId, Status: "FAILED", Error: "failed to read withdraw"}, err
	}
	if !exists || len(withdrawData) == 0 {
		if parent, readErr := readBTCIsolationParent(sv, withdrawID); readErr == nil {
			return h.dryRunBTCIsolationCancel(cancel, parent, sv)
		}
		return nil, &Receipt{TxID: base.TxId, Status: "FAILED", Error: "withdraw not found"}, errors.New("withdraw not found")
	}
	withdraw, err := unmarshalWithdrawRequest(withdrawData)
	if err != nil {
		return nil, &Receipt{TxID: base.TxId, Status: "FAILED", Error: "failed to parse withdraw"}, err
	}
	if withdraw.WithdrawID != withdrawID {
		return nil, &Receipt{TxID: base.TxId, Status: "FAILED", Error: "withdraw id mismatch"}, errors.New("withdraw id mismatch")
	}
	if withdraw.BtcProtocolVersion == 3 {
		return h.dryRunBTCIsolationCancel(cancel, withdraw, sv)
	}
	if withdraw.Status != WithdrawStatusQueued || strings.TrimSpace(withdraw.JobID) != "" {
		return nil, &Receipt{TxID: base.TxId, Status: "FAILED", Error: "withdraw is no longer cancellable"}, fmt.Errorf("withdraw is no longer cancellable: status=%s", withdraw.Status)
	}
	if err := validateWithdrawFIFOEntry(sv, withdraw); err != nil {
		return nil, &Receipt{TxID: base.TxId, Status: "FAILED", Error: err.Error()}, err
	}

	requester, err := authorizeWithdrawCancellation(sv, cancel, withdraw)
	if err != nil {
		return nil, &Receipt{TxID: base.TxId, Status: "FAILED", Error: err.Error()}, err
	}

	tokenKey := keys.KeyToken(withdraw.Asset)
	tokenData, tokenExists, err := sv.Get(tokenKey)
	if err != nil {
		return nil, &Receipt{TxID: base.TxId, Status: "FAILED", Error: "failed to read token record"}, err
	}
	if !tokenExists || len(tokenData) == 0 {
		return nil, &Receipt{TxID: base.TxId, Status: "FAILED", Error: "withdraw token not found"}, errors.New("withdraw token not found")
	}
	var token pb.Token
	if err := unmarshalProtoCompat(tokenData, &token); err != nil {
		return nil, &Receipt{TxID: base.TxId, Status: "FAILED", Error: "invalid withdraw token record"}, err
	}
	if token.Type != pb.TokenType_TOKEN_BRIDGED || token.Address != withdraw.Asset || chain.NormalizeChain(token.NativeChain) != chain.NormalizeChain(withdraw.Chain) {
		return nil, &Receipt{TxID: base.TxId, Status: "FAILED", Error: "withdraw token context mismatch"}, errors.New("withdraw token context mismatch")
	}

	amount, err := chain.ParseWithdrawAmount(withdraw.Amount)
	if err != nil {
		return nil, &Receipt{TxID: base.TxId, Status: "FAILED", Error: "invalid withdraw amount"}, err
	}
	balance, err := getBalanceStrict(sv, requester, withdraw.Asset)
	if err != nil {
		return nil, &Receipt{TxID: base.TxId, Status: "FAILED", Error: "failed to read requester balance"}, err
	}
	currentBalance, err := parseBalanceStrict("requester balance", balance.Balance)
	if err != nil {
		return nil, &Receipt{TxID: base.TxId, Status: "FAILED", Error: "invalid requester balance"}, err
	}
	refundedBalance, err := SafeAdd(currentBalance, amount)
	if err != nil {
		return nil, &Receipt{TxID: base.TxId, Status: "FAILED", Error: "requester balance overflow"}, err
	}

	totalSupply, err := parseBalanceStrict("token total supply", token.TotalSupply)
	if err != nil {
		return nil, &Receipt{TxID: base.TxId, Status: "FAILED", Error: "invalid token total supply"}, err
	}
	refundedSupply, err := SafeAdd(totalSupply, amount)
	if err != nil {
		return nil, &Receipt{TxID: base.TxId, Status: "FAILED", Error: "token total supply overflow"}, err
	}
	token.TotalSupply = refundedSupply.String()
	updatedTokenData, err := proto.Marshal(&token)
	if err != nil {
		return nil, &Receipt{TxID: base.TxId, Status: "FAILED", Error: "failed to marshal token record"}, err
	}

	withdraw.Status = WithdrawStatusCancelled
	withdraw.Requester = requester
	withdraw.CancelTxID = base.TxId
	withdraw.CancelHeight = base.ExecutedHeight
	cancelledData, err := marshalWithdrawRequest(withdraw)
	if err != nil {
		return nil, &Receipt{TxID: base.TxId, Status: "FAILED", Error: "failed to marshal cancelled withdraw"}, err
	}

	var rawHead, startHead, nextHead uint64
	var cleanupOps []WriteOp
	var cancelledAtHead bool
	if chain.NormalizeChain(withdraw.Chain) == chain.ChainBTC {
		rawHead, err = readWithdrawQueueUint64(sv, keys.KeyFrostWithdrawFIFOHead(withdraw.Chain, withdraw.Asset))
		if err != nil {
			return nil, &Receipt{TxID: base.TxId, Status: "FAILED", Error: "failed to read withdraw FIFO head"}, err
		}
		startHead = rawHead
		if startHead == 0 {
			startHead = 1
		}
		nextHead, cleanupOps, cancelledAtHead, err = resolveCancelledWithdrawsFrom(sv, withdraw.Chain, withdraw.Asset, startHead, withdraw)
		if err != nil {
			return nil, &Receipt{TxID: base.TxId, Status: "FAILED", Error: "failed to advance withdraw FIFO: " + err.Error()}, err
		}
	} else {
		cleanupOps, err = accountWithdrawCancelQueueOps(sv, withdraw, amount)
		if err != nil {
			return nil, &Receipt{TxID: base.TxId, Status: "FAILED", Error: err.Error()}, err
		}
	}

	                                                                          
	                                                                           
	balance.Balance = refundedBalance.String()
	SetBalance(sv, requester, withdraw.Asset, balance)
	ops := make([]WriteOp, 0, 4+len(cleanupOps))
	ops = append(ops, balanceWriteOp(sv, requester, withdraw.Asset))
	sv.Set(tokenKey, updatedTokenData)
	ops = append(ops, WriteOp{Key: tokenKey, Value: updatedTokenData, Category: "token"})
	if chain.NormalizeChain(withdraw.Chain) != chain.ChainBTC || !cancelledAtHead {
		ops = append(ops,
			WriteOp{Key: withdrawKey, Value: cancelledData, Category: "frost_withdraw"},
			archiveWriteOp(keys.KeyArchiveFrostWithdraw(withdrawID), cancelledData, "archive_frost_withdraw"),
			archiveWriteOp(keys.KeyArchiveFrostWithdrawDone(withdraw.Chain, withdraw.Asset, withdraw.Seq, withdrawID), []byte(withdrawID), "archive_frost_withdraw_done"),
		)
	}
	ops = append(ops, cleanupOps...)
	ops = append(ops, finalizeFrostWithdrawQueryIndexes(withdraw)...)
	if chain.NormalizeChain(withdraw.Chain) == chain.ChainBTC && (nextHead != startHead || rawHead == 0) {
		ops = append(ops, WriteOp{
			Key:      keys.KeyFrostWithdrawFIFOHead(withdraw.Chain, withdraw.Asset),
			Value:    []byte(strconv.FormatUint(nextHead, 10)),
			Category: "frost_withdraw_head",
		})
	}
	applyFrostWithdrawTransactionOps(sv, ops)

	receipt := &Receipt{TxID: base.TxId, Status: "SUCCEED", BlockHeight: base.ExecutedHeight, WriteCount: len(ops)}
	receipt.Logs = []string{fmt.Sprintf("frost_withdraw_cancel: withdraw=%s requester=%s refund=%s", withdrawID, requester, amount.String())}
	return ops, receipt, nil
}

func (h *FrostWithdrawCancelTxHandler) Apply(tx *pb.AnyTx) error { return ErrNotImplemented }

func authorizeWithdrawCancellation(sv StateView, cancel *pb.FrostWithdrawCancelTx, withdraw *FrostWithdrawRequest) (string, error) {
	if cancel == nil || cancel.Base == nil || withdraw == nil {
		return "", errors.New("invalid withdraw cancellation authorization")
	}
	sender := strings.TrimSpace(cancel.Base.FromAddress)
	if requester := strings.TrimSpace(withdraw.Requester); requester != "" {
		if sender != requester {
			return "", errors.New("only the withdrawal requester can cancel")
		}
		return requester, nil
	}

	if len(cancel.OriginalRequestTx) == 0 {
		return "", errors.New("legacy withdrawal requires original_request_tx proof")
	}
	var original pb.AnyTx
	if err := proto.Unmarshal(cancel.OriginalRequestTx, &original); err != nil {
		return "", errors.New("invalid original_request_tx proof")
	}
	if err := pb.Sanitize(&original); err != nil {
		return "", fmt.Errorf("invalid original_request_tx proof: %w", err)
	}
	requestWrapper, ok := original.GetContent().(*pb.AnyTx_FrostWithdrawRequestTx)
	if !ok || requestWrapper.FrostWithdrawRequestTx == nil || requestWrapper.FrostWithdrawRequestTx.Base == nil {
		return "", errors.New("original_request_tx is not a frost withdrawal request")
	}
	originalBase := requestWrapper.FrostWithdrawRequestTx.Base
	if originalBase.ChainId != cancel.Base.ChainId {
		return "", errors.New("original withdrawal chain_id mismatch")
	}
	if err := txverify.Verify(&original, cancel.Base.ChainId); err != nil {
		return "", fmt.Errorf("invalid original withdrawal signature: %w", err)
	}
	if originalBase.TxId != withdraw.TxID {
		return "", errors.New("original withdrawal tx_id mismatch")
	}
	if strings.TrimSpace(originalBase.FromAddress) != sender {
		return "", errors.New("only the withdrawal requester can cancel")
	}

	ref, exists, err := sv.Get(keys.KeyFrostWithdrawTxRef(withdraw.TxID))
	if err != nil {
		return "", fmt.Errorf("read original withdrawal reference: %w", err)
	}
	if !exists || string(ref) != withdraw.WithdrawID {
		return "", errors.New("original withdrawal reference mismatch")
	}
	if err := validateLegacyWithdrawProofPayload(sv, requestWrapper.FrostWithdrawRequestTx, withdraw); err != nil {
		return "", err
	}
	return sender, nil
}

                                                                           
                                                                               
                                                                             
                                      
func validateLegacyWithdrawProofPayload(sv StateView, request *pb.FrostWithdrawRequestTx, withdraw *FrostWithdrawRequest) error {
	if request == nil || withdraw == nil {
		return errors.New("invalid original withdrawal payload")
	}
	asset := strings.TrimSpace(request.Asset)
	if asset == "" || asset != withdraw.Asset {
		return errors.New("original withdrawal asset mismatch")
	}

	chainName := chain.NormalizeChain(request.Chain)
	if chainName == "" {
		tokenData, exists, err := sv.Get(keys.KeyToken(asset))
		if err != nil {
			return fmt.Errorf("read original withdrawal token: %w", err)
		}
		if !exists || len(tokenData) == 0 {
			return errors.New("original withdrawal token not found")
		}
		var token pb.Token
		if err := unmarshalProtoCompat(tokenData, &token); err != nil {
			return errors.New("invalid original withdrawal token")
		}
		chainName = chain.NormalizeChain(token.NativeChain)
	}
	if chainName == "" || chainName != chain.NormalizeChain(withdraw.Chain) {
		return errors.New("original withdrawal chain mismatch")
	}

	proofAmount, err := chain.ParseWithdrawAmount(request.Amount)
	if err != nil {
		return errors.New("invalid original withdrawal amount")
	}
	storedAmount, err := chain.ParseWithdrawAmount(withdraw.Amount)
	if err != nil || proofAmount.Cmp(storedAmount) != 0 {
		return errors.New("original withdrawal amount mismatch")
	}
	var receiver string
	switch chainName {
	case chain.ChainBTC:
		cfg, err := loadFrostVaultConfig(sv, chainName)
		if err != nil {
			return fmt.Errorf("load original withdrawal BTC config: %w", err)
		}
		receiver, _, err = btc.NormalizeAddressForNetwork(request.To, cfg.BtcNetwork)
		if err != nil {
			return errors.New("invalid original withdrawal receiver")
		}
	case chain.ChainETH, chain.ChainBNB, chain.ChainBASE, chain.ChainARB, chain.ChainTRX:
		receiver, err = evm.NormalizeAccountAddress(chainName, request.To, false)
		if err != nil {
			return errors.New("invalid original withdrawal receiver")
		}
	default:
		return errors.New("unsupported original withdrawal chain")
	}
	if receiver != withdraw.To {
		return errors.New("original withdrawal receiver mismatch")
	}
	return nil
}
