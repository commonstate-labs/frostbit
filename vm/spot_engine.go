package vm

import (
	"dex/keys"
	"dex/pb"
	"fmt"
	"math/big"
	"sort"

	"google.golang.org/protobuf/proto"
)

                                                                                                              
type SpotEngine struct {
	sv StateView
}

func NewSpotEngine(sv StateView) *SpotEngine {
	return &SpotEngine{sv: sv}
}

func syncSpotAccountOrderIndex(sv StateView, st *pb.OrderState, data []byte, oldStates ...*pb.OrderState) {
	newKey := keys.KeySpotAccountOrderFromState(st)
	for _, old := range oldStates {
		oldKey := keys.KeySpotAccountOrderFromState(old)
		if oldKey != "" && oldKey != newKey {
			sv.Del(oldKey)
		}
	}
	if newKey != "" {
		sv.Set(newKey, data)
	}
	syncSpotOpenOrderMarker(sv, st, oldStates...)
}

                                                                          
  
                                                                                          
                                                                                                          
  
                        
  
                                                                    
                                                 
                                                                                           
                                                       
                                                                         
                                                                
                                                       
  
                                                                                                                
                                                                                                           
func (e *SpotEngine) updateAccountBalancesFromStates(
	newOrd *pb.OrderTx,
	tradeEvents []SpotTradeUpdate,
	stateUpdates map[string]*pb.OrderState,
	sv StateView,
) error {
	ledger := NewLedger(sv)
	height := newOrd.Base.ExecutedHeight
	takerTxID := newOrd.Base.TxId

	if err := validateSpotTradeEventPairs(newOrd, tradeEvents, stateUpdates); err != nil {
		return err
	}

	for _, ev := range tradeEvents {
		orderState := stateUpdates[ev.OrderID]
		address := orderState.Owner
		tradeAmtBI := ev.TradeAmt
		tradePriceBI := ev.TradePrice
		tradeQuoteAmt, err := QuoteFromBasePrice(tradeAmtBI, tradePriceBI)
		if err != nil {
			return err
		}

		if orderState.Side == pb.OrderSide_SELL {
			                                               
			if err := ledger.Move(MoveRequest{
				Account: address, Token: orderState.BaseToken, Amount: tradeAmtBI,
				From: BucketOrderFrozen, To: BucketExternal,
				Reason: "spot_trade_seller_deliver_base",
				Height: height, TxID: takerTxID, Ref: ev.OrderID,
			}); err != nil {
				return fmt.Errorf("seller deliver base: %w", err)
			}
			                     
			if err := ledger.Move(MoveRequest{
				Account: address, Token: orderState.QuoteToken, Amount: tradeQuoteAmt,
				From: BucketExternal, To: BucketAvailable,
				Reason: "spot_trade_seller_receive_quote",
				Height: height, TxID: takerTxID, Ref: ev.OrderID,
			}); err != nil {
				return fmt.Errorf("seller receive quote: %w", err)
			}
			continue
		}

		                        
		isMarketBuy := ev.OrderID == newOrd.Base.TxId && newOrd.IsMarket
		if isMarketBuy {
			                                                              
			if err := ledger.Move(MoveRequest{
				Account: address, Token: orderState.QuoteToken, Amount: tradeQuoteAmt,
				From: BucketAvailable, To: BucketExternal,
				Reason: "spot_market_buy_pay",
				Height: height, TxID: takerTxID, Ref: ev.OrderID,
			}); err != nil {
				return fmt.Errorf("market buy pay: %w", err)
			}
			if err := ledger.Move(MoveRequest{
				Account: address, Token: orderState.BaseToken, Amount: tradeAmtBI,
				From: BucketExternal, To: BucketAvailable,
				Reason: "spot_market_buy_receive",
				Height: height, TxID: takerTxID, Ref: ev.OrderID,
			}); err != nil {
				return fmt.Errorf("market buy receive: %w", err)
			}
			continue
		}

		                                                
		limitPrice, err := parseBalanceStrict("order price", orderState.Price)
		if err != nil {
			return err
		}
		frozenToDeduct, err := QuoteFromBasePrice(tradeAmtBI, limitPrice)
		if err != nil {
			return err
		}
		if err := ledger.Move(MoveRequest{
			Account: address, Token: orderState.QuoteToken, Amount: frozenToDeduct,
			From: BucketOrderFrozen, To: BucketExternal,
			Reason: "spot_trade_buyer_pay_quote",
			Height: height, TxID: takerTxID, Ref: ev.OrderID,
		}); err != nil {
			return fmt.Errorf("buyer pay quote: %w", err)
		}
		if err := ledger.Move(MoveRequest{
			Account: address, Token: orderState.BaseToken, Amount: tradeAmtBI,
			From: BucketExternal, To: BucketAvailable,
			Reason: "spot_trade_buyer_receive_base",
			Height: height, TxID: takerTxID, Ref: ev.OrderID,
		}); err != nil {
			return fmt.Errorf("buyer receive base: %w", err)
		}
		                                        
		if tradePriceBI.Cmp(limitPrice) < 0 {
			refundQuote := new(big.Int).Sub(frozenToDeduct, tradeQuoteAmt) // safe-math-ok: 下一步拒绝负差额，实际退款经过 Ledger.Move 余额校验
			if refundQuote.Sign() < 0 {
				return fmt.Errorf("buyer frozen quote below trade quote: frozen=%s trade=%s", frozenToDeduct, tradeQuoteAmt)
			}
			if refundQuote.Sign() > 0 {
				if err := ledger.Move(MoveRequest{
					Account: address, Token: orderState.QuoteToken, Amount: refundQuote,
					From: BucketExternal, To: BucketAvailable,
					Reason: "spot_trade_buyer_refund_diff",
					Height: height, TxID: takerTxID, Ref: ev.OrderID,
				}); err != nil {
					return fmt.Errorf("buyer refund: %w", err)
				}
			}
		}
	}
	return nil
}

                                                                                              
                                                                               
