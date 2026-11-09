package vm

import (
	"dex/db"
	"dex/future"
	"dex/keys"
	"dex/pb"
	"dex/rent"
	"fmt"
	"math/big"
	"strings"

	"google.golang.org/protobuf/proto"
)

                                 
type TradeResult struct {
	MakerOrderID      string                                  
	Quantity          *big.Int                
	Price             *big.Int                                  
	MakerFilled       bool                                      
	MakerRemainBefore *big.Int                                                                             
	MakerOwner        string                           
	MakerSide         int32                         
	MakerLeverage     uint64                        
}

                                                          
type OrderIndexKeyFn func(pair string, side pb.OrderSide, isFilled bool) string

                                                   
type OrderStateKeyFn func(orderID string) string

                                                                                     
                                                                                               
                                                                                                                   
type MarketKind int

const (
	MarketSpot   MarketKind = iota                                                     
	MarketFuture                                                       
)

type makerIndexCandidate struct {
	indexKey  string
	orderID   string
	stateKey  string
	stateData []byte
}

type makerOrderSnapshot struct {
	remain   *big.Int
	price    *big.Int
	owner    string
	side     int32
	leverage uint64
	future   *pb.FutureOrderState
	spot     *pb.OrderState
}

                                                                             
                                                                             
                                                                          
                                                                             
func futureMakerPredatesHeight(maker *makerOrderSnapshot, currentHeight uint64) bool {
	if currentHeight == 0 {
		return true
	}
	return maker != nil && maker.future != nil && maker.future.CreateHeight < currentHeight
}

                                                                          
                                                                               
                                                                                
                                                                         
func makerRentCanTrade(sv StateView, owner string, matchHeight, liquidationHeight uint64) (bool, error) {
	if liquidationHeight != 0 || matchHeight == 0 || !rent.IsRentManagedAccount(owner) {
		return true, nil
	}
	settlement, err := settleRentOwner(sv, owner, matchHeight)
	if err != nil {
		return false, err
	}
	return settlement == nil || settlement.Projection == nil || !settlement.Projection.NeedsFunding, nil
}

                                                                            
                                                                       
                                                                         
                                                                          
                                                                             
                                          
func futureMakerReserveCoversTrade(sv StateView, pair, takerOwner string, maker *makerOrderSnapshot, tradeQty *big.Int, forceStandalone bool) bool {
	if maker == nil || maker.future == nil || maker.owner == "" || maker.leverage == 0 ||
		maker.remain == nil || maker.remain.Sign() <= 0 || tradeQty == nil || tradeQty.Sign() <= 0 ||
		tradeQty.Cmp(maker.remain) > 0 || maker.price == nil || maker.price.Sign() <= 0 {
		return false
	}
	makerSide := future.Side(maker.side)
	if makerSide != future.Long && makerSide != future.Short {
		return false
	}

	selfTrade := takerOwner != "" && maker.owner == takerOwner
	standalone := selfTrade || forceStandalone
	openQty := new(big.Int).Set(tradeQty)
	if !standalone {
		_, dynamicOpenQty, err := futureTradeExposureSplit(sv, maker.owner, pair, makerSide, tradeQty)
		if err != nil {
			return false
		}
		openQty = dynamicOpenQty
	}
	fillRequired := future.CalcRequiredMargin(tradeQty, maker.price, maker.leverage)
	if !standalone {
		var err error
		fillRequired, err = futureMakerOpenMarginNeeded(sv, maker.owner, pair, makerSide, openQty, maker.price, maker.leverage)
		if err != nil {
			return false
		}
	}
	remainAfter := new(big.Int).Sub(maker.remain, tradeQty) // safe-math-ok: 调用前已限制 tradeQty <= maker.remain，只读预检余量
	remainderRequired := future.CalcRequiredMargin(remainAfter, maker.price, maker.leverage)

	data, exists, err := sv.Get(keys.KeyFutureOrderReservedMargin(maker.future.OrderId))
	if err != nil {
		return false
	}
	reserved := future.CalcRequiredMargin(maker.remain, maker.price, maker.leverage)
	versionedAllocation := false
	if exists {
		allocation, versioned, decodeErr := decodeFutureReservedMarginAllocation(data)
		if decodeErr != nil {
			return false
		}
		reserved = new(big.Int).Set(allocation.Remaining)
		versionedAllocation = versioned
		if versioned {
			expectedRemain := new(big.Int).Sub(allocation.BaselineQty, allocation.ConsumedQty) // safe-math-ok: 只读预检要求结果与 maker 剩余量严格一致
			if expectedRemain.Cmp(maker.remain) != 0 || allocation.MakerPrice.Cmp(maker.price) != 0 || allocation.MakerLeverage != maker.leverage {
				return false
			}
			nextOpen := new(big.Int).Add(allocation.OpenConsumedQty, openQty) // safe-math-ok: 下一步明确拒绝累计开仓量超过 BaselineQty
			if nextOpen.Cmp(allocation.BaselineQty) > 0 {
				return false
			}
			previousTarget := future.CalcRequiredMargin(allocation.OpenConsumedQty, allocation.MakerPrice, allocation.MakerLeverage)
			nextTarget := future.CalcRequiredMargin(nextOpen, allocation.MakerPrice, allocation.MakerLeverage)
			orderDelta := new(big.Int).Sub(nextTarget, previousTarget) // safe-math-ok: 随后显式拒绝负的累计保证金差额
			if orderDelta.Sign() < 0 {
				return false
			}
			if orderDelta.Cmp(fillRequired) > 0 {
				fillRequired = orderDelta
			}
		}
	}
	                                                                            
	                                                                          
	                                                                           
	if !versionedAllocation {
		legacyOrderDelta := future.CalcRequiredMargin(openQty, maker.price, maker.leverage)
		if legacyOrderDelta.Cmp(fillRequired) > 0 {
			fillRequired = legacyOrderDelta
		}
	}
	cfg, cfgErr := newFutureEngine(sv, 0).GetMarketConfig(pair)
	if cfgErr != nil || cfg == nil || cfg.QuoteToken == "" {
		return false
	}
	actualFrozen := parseBigOrZero(GetBalance(sv, maker.owner, cfg.QuoteToken).OrderFrozenBalance)
	if actualFrozen.Cmp(reserved) < 0 {
		return false
	}
	totalRequired := new(big.Int).Add(fillRequired, remainderRequired) // safe-math-ok: 只读资金需求，返回前必须不超过 reserved
	return reserved.Cmp(totalRequired) >= 0
}

                                                                            
                                                                           
                                                                           
                                                                               
