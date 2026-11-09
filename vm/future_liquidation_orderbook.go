package vm

import (
	"dex/future"
	"dex/keys"
	"dex/pb"
	"dex/utils"
	"fmt"
	"math/big"

	"google.golang.org/protobuf/proto"
)

                                                                                                               
  
                                                                                                    
                                                                                                             
                                                                                                    
  
                                                                                  
  
                               
                                                                                                               
                                                                                               
                                        
                                                                                         
                                                                                 
                                                              
                                                                                                               
  
                                                                                                              
                                                              
  
                            
                                                                                                     
                                                                            

                                                                                              
                                                                                                                     
const LiquidationOrderbookBufferBps uint32 = 500

                                                                              
                                                                             
                                                                               
                                                                              
                                                                         
func liquidationClosePrice(sumQuote, sumQty *big.Int, positionSide future.Side) *big.Int {
	price, remainder := new(big.Int).QuoRem(sumQuote, sumQty, new(big.Int))
	if positionSide == future.Short && remainder.Sign() > 0 {
		price.Add(price, big.NewInt(1))
	}
	return price
}

                                                                      
                                                                                                    
                                                                                                 
                                                                                    
                                                                          
func tryOrderbookLiquidate(sv StateView, pair string, height uint64, eng *future.Engine) []*future.LiquidationResult {
	markPriceData, exists, _ := sv.Get(keys.KeyFutureMarkPrice(pair))
	if !exists || len(markPriceData) == 0 {
		return nil
	}
	markPrice := new(big.Int).SetBytes(markPriceData)
	if markPrice.Sign() <= 0 {
		return nil
	}

	atRisk, err := eng.FindAtRiskPositions(pair, markPrice)
	if err != nil {
		return nil
	}
	if len(atRisk) == 0 {
		return nil
	}
	cfg, err := eng.GetMarketConfig(pair)
	if err != nil || cfg == nil {
		return nil
	}

	results := make([]*future.LiquidationResult, 0)

	posStore := &svPositionStore{sv: sv}
	for _, posID := range atRisk {
		pos, err := posStore.GetPosition(posID)
		if err != nil || pos == nil || pos.Status != future.StatusOpen {
			continue
		}

		                                                                                                         
		                                                                                                 
		                                                             
		pending := eng.PreviewPendingFunding(pos)
		projectedMargin := new(big.Int).Add(pos.Margin, pending) // safe-math-ok: 资金费可为负，清算预览随后将负权益归零
		if projectedMargin.Sign() < 0 {
			projectedMargin = new(big.Int)
		}
		if !future.ShouldLiquidatePosition(pos, projectedMargin, markPrice, cfg.MaintenanceMarginRateOrDefault()) {
			                                                                                             
			                                                   
			continue
		}

		                                                                            
		fundingSnap := sv.Snapshot()
		if err := eng.ApplyPendingFunding(pos, cfg); err != nil {
			_ = sv.Revert(fundingSnap)
			continue
		}
		pos, _ = posStore.GetPosition(posID)
		if pos == nil || pos.Status != future.StatusOpen {
			                                                                                       
			continue
		}

		if res := tryOrderbookLiquidatePosition(sv, pair, height, eng, cfg, markPrice, pos); res != nil {
			results = append(results, res)
		}
	}

	return results
}

