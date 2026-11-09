package vm

import (
	"fmt"
	"math/big"

	"dex/frost/chain"
	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

func (h *FrostWithdrawCancelTxHandler) dryRunBTCIsolationCancel(cancel *pb.FrostWithdrawCancelTx, parent *FrostWithdrawRequest, sv StateView) (ops []WriteOp, receipt *Receipt, resultErr error) {
	fail := func(err error) ([]WriteOp, *Receipt, error) {
		return nil, &Receipt{TxID: cancel.Base.TxId, Status: "FAILED", Error: err.Error()}, err
	}
	active, err := chain.BTCIsolationActive(sv.Get)
	if err != nil {
		return fail(err)
	}
	if !active {
		return fail(fmt.Errorf("BTC v3 isolation is not active"))
	}
	if parent.Requester == "" || cancel.Base.FromAddress != parent.Requester {
		return fail(fmt.Errorf("only the withdrawal requester can cancel"))
	}
	l, err := readBTCIsolationLedger(parent)
	if err != nil {
		return fail(err)
	}
	if l.unallocated == 0 {
		return nil, &Receipt{TxID: cancel.Base.TxId, Status: "SUCCEED"}, nil
	}
	amount := new(big.Int).SetUint64(l.unallocated)
	tokenKey := keys.KeyToken(parent.Asset)
	raw, found, err := sv.Get(tokenKey)
	if err != nil {
		return fail(err)
	}
	var token pb.Token
	if !found || proto.Unmarshal(raw, &token) != nil || token.Type != pb.TokenType_TOKEN_BRIDGED ||
		token.Address != parent.Asset || chain.NormalizeChain(token.NativeChain) != chain.ChainBTC {
		return fail(fmt.Errorf("invalid BTC refund token context"))
	}
	balance, err := getBalanceStrict(sv, parent.Requester, parent.Asset)
	if err != nil {
		return fail(err)
	}
	current, err := parseBalanceStrict("requester balance", balance.Balance)
	if err != nil {
		return fail(err)
	}
	refunded, err := SafeAdd(current, amount)
	if err != nil {
		return fail(err)
	}
	supply, err := parseBalanceStrict("token total supply", token.TotalSupply)
	if err != nil {
		return fail(err)
	}
	newSupply, err := SafeAdd(supply, amount)
	if err != nil {
		return fail(err)
	}
	token.TotalSupply = newSupply.String()
	tokenData, err := proto.Marshal(&token)
	if err != nil {
		return fail(err)
	}
	l.refunded += l.unallocated
	l.unallocated = 0
	parent.CancelTxID, parent.CancelHeight = cancel.Base.TxId, cancel.Base.ExecutedHeight
	writeBTCIsolationLedger(parent, l)
	snap := sv.Snapshot()
	defer func() {
		if resultErr != nil || receipt == nil || receipt.Status != "SUCCEED" {
			_ = sv.Revert(snap)
		}
	}()
	balance.Balance = refunded.String()
	SetBalance(sv, parent.Requester, parent.Asset, balance)
	sv.Set(tokenKey, tokenData)
	if err := persistBTCIsolationParent(sv, parent); err != nil {
		return fail(err)
	}
	return nil, &Receipt{TxID: cancel.Base.TxId, Status: "SUCCEED", BlockHeight: cancel.Base.ExecutedHeight,
		Logs: []string{fmt.Sprintf("frost_btc_cancel_unallocated: parent=%s refund=%s", parent.WithdrawID, amount.String())}}, nil
}