func futureMakerTradeExecutable(sv StateView, pair, takerOwner string, maker *makerOrderSnapshot, tradeQty *big.Int, forceStandalone bool) bool {
	if maker == nil || maker.price == nil {
		return false
	}
	if notional, err := QuoteFromBasePrice(tradeQty, maker.price); err != nil || notional.Sign() == 0 {
		return false
	}
	return futureMakerReserveCoversTrade(sv, pair, takerOwner, maker, tradeQty, forceStandalone)
}

func futureMakerExposureKey(maker *makerOrderSnapshot) string {
	if maker == nil {
		return ""
	}
	return maker.owner + "\x00" + fmt.Sprintf("%d", maker.side)
}

type stateViewBatchGetter interface {
	GetMany(keys []string) (map[string][]byte, error)
}

                                                                                     
  
            
                                                           
                                                           
                                                                       
                                                
                                                                                
                                                            
func matchOrderFromSV(
	sv StateView,
	prefixFn OrderIndexKeyFn,
	stateKeyFn OrderStateKeyFn,
	pair string,
	takerSide pb.OrderSide,
	takerQty *big.Int,
	takerPrice *big.Int,
	isMarket bool,
	batchSize int,
	kind MarketKind,
	maxSlippageBps uint32,
	takerOwner string,
	skipSelfTrade bool,
	matchHeight uint64,
	liquidationHeight uint64,
	scanLimits ...int,
) ([]TradeResult, error) {
	writeMakerState := kind == MarketFuture
	                                                                                    
	var worstBuy, worstSell *big.Int
	if isMarket && maxSlippageBps > 0 && takerPrice != nil && takerPrice.Sign() > 0 {
		delta := new(big.Int).Mul(takerPrice, big.NewInt(int64(maxSlippageBps))) // safe-math-ok: 滑点价格公式的高精度分子，随后除以 10000
		delta.Quo(delta, big.NewInt(10_000))
		worstBuy = new(big.Int).Add(takerPrice, delta) // safe-math-ok: 只读市价单买入价格上界，不写资金余额；BUY 最多接受到这个价
		worstSell = new(big.Int).Sub(takerPrice, delta) // safe-math-ok: 只读卖出价格下界，随后将负值归零；SELL 最少接受到这个价
		if worstSell.Sign() < 0 {
			worstSell = new(big.Int)
		}
	}
	if takerQty == nil || takerQty.Sign() <= 0 {
		return nil, nil
	}
	if batchSize <= 0 {
		batchSize = 200
	}

	                                    
	var counterSide pb.OrderSide
	var useReverse bool
	if takerSide == pb.OrderSide_BUY {
		counterSide = pb.OrderSide_SELL
		useReverse = false                                 
	} else {
		counterSide = pb.OrderSide_BUY
		useReverse = true                                
	}
	counterPrefix := prefixFn(pair, counterSide, false)
	takerPriceKey := priceKeyFromBigInt(takerPrice)
	worstBuyKey := priceKeyFromBigInt(worstBuy)
	worstSellKey := priceKeyFromBigInt(worstSell)

	if kind == MarketFuture {
		if trades, ok, err := matchFutureOrderFromSnapshots(sv, stateKeyFn, pair, takerSide, takerQty, batchSize, counterPrefix, takerPriceKey, worstBuyKey, worstSellKey, isMarket, useReverse, takerOwner, skipSelfTrade, matchHeight, liquidationHeight); ok || err != nil {
			return trades, err
		}
	}

	remain := new(big.Int).Set(takerQty)
	var trades []TradeResult
	touchedMakerExposure := make(map[string]struct{})
	startAfter := ""
	scanBudget := 0
	if len(scanLimits) > 0 {
		scanBudget = scanLimits[0]
	}
	scanned := 0

	                                                                                                                         
	for remain.Sign() > 0 {
		limit := batchSize
		if scanBudget > 0 {
			if scanned >= scanBudget {
				break
			}
			if limit > scanBudget-scanned {
				limit = scanBudget - scanned
			}
		}
		var (
			indexKeys  []string
			indexPairs []KVPair
			err        error
		)
		if kind == MarketFuture {
			indexPairs, err = scanStateViewOrderedAfter(sv, counterPrefix, startAfter, limit, useReverse)
			indexKeys = make([]string, 0, len(indexPairs))
			for _, p := range indexPairs {
				indexKeys = append(indexKeys, p.Key)
			}
		} else {
			indexKeys, err = scanStateViewOrderedKeysAfter(sv, counterPrefix, startAfter, limit, useReverse)
		}
		if err != nil {
			return nil, err
		}
		if len(indexKeys) == 0 {
			break
		}
		                                                                     
		                                                                       
		if scanBudget > 0 && len(indexKeys) > limit {
			indexKeys = indexKeys[:limit]
		}
		scanned += len(indexKeys)

		matchedInBatch := false
		advancedInBatch := false
		stopAtPriceBoundary := false
		lastScannedKey := ""
		candidates := make([]makerIndexCandidate, 0, len(indexKeys))
		stateKeys := make([]string, 0, len(indexKeys))
		seenStateKeys := make(map[string]struct{}, len(indexKeys))

		for i, indexKey := range indexKeys {
			if remain.Sign() <= 0 {
				break
			}
			lastScannedKey = indexKey

			orderID := extractOrderIDFromKey(indexKey)
			priceKey := extractPriceKeyFromKey(indexKey)
			if orderID == "" || priceKey == "" {
				sv.Del(indexKey)
				advancedInBatch = true
				continue
			}
			if !isMarket {
				if takerSide == pb.OrderSide_BUY && takerPriceKey != "" && priceKey > takerPriceKey {
					stopAtPriceBoundary = true
					break
				}
				if takerSide == pb.OrderSide_SELL && takerPriceKey != "" && priceKey < takerPriceKey {
					stopAtPriceBoundary = true
					break
				}
			} else {
				if takerSide == pb.OrderSide_BUY && worstBuyKey != "" && priceKey > worstBuyKey {
					stopAtPriceBoundary = true
					break
				}
				if takerSide == pb.OrderSide_SELL && worstSellKey != "" && priceKey < worstSellKey {
					stopAtPriceBoundary = true
					break
				}
			}

			stateKey := stateKeyFn(orderID)
			c := makerIndexCandidate{indexKey: indexKey, orderID: orderID, stateKey: stateKey}
			if kind == MarketFuture {
				if i < len(indexPairs) {
					c.stateData = indexPairs[i].Value
				}
			} else if _, ok := seenStateKeys[stateKey]; !ok {
				seenStateKeys[stateKey] = struct{}{}
				stateKeys = append(stateKeys, stateKey)
			}
			candidates = append(candidates, c)
		}
		var stateDataByKey map[string][]byte
		if kind != MarketFuture {
			stateDataByKey, err = getStateMany(sv, stateKeys)
			if err != nil {
				return nil, err
			}
		}

		for _, c := range candidates {
			if remain.Sign() <= 0 {
				break
			}
			stateData := c.stateData
			if kind != MarketFuture {
				stateData = stateDataByKey[c.stateKey]
			}
			if len(stateData) == 0 {
				sv.Del(c.indexKey)
				advancedInBatch = true
				continue
			}

			maker, err := parseMakerOrderSnapshot(stateData, kind)
			if err != nil && scanBudget > 0 {
				return nil, fmt.Errorf("rent maker %s: %w", c.orderID, err)
			}
			if err != nil || maker == nil || maker.remain.Sign() <= 0 || maker.price == nil || maker.price.Sign() <= 0 {
				sv.Del(c.indexKey)
				advancedInBatch = true
				continue
			}
			if !futureMakerPredatesHeight(maker, liquidationHeight) {
				advancedInBatch = true
				continue
			}
			if skipSelfTrade && takerOwner != "" && maker.owner == takerOwner {
				advancedInBatch = true
				continue
			}
			rentEligible, rentErr := makerRentCanTrade(sv, maker.owner, matchHeight, liquidationHeight)
			if rentErr != nil {
				return nil, rentErr
			}
			if !rentEligible {
				advancedInBatch = true
				continue
			}

			tradeQty := new(big.Int).Set(remain)
			if tradeQty.Cmp(maker.remain) > 0 {
				tradeQty.Set(maker.remain)
			}
			makerNewRemain := new(big.Int).Sub(maker.remain, tradeQty) // safe-math-ok: 成交量先取 maker 与 taker 剩余量较小值，余量不下溢
			makerFilled := makerNewRemain.Sign() == 0
			exposureKey := futureMakerExposureKey(maker)
			_, forceStandalone := touchedMakerExposure[exposureKey]
			if kind == MarketFuture {
				if !futureMakerTradeExecutable(sv, pair, takerOwner, maker, tradeQty, forceStandalone) {
					advancedInBatch = true
					continue
				}
			} else if notional, nerr := QuoteFromBasePrice(tradeQty, maker.price); nerr != nil || notional.Sign() == 0 {
				advancedInBatch = true
				continue
			}
			if writeMakerState {
				if err := updateMakerOrderSnapshotState(sv, stateKeyFn, kind, c.indexKey, c.orderID, maker, tradeQty, maker.price, makerFilled, matchHeight); err != nil {
					continue
				}
			}
			if makerFilled {
				sv.Del(c.indexKey)
				advancedInBatch = true
			}
			if kind == MarketFuture && exposureKey != "" {
				touchedMakerExposure[exposureKey] = struct{}{}
			}

			trades = append(trades, TradeResult{
				MakerOrderID:      c.orderID,
				Quantity:          tradeQty,
				Price:             new(big.Int).Set(maker.price),
				MakerFilled:       makerFilled,
				MakerRemainBefore: new(big.Int).Set(maker.remain),
				MakerOwner:        maker.owner,
				MakerSide:         maker.side,
				MakerLeverage:     maker.leverage,
			})
			remain.Sub(remain, tradeQty)
			matchedInBatch = true
		}

		if stopAtPriceBoundary {
			break
		}
		if !matchedInBatch && !advancedInBatch {
			break
		}
		if len(indexKeys) < batchSize {
			break
		}
		if lastScannedKey == "" || lastScannedKey == startAfter {
			break
		}
		startAfter = lastScannedKey
	}

	return trades, nil
}

