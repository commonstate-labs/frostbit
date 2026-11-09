package vm

import (
	"dex/keys"

	"dex/pb"
	"dex/protocol"
	"dex/utils"
	"fmt"
	"google.golang.org/protobuf/proto"
	"math/big"

	"sync"
)

var orderStatePool = sync.Pool{
	New: func() interface{} {
		return &pb.OrderState{}
	},
}

                                                                                
type OrderTxHandler struct{}

func (h *OrderTxHandler) Kind() string {
	return "order"
}

func (h *OrderTxHandler) DryRun(tx *pb.AnyTx, sv StateView) ([]WriteOp, *Receipt, error) {
	                    
	orderTx, ok := tx.GetContent().(*pb.AnyTx_OrderTx)
	if !ok {
		return nil, failedReceipt(tx.GetBase(), "not an order transaction"), fmt.Errorf("not an order transaction")
	}

	ord := orderTx.OrderTx
	if ord == nil || ord.Base == nil {
		return nil, failedReceipt(tx.GetBase(), "invalid order transaction"), fmt.Errorf("invalid order transaction")
	}

	                                    
	switch ord.Op {
	case pb.OrderOp_ADD:
		return h.handleAddOrder(ord, sv)
	case pb.OrderOp_REMOVE:
		return h.handleRemoveOrder(ord, sv)
	default:
		return nil, failedReceipt(ord.Base, "unknown order operation"), fmt.Errorf("unknown order operation: %v", ord.Op)
	}
}

                                                             
func (h *OrderTxHandler) handleAddOrder(ord *pb.OrderTx, sv StateView) ([]WriteOp, *Receipt, error) {
	return h.handleAddOrderWithLimits(ord, sv, nil, 0)
}

                                                                            
                                                                                