func tryOrderbookLiquidatePosition(sv StateView, pair string, height uint64, eng *future.Engine, cfg *future.MarketConfig, markPrice *big.Int, pos *future.Position) *future.LiquidationResult {
	res, _ := tryOrderbookLiquidatePositionChecked(sv, pair, height, eng, cfg, markPrice, pos)
	return res
}

                                                                        
                                                                            
                                                                              
                                                                         
                                         
func tryOrderbookLiquidatePositionChecked(sv StateView, pair string, height uint64, eng *future.Engine, cfg *future.MarketConfig, markPrice *big.Int, pos *future.Position) (*future.LiquidationResult, error) {
	if pos == nil || pos.Status != future.StatusOpen || markPrice == nil || markPrice.Sign() <= 0 {
		return nil, nil
	}

	takerSide := pb.OrderSide_SELL
	if pos.Side == future.Short {
		takerSide = pb.OrderSide_BUY
	}

	if healthy, err := applyLiquidationSelfMarginTopups(sv, pair, height, eng, cfg, markPrice, pos); err != nil {
		return nil, fmt.Errorf("apply liquidation self-margin topups for %s: %w", pos.ID, err)
	} else if healthy {
		return nil, nil
	}

	snap := sv.Snapshot()
	rollback := func(cause error) (*future.LiquidationResult, error) {
		if revertErr := sv.Revert(snap); revertErr != nil {
			return nil, fmt.Errorf("%v (revert liquidation attempt: %w)", cause, revertErr)
		}
		return nil, cause
	}
	trades, err := MatchFutureOrderForLiquidationAtHeight(sv, pair, takerSide, pos.Quantity, markPrice, true, LiquidationOrderbookBufferBps, pos.Owner, height)
	if err != nil {
		return rollback(fmt.Errorf("match liquidation orderbook for %s: %w", pos.ID, err))
	}

	sumQty := new(big.Int)
	sumQuote := new(big.Int)
	overflow := false
	for _, tr := range trades {
		if nextQty, err := SafeAdd(sumQty, tr.Quantity); err != nil {
			overflow = true
			break
		} else {
			sumQty = nextQty
		}
		seg, err := SafeMul(tr.Price, tr.Quantity)
		if err != nil {
			overflow = true
			break
		}
		if nextQuote, err := SafeAdd(sumQuote, seg); err != nil {
			overflow = true
			break
		} else {
			sumQuote = nextQuote
		}
	}
	if overflow {
		return rollback(fmt.Errorf("aggregate liquidation orderbook fills for %s: integer overflow", pos.ID))
	}
	if sumQty.Sign() == 0 {
		if _, err := rollback(nil); err != nil {
			return nil, err
		}
		return nil, nil
	}

	avgPrice := liquidationClosePrice(sumQuote, sumQty, pos.Side)
	isPartial := sumQty.Cmp(pos.Quantity) < 0

	                                                                          
	                                                                         
	                                                                           
	                                                                           
	                                                                          
	                                                            
	var res *future.LiquidationResult
	if isPartial {
		res, err = eng.LiquidatePartial(pos, avgPrice, sumQty, cfg)
	} else {
		res, err = eng.LiquidateAtPrice(pos, avgPrice, cfg)
	}
	if err != nil {
		return rollback(fmt.Errorf("seize liquidated margin for %s: %w", pos.ID, err))
	}
	if err := eng.BeginRealizedPnLBatch(); err != nil {
		return rollback(fmt.Errorf("begin liquidation maker PnL batch for %s: %w", pos.ID, err))
	}
	batchActive := true
	defer func() {
		if batchActive {
			eng.AbortRealizedPnLBatch()
		}
	}()

	for i, tr := range trades {
		if tr.MakerOwner == "" || tr.MakerLeverage == 0 {
			return rollback(fmt.Errorf("invalid liquidation maker metadata: order=%s owner=%q leverage=%d", tr.MakerOrderID, tr.MakerOwner, tr.MakerLeverage))
		}
		makerSide := future.Side(tr.MakerSide)
		makerCloseQty, makerOpenQty, err := futureTradeExposureSplit(sv, tr.MakerOwner, pair, makerSide, tr.Quantity)
		if err != nil {
			return rollback(fmt.Errorf("split liquidation maker exposure for %s: %w", tr.MakerOrderID, err))
		}
		makerPortion, err := consumeFutureOrderReservedMargin(
			sv, tr.MakerOrderID, tr.MakerOwner, pair, cfg.QuoteToken, makerSide,
			tr.Quantity, tr.MakerRemainBefore, makerOpenQty, tr.Price, tr.MakerLeverage,
			tr.MakerFilled, height,
		)
		if err != nil {
			return rollback(fmt.Errorf("consume liquidation maker reserve for %s: %w", tr.MakerOrderID, err))
		}
		if makerCloseQty.Sign() > 0 {
			if err := eng.ProcessTradeForPosition(tr.MakerOwner, pair, makerSide, makerCloseQty, tr.Price, new(big.Int), height); err != nil {
				return rollback(fmt.Errorf("close liquidation maker position for %s: %w", tr.MakerOrderID, err))
			}
		}
		if makerOpenQty.Sign() > 0 {
			if err := eng.ProcessTradeForPosition(tr.MakerOwner, pair, makerSide, makerOpenQty, tr.Price, makerPortion, height); err != nil {
				return rollback(fmt.Errorf("open liquidation maker position for %s: %w", tr.MakerOrderID, err))
			}
		}
		tradeTsKey, tradeTsText := deterministicTradeTime(height, i)
		tradeRecord := &pb.TradeRecord{
			TradeId:      fmt.Sprintf("liq_ob_%s_%d", pos.ID, i),
			Pair:         pair,
			Price:        tr.Price.String(),
			Amount:       tr.Quantity.String(),
			TakerSide:    takerSide,
			MakerOrderId: tr.MakerOrderID,
			Timestamp:    tradeTsText,
			Height:       height,
		}
		if data, err := proto.Marshal(tradeRecord); err == nil {
			sv.Set(keys.KeyFutureTradeRecord(pair, tradeTsKey, tradeRecord.TradeId), data)
		}
	}
	batchResult, err := eng.CommitRealizedPnLBatch()
	if err != nil {
		return rollback(fmt.Errorf("commit liquidation maker PnL batch for %s: %w", pos.ID, err))
	}
	batchActive = false
	                                                                        
	                                                                        
	                                                                               
	                                                                            
	                                                                         
	                                                                         
	if res != nil && res.ADLShortfall != nil && res.ADLShortfall.Sign() > 0 {
		absorbed := batchResult.ProfitClaimsForPair(pair)
		newFunding := batchResult.LossContributionsForPair(pair)
		newFunding.Add(newFunding, batchResult.InsuranceInflowsForPair(pair))
		if res.MarginLost != nil && res.MarginLost.Sign() > 0 {
			newFunding.Add(newFunding, res.MarginLost)
		}
		absorbed.Sub(absorbed, newFunding)
		if absorbed.Sign() < 0 {
			absorbed.SetInt64(0)
		}
		if absorbed.Cmp(res.ADLShortfall) > 0 {
			absorbed.Set(res.ADLShortfall)
		}
		res.ADLShortfall.Sub(res.ADLShortfall, absorbed)
		res.NeedsADL = res.ADLShortfall.Sign() > 0
	}
	recordUserActivity(sv, pos.Owner, &UserActivityRecord{
		Kind: ActKindLiquidation, Height: height,
		Amount: sumQty.String(), Token: pair,
		Ref:  pos.ID,
		Note: fmt.Sprintf("via=orderbook close_price=%s side=%d partial=%t", avgPrice.String(), int(pos.Side), isPartial),
	})

	liqOrderID := fmt.Sprintf("liq_ob_%s", pos.ID)
	liqNotional := future.CalcNotional(sumQty, avgPrice)
	liqState := &pb.FutureOrderState{
		OrderId: liqOrderID, Owner: pos.Owner, Pair: pair,
		Side: int32(takerSide), Price: avgPrice.String(),
		Amount: sumQty.String(), FilledBase: sumQty.String(),
		FilledQuote: liqNotional.String(),
		IsClose:     true, ClosePositionId: pos.ID,
		Status:       pb.FutureOrderStatus_FUTURE_ORDER_LIQUIDATED,
		OwnerAddress: pos.Owner, CreateHeight: height,
	}
	_ = writeFutureOrderSnapshot(sv, liqState)
	return res, nil
}