func matchFutureOrderFromSnapshots(
	sv StateView,
	stateKeyFn OrderStateKeyFn,
	pair string,
	takerSide pb.OrderSide,
	takerQty *big.Int,
	batchSize int,
	counterPrefix string,
	takerPriceKey string,
	worstBuyKey string,
	worstSellKey string,
	isMarket bool,
	useReverse bool,
	takerOwner string,
	skipSelfTrade bool,
	matchHeight uint64,
	liquidationHeight uint64,
) ([]TradeResult, bool, error) {
	if visitor, ok := sv.(futureOrderbookSnapshotVisitor); ok {
		if trades, used, err := matchFutureOrderFromSnapshotVisitor(sv, visitor, stateKeyFn, pair, takerSide, takerQty, batchSize, counterPrefix, takerPriceKey, worstBuyKey, worstSellKey, isMarket, useReverse, takerOwner, skipSelfTrade, matchHeight, liquidationHeight); used || err != nil {
			return trades, used, err
		}
	}
	scanner, ok := sv.(futureOrderbookSnapshotScanner)
	if !ok {
		return nil, false, nil
	}
	remain := new(big.Int).Set(takerQty)
	var trades []TradeResult
	touchedMakerExposure := make(map[string]struct{})
	startAfter := ""
	for remain.Sign() > 0 {
		entries, ok, err := scanner.ScanFutureOrderbookSnapshots(counterPrefix, startAfter, batchSize, useReverse)
		if err != nil || !ok {
			return nil, ok, err
		}
		if len(entries) == 0 {
			break
		}

		matchedInBatch := false
		advancedInBatch := false
		stopAtPriceBoundary := false
		var lastScanned futureOrderbookEntry
		lastScannedSet := false

		for _, entry := range entries {
			if remain.Sign() <= 0 {
				break
			}
			lastScanned = entry
			lastScannedSet = true
			priceKey := entry.priceKey
			if entry.orderID == "" || priceKey == "" {
				sv.Del(entry.indexKey(counterPrefix))
				advancedInBatch = true
				continue
			}
			if !isMarket {
				if takerSide == pb.OrderSide_BUY && takerPriceKey != "" && priceKey > takerPriceKey {
					stopAtPriceBoundary = true
					break
				}
				if takerSide == pb.OrderSide_SELL && takerPriceKey != "" && priceKey < takerPriceKey {
					stopAtPriceBoundary = true
					break
				}
			} else {
				if takerSide == pb.OrderSide_BUY && worstBuyKey != "" && priceKey > worstBuyKey {
					stopAtPriceBoundary = true
					break
				}
				if takerSide == pb.OrderSide_SELL && worstSellKey != "" && priceKey < worstSellKey {
					stopAtPriceBoundary = true
					break
				}
			}

			maker, err := entry.snapshot.makerSnapshot(pair, priceKey)
			if err != nil || maker == nil || maker.remain.Sign() <= 0 || maker.price == nil || maker.price.Sign() <= 0 {
				sv.Del(entry.indexKey(counterPrefix))
				advancedInBatch = true
				continue
			}
			if !futureMakerPredatesHeight(maker, liquidationHeight) {
				advancedInBatch = true
				continue
			}
			if skipSelfTrade && takerOwner != "" && maker.owner == takerOwner {
				advancedInBatch = true
				continue
			}
			rentEligible, rentErr := makerRentCanTrade(sv, maker.owner, matchHeight, liquidationHeight)
			if rentErr != nil {
				return nil, true, rentErr
			}
			if !rentEligible {
				advancedInBatch = true
				continue
			}

			tradeQty := new(big.Int).Set(remain)
			if tradeQty.Cmp(maker.remain) > 0 {
				tradeQty.Set(maker.remain)
			}
			makerNewRemain := new(big.Int).Sub(maker.remain, tradeQty) // safe-math-ok: 成交量先取 maker 与 taker 剩余量较小值，余量不下溢
			makerFilled := makerNewRemain.Sign() == 0
			exposureKey := futureMakerExposureKey(maker)
			_, forceStandalone := touchedMakerExposure[exposureKey]
			if !futureMakerTradeExecutable(sv, pair, takerOwner, maker, tradeQty, forceStandalone) {
				advancedInBatch = true
				continue
			}
			indexKey := entry.indexKey(counterPrefix)
			if err := updateMakerOrderSnapshotState(sv, stateKeyFn, MarketFuture, indexKey, entry.orderID, maker, tradeQty, maker.price, makerFilled, matchHeight); err != nil {
				continue
			}
			if makerFilled {
				sv.Del(indexKey)
				advancedInBatch = true
			}
			if exposureKey != "" {
				touchedMakerExposure[exposureKey] = struct{}{}
			}

			trades = append(trades, TradeResult{
				MakerOrderID:      entry.orderID,
				Quantity:          tradeQty,
				Price:             new(big.Int).Set(maker.price),
				MakerFilled:       makerFilled,
				MakerRemainBefore: new(big.Int).Set(maker.remain),
				MakerOwner:        maker.owner,
				MakerSide:         maker.side,
				MakerLeverage:     maker.leverage,
			})
			remain.Sub(remain, tradeQty)
			matchedInBatch = true
		}

		if stopAtPriceBoundary {
			break
		}
		if !matchedInBatch && !advancedInBatch {
			break
		}
		if len(entries) < batchSize {
			break
		}
		if !lastScannedSet {
			break
		}
		lastScannedKey := lastScanned.indexKey(counterPrefix)
		if lastScannedKey == "" || lastScannedKey == startAfter {
			break
		}
		startAfter = lastScannedKey
	}
	return trades, true, nil
}