func (h *OrderTxHandler) handleAddOrderWithLimits(ord *pb.OrderTx, sv StateView, referencePrice *big.Int, makerLimit int) ([]WriteOp, *Receipt, error) {
	amountBI, err := parsePositiveBalanceStrict("order amount", ord.Amount)
	if err != nil {
		return nil, failedReceipt(ord.Base, "invalid order amount"), nil
	}
	if ord.Side != pb.OrderSide_BUY && ord.Side != pb.OrderSide_SELL {
		return nil, failedReceipt(ord.Base, "invalid order side"), nil
	}
	if _, identityErr := protocol.NewMarketIdentity(ord.BaseToken, ord.QuoteToken); identityErr != nil {
		return nil, failedReceipt(ord.Base, "invalid market identity: "+identityErr.Error()), identityErr
	}
	if assetErr := validateRegisteredMarketAssets(sv, ord.BaseToken, ord.QuoteToken); assetErr != nil {
		return nil, failedReceipt(ord.Base, assetErr.Error()), assetErr
	}

	priceBI := referencePrice
	if !ord.IsMarket {
		priceBI, err = parsePositiveBalanceStrict("order price", ord.Price)
		if err != nil {
			return nil, failedReceipt(ord.Base, "invalid order price"), nil
		}
		notional, err := QuoteFromBasePrice(amountBI, priceBI)
		if err != nil {
			return nil, failedReceipt(ord.Base, "amount*price overflow"), nil
		}
		if notional.Cmp(minSpotOrderNotional()) < 0 {
			return nil, failedReceipt(ord.Base, "order notional too small"), nil
		}
	}

	                                                     
	freezeToken := ord.QuoteToken
	if ord.Side == pb.OrderSide_SELL {
		freezeToken = ord.BaseToken
	}
	frozen := false
	if makerLimit > 0 {
		data, exists, err := sv.Get(keys.KeyFreeze(ord.Base.FromAddress, freezeToken))
		if err != nil {
			return nil, failedReceipt(ord.Base, "read token freeze"), err
		}
		frozen = exists && string(data) == "true"
	} else {
		frozen = IsFrozen(sv, ord.Base.FromAddress, freezeToken)
	}
	if frozen {
		return nil, failedReceipt(ord.Base, "account is frozen for token: "+freezeToken), nil
	}

	accountKey := keys.KeyAccount(ord.Base.FromAddress)
	if _, exists, err := sv.Get(accountKey); err != nil || !exists {
		if err != nil && makerLimit > 0 {
			return nil, failedReceipt(ord.Base, "read account"), err
		}
		return nil, failedReceipt(ord.Base, "account not found"), nil
	}

	                                                                               
	                                               
	                                                                    
	                                                                           
	ledger := NewLedger(sv)
	switch {
	case ord.Side == pb.OrderSide_SELL:                                        
		if err := ledger.Move(MoveRequest{
			Account: ord.Base.FromAddress, Token: ord.BaseToken, Amount: amountBI,
			From: BucketAvailable, To: BucketOrderFrozen,
			Reason: "spot_order_freeze_sell", Height: ord.Base.ExecutedHeight, TxID: ord.Base.TxId,
		}); err != nil {
			return nil, failedReceipt(ord.Base, fmt.Sprintf("freeze %s: %v", ord.BaseToken, err)), fmt.Errorf("insufficient balance")
		}
	case !ord.IsMarket:                            
		needed, err := QuoteFromBasePrice(amountBI, priceBI)
		if err != nil {
			return nil, failedReceipt(ord.Base, "amount*price overflow"), nil
		}
		if err := ledger.Move(MoveRequest{
			Account: ord.Base.FromAddress, Token: ord.QuoteToken, Amount: needed,
			From: BucketAvailable, To: BucketOrderFrozen,
			Reason: "spot_order_freeze_buy", Height: ord.Base.ExecutedHeight, TxID: ord.Base.TxId,
		}); err != nil {
			return nil, failedReceipt(ord.Base, fmt.Sprintf("freeze %s: %v", ord.QuoteToken, err)), fmt.Errorf("insufficient balance")
		}
	}
	                                                                                                

	pair := utils.GeneratePairKey(ord.BaseToken, ord.QuoteToken)

	                                                                                    
	var trades []TradeResult
	if makerLimit > 0 {
		trades, err = matchOrderFromSV(sv, keys.KeyOrderPriceIndexPrefix, keys.KeyOrderState, pair, ord.Side, amountBI, priceBI, ord.IsMarket, makerLimit, MarketSpot, ord.MaxSlippageBps, ord.Base.FromAddress, true, ord.Base.ExecutedHeight, 0, makerLimit)
	} else {
		trades, err = MatchSpotOrderAtHeight(sv, pair, ord.Side, amountBI, priceBI, ord.IsMarket, ord.MaxSlippageBps, ord.Base.FromAddress, ord.Base.ExecutedHeight)
	}
	if err != nil {
		return nil, failedReceipt(ord.Base, fmt.Sprintf("matching failed: %v", err)), err
	}

	                                                               
	tradeEvents := make([]SpotTradeUpdate, 0, len(trades)*2)
	for _, tr := range trades {
		               
		tradeEvents = append(tradeEvents, SpotTradeUpdate{
			OrderID:    ord.Base.TxId,
			TradeAmt:   tr.Quantity,
			TradePrice: tr.Price,
			IsFilled:   false,                                                
		})
		               
		tradeEvents = append(tradeEvents, SpotTradeUpdate{
			OrderID:    tr.MakerOrderID,
			TradeAmt:   tr.Quantity,
			TradePrice: tr.Price,
			IsFilled:   tr.MakerFilled,
		})
	}

	                                                              
	matchedQty := new(big.Int)
	for _, tr := range trades {
		matchedQty.Add(matchedQty, tr.Quantity)
	}
	takerFilled := matchedQty.Cmp(amountBI) >= 0
	                                  
	for i := range tradeEvents {
		if tradeEvents[i].OrderID == ord.Base.TxId {
			tradeEvents[i].IsFilled = takerFilled
		}
	}

	if err := NewSpotEngine(sv).generateWriteOpsFromTrades(ord, tradeEvents, sv, pair); err != nil {
		return nil, failedReceipt(ord.Base, fmt.Sprintf("failed to generate write ops: %v", err)), err
	}
	if _, err := registerSpotMarket(sv, ord.BaseToken, ord.QuoteToken); err != nil {
		return nil, failedReceipt(ord.Base, fmt.Sprintf("failed to register spot market: %v", err)), err
	}

	r := &Receipt{
		TxID:   ord.Base.TxId,
		Status: "SUCCEED",
	}
	                                                                                        
	for _, tr := range trades {
		r.AppendEvent(EventOrderMatched, map[string]string{
			"pair":           pair,
			"taker":          ord.Base.FromAddress,
			"maker":          tr.MakerOwner,
			"maker_order_id": tr.MakerOrderID,
			"quantity":       tr.Quantity.String(),
			"price":          tr.Price.String(),
		})
	}
	return nil, r, nil
}

                                             
func (h *OrderTxHandler) handleRemoveOrder(ord *pb.OrderTx, sv StateView) ([]WriteOp, *Receipt, error) {
	if ord.OpTargetId == "" {
		return nil, failedReceipt(ord.Base, "op_target_id is required for REMOVE operation"), fmt.Errorf("op_target_id is required")
	}

	targetStateKey := keys.KeyOrderState(ord.OpTargetId)
	targetStateData, exists, err := sv.Get(targetStateKey)
	if err != nil || !exists {
		return nil, failedReceipt(ord.Base, "target order not found"), fmt.Errorf("target order not found: %s", ord.OpTargetId)
	}

	var targetState pb.OrderState
	if err := unmarshalProtoCompat(targetStateData, &targetState); err != nil {
		return nil, failedReceipt(ord.Base, "failed to parse target order state"), err
	}

	if targetState.Owner != ord.Base.FromAddress {
		return nil, failedReceipt(ord.Base, "only order creator can remove the order"), fmt.Errorf("permission denied")
	}
	if targetState.Status != pb.OrderStateStatus_ORDER_OPEN || targetState.IsFilled {
		return nil, failedReceipt(ord.Base, "order not open"), fmt.Errorf("order not open, status=%d", targetState.Status)
	}
	oldTargetState := proto.Clone(&targetState).(*pb.OrderState)

	accountKey := keys.KeyAccount(ord.Base.FromAddress)
	accountData, exists, err := sv.Get(accountKey)
	if err != nil || !exists {
		return nil, failedReceipt(ord.Base, "account not found"), fmt.Errorf("account not found")
	}

	var account pb.Account
	if err := unmarshalProtoCompat(accountData, &account); err != nil {
		return nil, failedReceipt(ord.Base, "failed to parse account"), err
	}

	totalAmount, err := parseBalanceStrict("order amount", targetState.Amount)
	if err != nil {
		return nil, failedReceipt(ord.Base, "invalid order state amount"), err
	}
	filledBase, err := parseBalanceStrict("order filled_base", targetState.FilledBase)
	if err != nil {
		return nil, failedReceipt(ord.Base, "invalid order state filled_base"), err
	}
	toRefundBase, err := SafeSub(totalAmount, filledBase)
	if err != nil {
		return nil, failedReceipt(ord.Base, "invalid order state: filled exceeds amount"), err
	}
	if toRefundBase.Sign() <= 0 {
		return nil, failedReceipt(ord.Base, "no unfilled quantity"), fmt.Errorf("no unfilled quantity")
	}

	                                                                      
	ledger := NewLedger(sv)
	if targetState.Side == pb.OrderSide_SELL {
		if err := ledger.Move(MoveRequest{
			Account: ord.Base.FromAddress, Token: targetState.BaseToken, Amount: toRefundBase,
			From: BucketOrderFrozen, To: BucketAvailable,
			Reason: "spot_order_cancel_unfreeze_sell",
			Height: ord.Base.ExecutedHeight, TxID: ord.Base.TxId,
			Ref: ord.OpTargetId,
		}); err != nil {
			return nil, failedReceipt(ord.Base, fmt.Sprintf("unfreeze %s: %v", targetState.BaseToken, err)), err
		}
	} else {
		limitPrice, err := parseBalanceStrict("order price", targetState.Price)
		if err != nil {
			return nil, failedReceipt(ord.Base, "invalid order state price"), err
		}
		toRefundQuote, err := QuoteFromBasePrice(toRefundBase, limitPrice)
		if err != nil {
			return nil, failedReceipt(ord.Base, "refund overflow"), err
		}
		if err := ledger.Move(MoveRequest{
			Account: ord.Base.FromAddress, Token: targetState.QuoteToken, Amount: toRefundQuote,
			From: BucketOrderFrozen, To: BucketAvailable,
			Reason: "spot_order_cancel_unfreeze_buy",
			Height: ord.Base.ExecutedHeight, TxID: ord.Base.TxId,
			Ref: ord.OpTargetId,
		}); err != nil {
			return nil, failedReceipt(ord.Base, fmt.Sprintf("unfreeze %s: %v", targetState.QuoteToken, err)), err
		}
	}

	                                                                                     
	targetState.Status = pb.OrderStateStatus_ORDER_CANCELLED
	if err := writeSpotOrderSnapshot(sv, &targetState, oldTargetState); err != nil {
		return nil, failedReceipt(ord.Base, "failed to write order state"), err
	}

	updatedAccountData, err := MarshalProto(&account)
	if err != nil {
		return nil, failedReceipt(ord.Base, "failed to marshal account"), err
	}
	sv.Set(accountKey, updatedAccountData)

	pair := utils.GeneratePairKey(targetState.BaseToken, targetState.QuoteToken)
	r := &Receipt{
		TxID:   ord.Base.TxId,
		Status: "SUCCEED",
	}
	r.AppendEvent(EventOrderCancelled, map[string]string{
		"order_id": ord.OpTargetId,
		"owner":    ord.Base.FromAddress,
		"pair":     pair,
	})
	return nil, r, nil
}

func (h *OrderTxHandler) Apply(tx *pb.AnyTx) error {
	return ErrNotImplemented
}