func applyLiquidationSelfMarginTopups(sv StateView, pair string, height uint64, eng *future.Engine, cfg *future.MarketConfig, markPrice *big.Int, pos *future.Position) (bool, error) {
	if sv == nil || eng == nil || cfg == nil || pos == nil || pos.Owner == "" || pos.Quantity == nil || pos.Quantity.Sign() <= 0 || markPrice == nil || markPrice.Sign() <= 0 {
		return false, nil
	}
	snap := sv.Snapshot()
	healthy, err := applyLiquidationSelfMarginTopupsLocked(sv, pair, height, eng, cfg, markPrice, pos)
	if err != nil {
		_ = sv.Revert(snap)
		return false, err
	}
	return healthy, nil
}

func applyLiquidationSelfMarginTopupsLocked(sv StateView, pair string, height uint64, eng *future.Engine, cfg *future.MarketConfig, markPrice *big.Int, pos *future.Position) (bool, error) {
	takerSide := pb.OrderSide_SELL
	counterSide := pb.OrderSide_BUY
	useReverse := true
	if pos.Side == future.Short {
		takerSide = pb.OrderSide_BUY
		counterSide = pb.OrderSide_SELL
		useReverse = false
	}

	prefix := keys.KeyFutureOrderPriceIndexPrefix(pair, counterSide, false)
	worstBuyKey, worstSellKey := liquidationWorstPriceKeys(markPrice, LiquidationOrderbookBufferBps)
	posStore := &svPositionStore{sv: sv}
	targetQty := new(big.Int).Set(pos.Quantity)
	scannedQty := new(big.Int)
	startAfter := ""
	topupApplied := false
	touchedMakerExposure := make(map[string]struct{})
	const batchSize = 200

	for scannedQty.Cmp(targetQty) < 0 {
		entries, err := scanLiquidationOrderbookEntries(sv, prefix, startAfter, batchSize, useReverse)
		if err != nil {
			return false, err
		}
		if len(entries) == 0 {
			break
		}

		stop := false
		lastScannedKey := ""
		for _, entry := range entries {
			indexKey := entry.indexKey(prefix)
			lastScannedKey = indexKey
			if entry.orderID == "" || entry.priceKey == "" {
				if indexKey != "" {
					sv.Del(indexKey)
				}
				continue
			}
			if liquidationPriceBoundaryExceeded(takerSide, entry.priceKey, worstBuyKey, worstSellKey) {
				stop = true
				break
			}

			maker, err := entry.snapshot.makerSnapshot(pair, entry.priceKey)
			if err != nil || maker == nil || maker.remain == nil || maker.remain.Sign() <= 0 || maker.price == nil || maker.price.Sign() <= 0 {
				sv.Del(indexKey)
				continue
			}
			                                                                       
			                                                                      
			                                                             
			if !futureMakerPredatesHeight(maker, height) {
				continue
			}
			                                                                  
			                                                                      
			                                                                 
			if scannedQty.Cmp(targetQty) >= 0 {
				stop = true
				break
			}

			if maker.owner == pos.Owner {
				state, err := loadFutureOrderStateForTopup(sv, entry.orderID, maker)
				if err != nil {
					return false, err
				}
				if state == nil || state.Status != pb.FutureOrderStatus_FUTURE_ORDER_OPEN || futureOrderStateOwner(state) != pos.Owner || state.IsClose {
					sv.Del(indexKey)
					continue
				}
				consumed, err := consumeLiquidationOwnMakerMargin(sv, state, maker, cfg.QuoteToken, height)
				if err != nil {
					return false, err
				}
				if applyLiquidationSelfMarginTopup(pos, consumed) {
					if err := posStore.SetPosition(pos); err != nil {
						return false, err
					}
					eng.RefreshLiquidationIndex(pos, cfg)
					topupApplied = true
					if !future.ShouldLiquidatePosition(pos, pos.Margin, markPrice, cfg.MaintenanceMarginRateOrDefault()) {
						return true, nil
					}
				}
				                                                                    
				                                                                    
				                                                                  
				continue
			}

			remainingSweep := new(big.Int).Sub(targetQty, scannedQty) // safe-math-ok: 扫描剩余目标仅用于限量，非正值立即停止
			if remainingSweep.Sign() <= 0 {
				stop = true
				break
			}
			sweepQty := new(big.Int).Set(maker.remain)
			if sweepQty.Cmp(remainingSweep) > 0 {
				sweepQty.Set(remainingSweep)
			}
			exposureKey := futureMakerExposureKey(maker)
			_, forceStandalone := touchedMakerExposure[exposureKey]
			if !futureMakerTradeExecutable(sv, pair, pos.Owner, maker, sweepQty, forceStandalone) {
				continue
			}
			scannedQty.Add(scannedQty, sweepQty)
			if exposureKey != "" {
				touchedMakerExposure[exposureKey] = struct{}{}
			}
		}

		if stop || len(entries) < batchSize || lastScannedKey == "" || lastScannedKey == startAfter {
			break
		}
		startAfter = lastScannedKey
	}

	if !topupApplied {
		return false, nil
	}
	return !future.ShouldLiquidatePosition(pos, pos.Margin, markPrice, cfg.MaintenanceMarginRateOrDefault()), nil
}