func matchFutureOrderFromSnapshotVisitor(
	sv StateView,
	scanner futureOrderbookSnapshotVisitor,
	stateKeyFn OrderStateKeyFn,
	pair string,
	takerSide pb.OrderSide,
	takerQty *big.Int,
	batchSize int,
	counterPrefix string,
	takerPriceKey string,
	worstBuyKey string,
	worstSellKey string,
	isMarket bool,
	useReverse bool,
	takerOwner string,
	skipSelfTrade bool,
	matchHeight uint64,
	liquidationHeight uint64,
) ([]TradeResult, bool, error) {
	remain := new(big.Int).Set(takerQty)
	var trades []TradeResult
	touchedMakerExposure := make(map[string]struct{})
	startAfter := ""
	for remain.Sign() > 0 {
		                                                                        
		                                                                          
		                                                                         
		                                   
		entries := make([]futureOrderbookEntry, 0, batchSize)
		ok, err := scanner.VisitFutureOrderbookSnapshots(counterPrefix, startAfter, batchSize, useReverse, func(entry futureOrderbookEntry) bool {
			entry.snapshot.raw = append([]byte(nil), entry.snapshot.raw...)
			entries = append(entries, entry)
			return len(entries) < batchSize
		})
		if err != nil || !ok {
			return nil, ok, err
		}
		if len(entries) == 0 {
			break
		}

		matchedInBatch := false
		advancedInBatch := false
		stopAtPriceBoundary := false
		var lastScanned futureOrderbookEntry
		lastScannedSet := false
		pendingOps := make([]WriteOp, 0, batchSize*3)

		for _, entry := range entries {
			if remain.Sign() <= 0 {
				break
			}
			lastScanned = entry
			lastScannedSet = true
			priceKey := entry.priceKey
			if entry.orderID == "" || priceKey == "" {
				pendingOps = append(pendingOps, WriteOp{Key: entry.indexKey(counterPrefix), Del: true})
				advancedInBatch = true
				continue
			}
			if !isMarket {
				if takerSide == pb.OrderSide_BUY && takerPriceKey != "" && priceKey > takerPriceKey {
					stopAtPriceBoundary = true
					break
				}
				if takerSide == pb.OrderSide_SELL && takerPriceKey != "" && priceKey < takerPriceKey {
					stopAtPriceBoundary = true
					break
				}
			} else {
				if takerSide == pb.OrderSide_BUY && worstBuyKey != "" && priceKey > worstBuyKey {
					stopAtPriceBoundary = true
					break
				}
				if takerSide == pb.OrderSide_SELL && worstSellKey != "" && priceKey < worstSellKey {
					stopAtPriceBoundary = true
					break
				}
			}

			maker, err := entry.snapshot.makerSnapshot(pair, priceKey)
			if err != nil || maker == nil || maker.remain.Sign() <= 0 || maker.price == nil || maker.price.Sign() <= 0 {
				pendingOps = append(pendingOps, WriteOp{Key: entry.indexKey(counterPrefix), Del: true})
				advancedInBatch = true
				continue
			}
			if !futureMakerPredatesHeight(maker, liquidationHeight) {
				advancedInBatch = true
				continue
			}
			if skipSelfTrade && takerOwner != "" && maker.owner == takerOwner {
				advancedInBatch = true
				continue
			}
			rentEligible, rentErr := makerRentCanTrade(sv, maker.owner, matchHeight, liquidationHeight)
			if rentErr != nil {
				return nil, true, rentErr
			}
			if !rentEligible {
				advancedInBatch = true
				continue
			}

			tradeQty := new(big.Int).Set(remain)
			if tradeQty.Cmp(maker.remain) > 0 {
				tradeQty.Set(maker.remain)
			}
			makerNewRemain := new(big.Int).Sub(maker.remain, tradeQty) // safe-math-ok: 成交量先取 maker 与 taker 剩余量较小值，余量不下溢
			makerFilled := makerNewRemain.Sign() == 0
			exposureKey := futureMakerExposureKey(maker)
			_, forceStandalone := touchedMakerExposure[exposureKey]
			if !futureMakerTradeExecutable(sv, pair, takerOwner, maker, tradeQty, forceStandalone) {
				advancedInBatch = true
				continue
			}
			indexKey := entry.indexKey(counterPrefix)
			ops, err := prepareMakerOrderSnapshotStateUpdate(stateKeyFn, MarketFuture, indexKey, entry.orderID, maker, tradeQty, maker.price, makerFilled, matchHeight)
			if err != nil {
				continue
			}
			pendingOps = append(pendingOps, ops...)
			if makerFilled {
				pendingOps = append(pendingOps, WriteOp{Key: indexKey, Del: true})
				advancedInBatch = true
			}
			if exposureKey != "" {
				touchedMakerExposure[exposureKey] = struct{}{}
			}

			trades = append(trades, TradeResult{
				MakerOrderID:      entry.orderID,
				Quantity:          tradeQty,
				Price:             new(big.Int).Set(maker.price),
				MakerFilled:       makerFilled,
				MakerRemainBefore: new(big.Int).Set(maker.remain),
				MakerOwner:        maker.owner,
				MakerSide:         maker.side,
				MakerLeverage:     maker.leverage,
			})
			remain.Sub(remain, tradeQty)
			matchedInBatch = true
		}
		applyStateViewOps(sv, pendingOps)
		if stopAtPriceBoundary {
			break
		}
		if !matchedInBatch && !advancedInBatch {
			break
		}
		if len(entries) < batchSize {
			break
		}
		if !lastScannedSet {
			break
		}
		lastScannedKey := lastScanned.indexKey(counterPrefix)
		if lastScannedKey == "" || lastScannedKey == startAfter {
			break
		}
		startAfter = lastScannedKey
	}
	return trades, true, nil
}

                                                         
                                 