func (e *SpotEngine) generateWriteOpsFromTrades(
	newOrd *pb.OrderTx,
	tradeEvents []SpotTradeUpdate,
	sv StateView,
	pair string,
) error {
	                                                   
	if len(tradeEvents) == 0 {
		return e.saveNewOrder(newOrd, sv)
	}

	                              
	stateUpdates := make(map[string]*pb.OrderState)                                 
	oldStateByID := make(map[string]*pb.OrderState)

	for _, ev := range tradeEvents {
		                                
		var orderState *pb.OrderState
		if cached, ok := stateUpdates[ev.OrderID]; ok {
			                           
			orderState = cached
		} else if ev.OrderID == newOrd.Base.TxId {
			                                    
			orderState = &pb.OrderState{
				OrderId:          newOrd.Base.TxId,
				FilledBase:       "0",
				FilledQuote:      "0",
				IsFilled:         false,
				Status:           pb.OrderStateStatus_ORDER_OPEN,
				CreateHeight:     newOrd.Base.ExecutedHeight,
				LastUpdateHeight: newOrd.Base.ExecutedHeight,
				BaseToken:        newOrd.BaseToken,
				QuoteToken:       newOrd.QuoteToken,
				Amount:           newOrd.Amount,
				Price:            newOrd.Price,
				Side:             newOrd.Side,
				Owner:            newOrd.Base.FromAddress,
			}
		} else {
			                                                                                       
			orderStateKey := keys.KeyOrderState(ev.OrderID)
			orderStateData, exists, err := sv.Get(orderStateKey)
			if err != nil {
				return fmt.Errorf("load order state %s: %w", ev.OrderID, err)
			}
			if !exists || len(orderStateData) == 0 {
				return fmt.Errorf("missing order state for matched spot order %s", ev.OrderID)
			}
			orderState = orderStatePool.Get().(*pb.OrderState)
			orderState.Reset()
			if err := unmarshalProtoCompat(orderStateData, orderState); err != nil {
				orderStatePool.Put(orderState)
				return fmt.Errorf("unmarshal order state %s: %w", ev.OrderID, err)
			}
			oldStateByID[ev.OrderID] = proto.Clone(orderState).(*pb.OrderState)
		}

		                  
		filledBase, err := parseBalanceStrict("order filled_base", orderState.FilledBase)
		if err != nil {
			return fmt.Errorf("invalid order state filled_base for %s: %w", ev.OrderID, err)
		}
		filledQuote, err := parseBalanceStrict("order filled_quote", orderState.FilledQuote)
		if err != nil {
			return fmt.Errorf("invalid order state filled_quote for %s: %w", ev.OrderID, err)
		}
		tradeAmtBI := ev.TradeAmt
		tradePriceBI := ev.TradePrice
		tradeQuoteBI, err := QuoteFromBasePrice(tradeAmtBI, tradePriceBI)
		if err != nil {
			return fmt.Errorf("trade quote overflow for %s: %w", ev.OrderID, err)
		}
		newFilledBase, err := SafeAdd(filledBase, tradeAmtBI)
		if err != nil {
			return fmt.Errorf("filled_base overflow for %s: %w", ev.OrderID, err)
		}
		newFilledQuote, err := SafeAdd(filledQuote, tradeQuoteBI)
		if err != nil {
			return fmt.Errorf("filled_quote overflow for %s: %w", ev.OrderID, err)
		}

		orderState.FilledBase = newFilledBase.String()
		orderState.FilledQuote = newFilledQuote.String()
		orderState.IsFilled = ev.IsFilled
		if ev.IsFilled {
			orderState.Status = pb.OrderStateStatus_ORDER_FILLED
		}

		stateUpdates[ev.OrderID] = orderState

	}
	if err := validateSpotTradeEventPairs(newOrd, tradeEvents, stateUpdates); err != nil {
		return err
	}

	                              
	orderIDs := make([]string, 0, len(stateUpdates))
	for orderID := range stateUpdates {
		orderIDs = append(orderIDs, orderID)
	}
	sort.Strings(orderIDs)

	for _, orderID := range orderIDs {
		orderState := stateUpdates[orderID]

		isMarketTaker := (orderID == newOrd.Base.TxId) && newOrd.IsMarket
		if isMarketTaker && !orderState.IsFilled {
			orderState.Status = pb.OrderStateStatus_ORDER_CANCELLED

			                                                                    
			if newOrd.Side == pb.OrderSide_SELL {
				amountBI, _ := parsePositiveBalanceStrict("amount", newOrd.Amount)
				filledBI := new(big.Int)
				if orderState.FilledBase != "" {
					filledBI.SetString(orderState.FilledBase, 10)
				}
				remainQty := new(big.Int).Sub(amountBI, filledBI) // safe-math-ok: 仅正的未成交量可退款，Ledger.Move 校验冻结余额

				if remainQty.Sign() > 0 {
					                                                                      
					if err := NewLedger(sv).Move(MoveRequest{
						Account: newOrd.Base.FromAddress, Token: newOrd.BaseToken, Amount: remainQty,
						From: BucketOrderFrozen, To: BucketAvailable,
						Reason: "spot_market_sell_refund_unfilled",
						Height: newOrd.Base.ExecutedHeight, TxID: newOrd.Base.TxId,
					}); err != nil {
						return fmt.Errorf("market sell refund: %w", err)
					}
				}
			}
		}

		if isMarketTaker || orderState.IsFilled || orderState.Status != pb.OrderStateStatus_ORDER_OPEN {
			orderState.Status = normalizeTerminalSpotOrderStatus(orderState)
		}
		if err := writeSpotOrderSnapshot(sv, orderState, oldStateByID[orderID]); err != nil {
			return fmt.Errorf("write spot order snapshot %s: %w", orderID, err)
		}

	}

	               
	if err := e.generateTradeRecordsFromStates(newOrd, tradeEvents, stateUpdates, sv, pair); err != nil {
		return fmt.Errorf("failed to generate trade records: %w", err)
	}
	               
	if err := e.updateAccountBalancesFromStates(newOrd, tradeEvents, stateUpdates, sv); err != nil {
		return fmt.Errorf("failed to update account balances: %w", err)
	}

	                  
	for _, orderID := range orderIDs {
		orderState := stateUpdates[orderID]
		orderStatePool.Put(orderState)
	}
	return nil
}

                                              
                                                                                        
                                                                     
func (e *SpotEngine) saveNewOrder(ord *pb.OrderTx, sv StateView) error {
	                                                         
	if ord.Side == pb.OrderSide_SELL && ord.IsMarket {
		amountBI, _ := parsePositiveBalanceStrict("amount", ord.Amount)
		if err := NewLedger(sv).Move(MoveRequest{
			Account: ord.Base.FromAddress, Token: ord.BaseToken, Amount: amountBI,
			From: BucketOrderFrozen, To: BucketAvailable,
			Reason: "spot_market_sell_zero_fill_refund",
			Height: ord.Base.ExecutedHeight, TxID: ord.Base.TxId,
		}); err != nil {
			return fmt.Errorf("market sell zero-fill refund: %w", err)
		}
	}

	                          
	status := pb.OrderStateStatus_ORDER_OPEN
	if ord.IsMarket {
		status = pb.OrderStateStatus_ORDER_CANCELLED
	}
	orderState := &pb.OrderState{
		OrderId:          ord.Base.TxId,
		FilledBase:       "0",
		FilledQuote:      "0",
		IsFilled:         false,
		Status:           status,
		CreateHeight:     ord.Base.ExecutedHeight,
		LastUpdateHeight: ord.Base.ExecutedHeight,
		BaseToken:        ord.BaseToken,
		QuoteToken:       ord.QuoteToken,
		Amount:           ord.Amount,
		Price:            ord.Price,
		Side:             ord.Side,
		Owner:            ord.Base.FromAddress,
	}

	if err := writeSpotOrderSnapshot(sv, orderState); err != nil {
		return fmt.Errorf("write spot order snapshot: %w", err)
	}
	return nil
}