func applyLiquidationSelfMarginTopup(pos *future.Position, amount *big.Int) bool {
	if pos == nil || amount == nil || amount.Sign() <= 0 {
		return false
	}
	pos.Margin = new(big.Int).Add(nonNilBig(pos.Margin), amount) // safe-math-ok: 调用方已通过 Ledger 扣除 OrderFrozen，按原精度累计转入仓位
	                                                                           
	                                                                           
	                                                 
	pos.MarginBasisAmount = new(big.Int).Set(pos.Margin)
	pos.MarginBasisClosed = new(big.Int)
	return true
}

func scanLiquidationOrderbookEntries(sv StateView, prefix, startAfter string, limit int, reverse bool) ([]futureOrderbookEntry, error) {
	if scanner, ok := sv.(futureOrderbookSnapshotScanner); ok {
		entries, used, err := scanner.ScanFutureOrderbookSnapshots(prefix, startAfter, limit, reverse)
		if err != nil || used {
			return entries, err
		}
	}
	_, side, ok := parseFutureOrderbookPrefix(prefix)
	if !ok {
		return nil, fmt.Errorf("invalid future orderbook prefix: %s", prefix)
	}
	pairs, err := scanStateViewOrderedAfter(sv, prefix, startAfter, limit, reverse)
	if err != nil {
		return nil, err
	}
	out := make([]futureOrderbookEntry, 0, len(pairs))
	for _, p := range pairs {
		orderID := extractOrderIDFromKey(p.Key)
		priceKey := extractPriceKeyFromKey(p.Key)
		out = append(out, futureOrderbookEntry{
			priceKey: priceKey,
			orderID:  orderID,
			snapshot: compactFutureOrderSnapshot(orderID, side, p.Value),
		})
	}
	return out, nil
}