func extractOrderIDFromKey(indexKey string) string {
	parts := strings.Split(indexKey, "|order_id:")
	if len(parts) != 2 {
		return ""
	}
	return parts[1]
}

func priceKeyFromBigInt(price *big.Int) string {
	if price == nil || price.Sign() <= 0 {
		return ""
	}
	key, err := db.PriceToKey128(price.String())
	if err != nil {
		return ""
	}
	return key
}

func getStateMany(sv StateView, stateKeys []string) (map[string][]byte, error) {
	if len(stateKeys) == 0 {
		return nil, nil
	}
	if getter, ok := sv.(stateViewBatchGetter); ok {
		return getter.GetMany(stateKeys)
	}
	out := make(map[string][]byte, len(stateKeys))
	for _, stateKey := range stateKeys {
		data, exists, err := sv.Get(stateKey)
		if err != nil {
			return nil, err
		}
		if exists && len(data) > 0 {
			out[stateKey] = data
		}
	}
	return out, nil
}

func extractPriceKeyFromKey(indexKey string) string {
	priceStart := strings.Index(indexKey, "|price:")
	if priceStart < 0 {
		return ""
	}
	priceStart += len("|price:")
	priceEnd := strings.Index(indexKey[priceStart:], "|")
	if priceEnd < 0 {
		return ""
	}
	priceKey := indexKey[priceStart : priceStart+priceEnd]
	if len(priceKey) != 67 {
		return ""
	}
	return priceKey
}

                                                    
                                               
func extractPriceFromKey(indexKey string) *big.Int {
	priceKey67 := extractPriceKeyFromKey(indexKey)
	if priceKey67 == "" {
		return nil
	}
	return priceIntFromKey128(priceKey67)
}

func priceIntFromKey128(priceKey67 string) *big.Int {
	if len(priceKey67) != 67 {
		return nil
	}
	raw := strings.TrimLeft(priceKey67[:34], "0")
	if raw == "" {
		return new(big.Int)
	}
	price, ok := new(big.Int).SetString(raw, 10)
	if !ok {
		return nil
	}
	return price
}