func normalizeTerminalSpotOrderStatus(st *pb.OrderState) pb.OrderStateStatus {
	if st != nil && st.IsFilled {
		return pb.OrderStateStatus_ORDER_FILLED
	}
	return pb.OrderStateStatus_ORDER_CANCELLED
}

                                                                      
                                                    
func (e *SpotEngine) generateTradeRecordsFromStates(
	newOrd *pb.OrderTx,
	tradeEvents []SpotTradeUpdate,
	stateUpdates map[string]*pb.OrderState,
	sv StateView,
	pair string,
) error {
	if err := validateSpotTradeEventPairs(newOrd, tradeEvents, stateUpdates); err != nil {
		return err
	}
	for i := 0; i < len(tradeEvents)-1; i += 2 {
		takerEv := tradeEvents[i]
		makerEv := tradeEvents[i+1]

		var takerOrderID, makerOrderID string
		var tradePrice, tradeAmount string
		var takerSide pb.OrderSide

		if takerEv.OrderID == newOrd.Base.TxId {
			takerOrderID = takerEv.OrderID
			makerOrderID = makerEv.OrderID
			tradePrice = takerEv.TradePrice.String()
			tradeAmount = takerEv.TradeAmt.String()
			takerSide = newOrd.Side
		} else {
			takerOrderID = makerEv.OrderID
			makerOrderID = takerEv.OrderID
			tradePrice = makerEv.TradePrice.String()
			tradeAmount = makerEv.TradeAmt.String()
			takerSide = stateUpdates[makerOrderID].Side
		}

		takerIDShort := takerOrderID
		if len(takerIDShort) > 8 {
			takerIDShort = takerIDShort[:8]
		}
		makerIDShort := makerOrderID
		if len(makerIDShort) > 8 {
			makerIDShort = makerIDShort[:8]
		}
		tradeSeq := i / 2
		tradeID := fmt.Sprintf("%s_%s_%d", takerIDShort, makerIDShort, tradeSeq)
		tradeTsKey, tradeTsText := deterministicTradeTime(newOrd.Base.ExecutedHeight, tradeSeq)

		tradeRecord := &pb.TradeRecord{
			TradeId:      tradeID,
			Pair:         pair,
			Price:        tradePrice,
			Amount:       tradeAmount,
			MakerOrderId: makerOrderID,
			TakerOrderId: takerOrderID,
			TakerSide:    takerSide,
			Timestamp:    tradeTsText,
			Height:       newOrd.Base.ExecutedHeight,
		}

		tradeData, err := proto.Marshal(tradeRecord)
		if err != nil {
			return fmt.Errorf("failed to marshal trade record: %w", err)
		}

		sv.Set(keys.KeyTradeRecord(pair, tradeTsKey, tradeID), tradeData)

	}
	return nil
}