func liquidationWorstPriceKeys(markPrice *big.Int, maxSlippageBps uint32) (string, string) {
	if markPrice == nil || markPrice.Sign() <= 0 || maxSlippageBps == 0 {
		return "", ""
	}
	delta := new(big.Int).Mul(markPrice, big.NewInt(int64(maxSlippageBps))) // safe-math-ok: 滑点价格边界的乘法分子，下一步除以 10000
	delta.Quo(delta, big.NewInt(10_000))
	worstBuy := new(big.Int).Add(markPrice, delta) // safe-math-ok: 只读买入价格上界，不是资金余额
	worstSell := new(big.Int).Sub(markPrice, delta) // safe-math-ok: 只读卖出价格下界，随后将负值归零
	if worstSell.Sign() < 0 {
		worstSell = new(big.Int)
	}
	return priceKeyFromBigInt(worstBuy), priceKeyFromBigInt(worstSell)
}

func liquidationPriceBoundaryExceeded(takerSide pb.OrderSide, priceKey, worstBuyKey, worstSellKey string) bool {
	if takerSide == pb.OrderSide_BUY && worstBuyKey != "" && priceKey > worstBuyKey {
		return true
	}
	if takerSide == pb.OrderSide_SELL && worstSellKey != "" && priceKey < worstSellKey {
		return true
	}
	return false
}