func parsePositiveIntString(s string) (*big.Int, bool) {
	v, ok := new(big.Int).SetString(s, 10)
	return v, ok && v.Sign() > 0
}

func parseMakerOrderSnapshot(stateData []byte, kind MarketKind) (*makerOrderSnapshot, error) {
	switch kind {
	case MarketFuture:
		var fos pb.FutureOrderState
		if err := proto.Unmarshal(stateData, &fos); err != nil {
			return nil, err
		}
		if fos.OrderId == "" || fos.Status != pb.FutureOrderStatus_FUTURE_ORDER_OPEN {
			return nil, fmt.Errorf("future order not open")
		}
		amount, ok := parsePositiveIntString(fos.Amount)
		if !ok {
			return nil, fmt.Errorf("invalid amount")
		}
		filled := new(big.Int)
		if fos.FilledBase != "" {
			filled.SetString(fos.FilledBase, 10)
		}
		remain := new(big.Int).Sub(amount, filled) // safe-math-ok: 订单余量解析后立即拒绝非正值，不增加资产
		if remain.Sign() <= 0 {
			return nil, fmt.Errorf("fully filled")
		}
		price, ok := parsePositiveIntString(fos.Price)
		if !ok {
			return nil, fmt.Errorf("invalid price")
		}
		owner := fos.Owner
		if owner == "" {
			owner = fos.OwnerAddress
		}
		return &makerOrderSnapshot{
			remain: remain, price: price, owner: owner,
			side: fos.Side, leverage: fos.Leverage, future: &fos,
		}, nil
	case MarketSpot:
		var os pb.OrderState
		if err := proto.Unmarshal(stateData, &os); err != nil {
			return nil, err
		}
		if os.OrderId == "" || os.Status != pb.OrderStateStatus_ORDER_OPEN {
			return nil, fmt.Errorf("spot order not open")
		}
		amount, ok := parsePositiveIntString(os.Amount)
		if !ok {
			return nil, fmt.Errorf("invalid amount")
		}
		filled := new(big.Int)
		if os.FilledBase != "" {
			filled.SetString(os.FilledBase, 10)
		}
		remain := new(big.Int).Sub(amount, filled) // safe-math-ok: 订单余量解析后立即拒绝非正值，不增加资产
		if remain.Sign() <= 0 {
			return nil, fmt.Errorf("fully filled")
		}
		price, ok := parsePositiveIntString(os.Price)
		if !ok {
			return nil, fmt.Errorf("invalid price")
		}
		return &makerOrderSnapshot{
			remain: remain, price: price, owner: os.Owner,
			side: int32(os.Side), spot: &os,
		}, nil
	default:
		return nil, fmt.Errorf("unknown market kind: %d", kind)
	}
}

                                                                     
                                                                                                
                                                                                                                
func parseMakerRemainQty(stateData []byte, kind MarketKind) (*big.Int, error) {
	switch kind {
	case MarketFuture:
		var fos pb.FutureOrderState
		if err := proto.Unmarshal(stateData, &fos); err != nil {
			return nil, err
		}
		if fos.OrderId == "" || fos.Status != pb.FutureOrderStatus_FUTURE_ORDER_OPEN {
			return nil, fmt.Errorf("future order not open")
		}
		amount, ok := new(big.Int).SetString(fos.Amount, 10)
		if !ok || amount.Sign() <= 0 {
			return nil, fmt.Errorf("invalid amount")
		}
		filled := new(big.Int)
		if fos.FilledBase != "" {
			filled.SetString(fos.FilledBase, 10)
		}
		remain := new(big.Int).Sub(amount, filled) // safe-math-ok: 订单余量解析后立即拒绝非正值，不增加资产
		if remain.Sign() <= 0 {
			return nil, fmt.Errorf("fully filled")
		}
		return remain, nil
	case MarketSpot:
		var os pb.OrderState
		if err := proto.Unmarshal(stateData, &os); err != nil {
			return nil, err
		}
		if os.OrderId == "" || os.Status != pb.OrderStateStatus_ORDER_OPEN {
			return nil, fmt.Errorf("spot order not open")
		}
		amount, ok := new(big.Int).SetString(os.Amount, 10)
		if !ok || amount.Sign() <= 0 {
			return nil, fmt.Errorf("invalid amount")
		}
		filled := new(big.Int)
		if os.FilledBase != "" {
			filled.SetString(os.FilledBase, 10)
		}
		remain := new(big.Int).Sub(amount, filled) // safe-math-ok: 订单余量解析后立即拒绝非正值，不增加资产
		if remain.Sign() <= 0 {
			return nil, fmt.Errorf("fully filled")
		}
		return remain, nil
	default:
		return nil, fmt.Errorf("unknown market kind: %d", kind)
	}
}

                                                                                                                               
func parseMakerOwner(stateData []byte, kind MarketKind) string {
	switch kind {
	case MarketFuture:
		var fos pb.FutureOrderState
		if err := proto.Unmarshal(stateData, &fos); err != nil {
			return ""
		}
		if fos.Owner != "" {
			return fos.Owner
		}
		return fos.OwnerAddress
	case MarketSpot:
		var os pb.OrderState
		if err := proto.Unmarshal(stateData, &os); err != nil {
			return ""
		}
		return os.Owner
	}
	return ""
}

func parseMakerTradeMeta(stateData []byte, kind MarketKind) (owner string, side int32, leverage uint64) {
	switch kind {
	case MarketFuture:
		var fos pb.FutureOrderState
		if err := proto.Unmarshal(stateData, &fos); err != nil {
			return "", 0, 0
		}
		owner = fos.Owner
		if owner == "" {
			owner = fos.OwnerAddress
		}
		return owner, fos.Side, fos.Leverage
	case MarketSpot:
		return parseMakerOwner(stateData, kind), 0, 0
	default:
		return "", 0, 0
	}
}