func validateSpotTradeEventPairs(newOrd *pb.OrderTx, tradeEvents []SpotTradeUpdate, stateUpdates map[string]*pb.OrderState) error {
	if len(tradeEvents)%2 != 0 {
		return fmt.Errorf("unpaired spot trade events: %d", len(tradeEvents))
	}
	for i := 0; i < len(tradeEvents); i += 2 {
		takerEv := tradeEvents[i]
		makerEv := tradeEvents[i+1]
		if takerEv.OrderID != newOrd.Base.TxId {
			return fmt.Errorf("spot trade pair %d taker event mismatch: got %s want %s", i/2, takerEv.OrderID, newOrd.Base.TxId)
		}
		if takerEv.OrderID == makerEv.OrderID {
			return fmt.Errorf("spot trade pair %d self event for order %s", i/2, takerEv.OrderID)
		}
		if takerEv.TradeAmt == nil || makerEv.TradeAmt == nil || takerEv.TradeAmt.Cmp(makerEv.TradeAmt) != 0 {
			return fmt.Errorf("spot trade pair %d amount mismatch", i/2)
		}
		if takerEv.TradePrice == nil || makerEv.TradePrice == nil || takerEv.TradePrice.Cmp(makerEv.TradePrice) != 0 {
			return fmt.Errorf("spot trade pair %d price mismatch", i/2)
		}
		takerState, ok := stateUpdates[takerEv.OrderID]
		if !ok {
			return fmt.Errorf("missing order state for spot trade event %s", takerEv.OrderID)
		}
		makerState, ok := stateUpdates[makerEv.OrderID]
		if !ok {
			return fmt.Errorf("missing order state for spot trade event %s", makerEv.OrderID)
		}
		if err := validateSpotOrderStateForTrade(newOrd, takerEv.OrderID, takerState); err != nil {
			return fmt.Errorf("invalid taker state: %w", err)
		}
		if err := validateSpotOrderStateForTrade(newOrd, makerEv.OrderID, makerState); err != nil {
			return fmt.Errorf("invalid maker state: %w", err)
		}
		if takerState.Side != newOrd.Side {
			return fmt.Errorf("spot taker side mismatch for %s: got %s want %s", takerEv.OrderID, takerState.Side, newOrd.Side)
		}
		if takerState.Side == makerState.Side {
			return fmt.Errorf("spot trade pair %d has same-side states: %s", i/2, takerState.Side)
		}
	}
	return nil
}

func validateSpotOrderStateForTrade(newOrd *pb.OrderTx, orderID string, state *pb.OrderState) error {
	if state == nil {
		return fmt.Errorf("nil order state for %s", orderID)
	}
	if state.OrderId != orderID {
		return fmt.Errorf("order id mismatch: key=%s state=%s", orderID, state.OrderId)
	}
	if state.Owner == "" {
		return fmt.Errorf("missing owner for %s", orderID)
	}
	if state.Side != pb.OrderSide_BUY && state.Side != pb.OrderSide_SELL {
		return fmt.Errorf("invalid side for %s: %s", orderID, state.Side)
	}
	if state.BaseToken != newOrd.BaseToken || state.QuoteToken != newOrd.QuoteToken {
		return fmt.Errorf("pair mismatch for %s: got %s_%s want %s_%s", orderID, state.BaseToken, state.QuoteToken, newOrd.BaseToken, newOrd.QuoteToken)
	}
	return nil
}