func loadFutureOrderStateForTopup(sv StateView, orderID string, maker *makerOrderSnapshot) (*pb.FutureOrderState, error) {
	if data, exists, err := sv.Get(keys.KeyFutureOrderState(orderID)); err != nil {
		return nil, err
	} else if exists && len(data) > 0 {
		var state pb.FutureOrderState
		if err := proto.Unmarshal(data, &state); err != nil {
			return nil, err
		}
		if state.OrderId == "" {
			state.OrderId = orderID
		}
		return &state, nil
	}
	if maker != nil && maker.future != nil {
		state := proto.Clone(maker.future).(*pb.FutureOrderState)
		if state.OrderId == "" {
			state.OrderId = orderID
		}
		return state, nil
	}
	return nil, fmt.Errorf("future own maker order state missing: order=%s", orderID)
}

func futureOrderStateOwner(state *pb.FutureOrderState) string {
	if state == nil {
		return ""
	}
	if state.Owner != "" {
		return state.Owner
	}
	return state.OwnerAddress
}

func consumeLiquidationOwnMakerMargin(sv StateView, state *pb.FutureOrderState, maker *makerOrderSnapshot, quoteToken string, height uint64) (*big.Int, error) {
	if state == nil || state.OrderId == "" {
		return nil, fmt.Errorf("missing future own maker order")
	}
	if quoteToken == "" {
		quoteToken = "USDT"
	}
	key := keys.KeyFutureOrderReservedMargin(state.OrderId)
	data, exists, err := sv.Get(key)
	if err != nil {
		return nil, err
	}
	amount := new(big.Int)
	if exists {
		amount, err = futureReservedMarginAmount(data)
		if err != nil {
			return nil, fmt.Errorf("decode future own maker reserved margin: order=%s: %w", state.OrderId, err)
		}
	} else {
		price := makerPriceForTopup(state, maker)
		remain := makerRemainForTopup(state, maker)
		amount = future.CalcRequiredMargin(remain, price, state.Leverage)
		if amount.Sign() <= 0 {
			return nil, fmt.Errorf("future own maker reserved margin is missing: order=%s", state.OrderId)
		}
	}
	owner := futureOrderStateOwner(state)
	if amount.Sign() > 0 {
		if err := NewLedger(sv).Move(MoveRequest{
			Account: owner, Token: quoteToken, Amount: amount,
			From: BucketOrderFrozen, To: BucketMargin,
			Reason: "future_liquidation_self_margin_topup",
			Height: height, Ref: state.OrderId,
		}); err != nil {
			return nil, err
		}
	}
	sv.Del(key)

	cancelled := proto.Clone(state).(*pb.FutureOrderState)
	cancelled.Status = pb.FutureOrderStatus_FUTURE_ORDER_CANCELLED
	if err := writeFutureOrderSnapshot(sv, cancelled, state); err != nil {
		return nil, err
	}
	return amount, nil
}

func makerPriceForTopup(state *pb.FutureOrderState, maker *makerOrderSnapshot) *big.Int {
	if maker != nil && maker.price != nil && maker.price.Sign() > 0 {
		return new(big.Int).Set(maker.price)
	}
	return parseFutureOrderBigInt(state.Price)
}

func makerRemainForTopup(state *pb.FutureOrderState, maker *makerOrderSnapshot) *big.Int {
	if maker != nil && maker.remain != nil && maker.remain.Sign() > 0 {
		return new(big.Int).Set(maker.remain)
	}
	amount := parseFutureOrderBigInt(state.Amount)
	filled := parseFutureOrderBigInt(state.FilledBase)
	remain := new(big.Int).Sub(amount, filled) // safe-math-ok: 只用于判断 maker 是否仍有正剩余量，非正值不成交
	if remain.Sign() < 0 {
		return new(big.Int)
	}
	return remain
}

func nonNilBig(v *big.Int) *big.Int {
	if v == nil {
		return new(big.Int)
	}
	return v
}

                                                                  
var _ = utils.GeneratePairKey