func updateMakerOrderSnapshotState(sv StateView, stateKeyFn OrderStateKeyFn, kind MarketKind, indexKey, orderID string, maker *makerOrderSnapshot, tradeQty, tradePrice *big.Int, filled bool, matchHeight uint64) error {
	ops, err := prepareMakerOrderSnapshotStateUpdate(stateKeyFn, kind, indexKey, orderID, maker, tradeQty, tradePrice, filled, matchHeight)
	if err != nil {
		return err
	}
	applyStateViewOps(sv, ops)
	return nil
}

func applyStateViewOps(sv StateView, ops []WriteOp) {
	for _, op := range ops {
		if op.Del {
			sv.Del(op.Key)
		} else {
			sv.Set(op.Key, op.Value)
		}
	}
}

func appendFutureOrderExpiryRefreshOps(ops []WriteOp, st *pb.FutureOrderState, filled bool, matchHeight uint64) []WriteOp {
	if st == nil || st.IsClose {
		return ops
	}
	oldExpiryHeight := futureOrderCurrentExpiryHeight(st)
	newExpiryHeight := oldExpiryHeight
	if filled || !futureOrderIsOpen(st) {
		newExpiryHeight = 0
	} else if matchHeight > 0 {
		newExpiryHeight = futureOrderExpiryDueHeight(matchHeight)
	}
	st.ExpiryHeight = newExpiryHeight
	if oldExpiryHeight == newExpiryHeight {
		return ops
	}
	if expiryKey, ok := futureOrderExpiryKeyByDueHeight(st, oldExpiryHeight); ok {
		ops = append(ops, WriteOp{Key: expiryKey, Del: true})
	}
	if newExpiryHeight != 0 {
		if expiryKey, ok := futureOrderExpiryKeyByDueHeight(st, newExpiryHeight); ok {
			ops = append(ops, WriteOp{Key: expiryKey, Value: futureOrderExpiryValue(st)})
		}
	}
	return ops
}

func syncFutureOrderExpiryAfterFill(sv StateView, st *pb.FutureOrderState, filled bool, matchHeight uint64) {
	ops := appendFutureOrderExpiryRefreshOps(nil, st, filled, matchHeight)
	applyStateViewOps(sv, ops)
}

func prepareMakerOrderSnapshotStateUpdate(stateKeyFn OrderStateKeyFn, kind MarketKind, indexKey, orderID string, maker *makerOrderSnapshot, tradeQty, tradePrice *big.Int, filled bool, matchHeight uint64) ([]WriteOp, error) {
	if kind != MarketFuture || maker == nil || maker.future == nil {
		return nil, fmt.Errorf("unsupported maker snapshot update")
	}
	stateKey := stateKeyFn(orderID)
	fos := maker.future
	oldHistoryKey := keys.KeyFutureAccountOrderFromState(fos)
	ops := make([]WriteOp, 0, 5)
	oldFilled := new(big.Int)
	if fos.FilledBase != "" {
		oldFilled.SetString(fos.FilledBase, 10)
	}
	newFilled, err := SafeAdd(oldFilled, tradeQty)
	if err != nil {
		return nil, fmt.Errorf("future FilledBase overflow order=%s: %w", orderID, err)
	}
	fos.FilledBase = newFilled.String()
	oldQuote := new(big.Int)
	if fos.FilledQuote != "" {
		oldQuote.SetString(fos.FilledQuote, 10)
	}
	tradeQuote, qerr := QuoteFromBasePrice(tradeQty, tradePrice)
	if qerr != nil {
		return nil, qerr
	}
	newQuote, err := SafeAdd(oldQuote, tradeQuote)
	if err != nil {
		return nil, fmt.Errorf("future FilledQuote overflow order=%s: %w", orderID, err)
	}
	fos.FilledQuote = newQuote.String()
	if filled {
		fos.Status = pb.FutureOrderStatus_FUTURE_ORDER_FILLED
	}
	ops = appendFutureOrderExpiryRefreshOps(ops, fos, filled, matchHeight)
	data, err := proto.Marshal(fos)
	if err != nil {
		return nil, err
	}
	if filled {
		ops = append(ops, WriteOp{Key: stateKey, Del: true})
		if indexKey != "" {
			ops = append(ops, WriteOp{Key: indexKey, Del: true})
		}
	} else {
		ops = append(ops, WriteOp{Key: stateKey, Value: data})
		ops = append(ops, WriteOp{Key: keys.KeyRentOwnerMarker(futureOrderOwner(fos), rent.OwnerMarkerFutureOrder), Value: []byte{1}, Category: "rent_owner_marker"})
		if indexKey != "" {
			ops = append(ops, WriteOp{Key: indexKey, Value: data})
		}
	}
	newHistoryKey := keys.KeyFutureAccountOrderFromState(fos)
	if oldHistoryKey != "" && oldHistoryKey != newHistoryKey {
		ops = append(ops, WriteOp{Key: oldHistoryKey, Del: true})
	}
	if newHistoryKey != "" {
		ops = append(ops, WriteOp{Key: newHistoryKey, Value: data})
	}
	return ops, nil
}

                                                                                    
                                                                                                  
func updateMakerOrderState(sv StateView, stateKeyFn OrderStateKeyFn, kind MarketKind, orderID string, stateData []byte, tradeQty, tradePrice *big.Int, filled bool, matchHeight uint64) error {
	stateKey := stateKeyFn(orderID)

	switch kind {
	case MarketFuture:
		var fos pb.FutureOrderState
		if err := proto.Unmarshal(stateData, &fos); err != nil || fos.OrderId == "" {
			return fmt.Errorf("cannot unmarshal future order state for %s", orderID)
		}
		oldHistoryKey := keys.KeyFutureAccountOrderFromState(&fos)
		oldFilled := new(big.Int)
		if fos.FilledBase != "" {
			oldFilled.SetString(fos.FilledBase, 10)
		}
		newFilled, err := SafeAdd(oldFilled, tradeQty)
		if err != nil {
			return fmt.Errorf("future FilledBase overflow order=%s: %w", orderID, err)
		}
		fos.FilledBase = newFilled.String()
		                                                                                              
		oldQuote := new(big.Int)
		if fos.FilledQuote != "" {
			oldQuote.SetString(fos.FilledQuote, 10)
		}
		tradeQuote, qerr := QuoteFromBasePrice(tradeQty, tradePrice)
		if qerr != nil {
			return qerr
		}
		newQuote, err := SafeAdd(oldQuote, tradeQuote)
		if err != nil {
			return fmt.Errorf("future FilledQuote overflow order=%s: %w", orderID, err)
		}
		fos.FilledQuote = newQuote.String()
		if filled {
			fos.Status = pb.FutureOrderStatus_FUTURE_ORDER_FILLED
		}
		syncFutureOrderExpiryAfterFill(sv, &fos, filled, matchHeight)
		data, err := proto.Marshal(&fos)
		if err != nil {
			return err
		}
		if filled {
			sv.Del(stateKey)
		} else {
			sv.Set(stateKey, data)
			rent.MarkOwnerActivity(sv, futureOrderOwner(&fos), rent.OwnerMarkerFutureOrder)
		}
		                                                                                                   
		                                                            
		newHistoryKey := keys.KeyFutureAccountOrderFromState(&fos)
		if oldHistoryKey != "" && oldHistoryKey != newHistoryKey {
			sv.Del(oldHistoryKey)
		}
		if newHistoryKey != "" {
			sv.Set(newHistoryKey, data)
		}
		return nil
	case MarketSpot:
		                                                                                                                   
		var os pb.OrderState
		if err := proto.Unmarshal(stateData, &os); err != nil || os.OrderId == "" {
			return fmt.Errorf("cannot unmarshal spot order state for %s", orderID)
		}
		oldFilled := new(big.Int)
		if os.FilledBase != "" {
			oldFilled.SetString(os.FilledBase, 10)
		}
		newFilled, err := SafeAdd(oldFilled, tradeQty)
		if err != nil {
			return fmt.Errorf("spot FilledBase overflow order=%s: %w", orderID, err)
		}
		os.FilledBase = newFilled.String()

		                                                              
		                                                                                          
		                                                    

		if filled {
			os.IsFilled = true
			os.Status = pb.OrderStateStatus_ORDER_FILLED
		}
		data, err := proto.Marshal(&os)
		if err != nil {
			return err
		}
		sv.Set(stateKey, data)
		return nil
	default:
		return fmt.Errorf("unknown market kind: %d", kind)
	}
}

                                                                                                                 
                                                                                                                            
                                                                                                 
                                                                                        
func MatchSpotOrder(sv StateView, pair string, takerSide pb.OrderSide, takerQty, takerPrice *big.Int, isMarket bool, maxSlippageBps uint32, takerOwner string) ([]TradeResult, error) {
	return MatchSpotOrderAtHeight(sv, pair, takerSide, takerQty, takerPrice, isMarket, maxSlippageBps, takerOwner, 0)
}

func MatchSpotOrderAtHeight(sv StateView, pair string, takerSide pb.OrderSide, takerQty, takerPrice *big.Int, isMarket bool, maxSlippageBps uint32, takerOwner string, matchHeight uint64) ([]TradeResult, error) {
	return matchOrderFromSV(sv, keys.KeyOrderPriceIndexPrefix, keys.KeyOrderState, pair, takerSide, takerQty, takerPrice, isMarket, 200, MarketSpot, maxSlippageBps, takerOwner, false, matchHeight, 0)
}

                                                                         
func MatchFutureOrder(sv StateView, pair string, takerSide pb.OrderSide, takerQty, takerPrice *big.Int, isMarket bool, maxSlippageBps uint32, takerOwner string) ([]TradeResult, error) {
	return MatchFutureOrderAtHeight(sv, pair, takerSide, takerQty, takerPrice, isMarket, maxSlippageBps, takerOwner, 0)
}

func MatchFutureOrderAtHeight(sv StateView, pair string, takerSide pb.OrderSide, takerQty, takerPrice *big.Int, isMarket bool, maxSlippageBps uint32, takerOwner string, matchHeight uint64) ([]TradeResult, error) {
	return matchOrderFromSV(sv, keys.KeyFutureOrderPriceIndexPrefix, keys.KeyFutureOrderState, pair, takerSide, takerQty, takerPrice, isMarket, 200, MarketFuture, maxSlippageBps, takerOwner, false, matchHeight, 0)
}

                                                                              
                                                                            
                                                                           
                         
func MatchFutureReduceOnlyOrderAtHeight(sv StateView, pair string, takerSide pb.OrderSide, takerQty, takerPrice *big.Int, isMarket bool, maxSlippageBps uint32, takerOwner string, matchHeight uint64) ([]TradeResult, error) {
	return matchOrderFromSV(sv, keys.KeyFutureOrderPriceIndexPrefix, keys.KeyFutureOrderState, pair, takerSide, takerQty, takerPrice, isMarket, 200, MarketFuture, maxSlippageBps, takerOwner, true, matchHeight, 0)
}

                                                       
                                                                                                  
                                                                                                                 
                                                                                                               
func MatchFutureOrderForLiquidation(sv StateView, pair string, takerSide pb.OrderSide, takerQty, takerPrice *big.Int, isMarket bool, maxSlippageBps uint32, liquidatedOwner string) ([]TradeResult, error) {
	return MatchFutureOrderForLiquidationAtHeight(sv, pair, takerSide, takerQty, takerPrice, isMarket, maxSlippageBps, liquidatedOwner, 0)
}

                                                                             
                                                                            
                                                                               
func MatchFutureOrderForLiquidationAtHeight(sv StateView, pair string, takerSide pb.OrderSide, takerQty, takerPrice *big.Int, isMarket bool, maxSlippageBps uint32, liquidatedOwner string, matchHeight uint64) ([]TradeResult, error) {
	return matchOrderFromSV(sv, keys.KeyFutureOrderPriceIndexPrefix, keys.KeyFutureOrderState, pair, takerSide, takerQty, takerPrice, isMarket, 200, MarketFuture, maxSlippageBps, liquidatedOwner, liquidatedOwner != "", matchHeight, matchHeight)
}
