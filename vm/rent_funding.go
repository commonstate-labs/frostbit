package vm

import (
	"crypto/sha256"
	"dex/keys"
	"dex/pb"
	"dex/rent"
	"dex/utils"
	"fmt"
	"math/big"
	"sort"
	"strings"
)

const (
	rentFundingMakerLimit  = 32
	rentFundingSlippageBps = 500
	rentFundingCancelLimit = 64
	rentFundingFutureScan  = 256
	rentFundingFuturePhase = "funding_cancel_future"
)

type rentFundingFutureOrderIndex struct {
	built   bool
	byOwner map[string][]string
}

func (i *rentFundingFutureOrderIndex) ensure(sv StateView) error {
	if i.built {
		return nil
	}
	byOwner := make(map[string][]string)
	prefix := keys.KeyFutureOrderStatePrefix()
	cursor := ""
	for {
		var (
			rows []KVPair
			err  error
		)
		if scanner, ok := sv.(orderedStateScannerAfter); ok {
			rows, err = scanner.ScanWithLimitOrderedAfter(prefix, cursor, rentFundingFutureScan, false)
		} else {
			rows, err = scanStateViewOrderedAfter(sv, prefix, cursor, rentFundingFutureScan, false)
		}
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		advanced := false
		for _, item := range rows {
			if item.Key <= cursor {
				return fmt.Errorf("rent funding future index: non-monotonic key %q after %q", item.Key, cursor)
			}
			cursor = item.Key
			advanced = true
			var order pb.FutureOrderState
			if err := unmarshalProtoCompat(item.Value, &order); err != nil {
				return fmt.Errorf("rent funding future index %s: %w", item.Key, err)
			}
			owner := strings.TrimSpace(order.OwnerAddress)
			if owner == "" {
				owner = strings.TrimSpace(order.Owner)
			}
			if owner == "" || order.Status != pb.FutureOrderStatus_FUTURE_ORDER_OPEN || order.IsClose {
				continue
			}
			byOwner[owner] = append(byOwner[owner], item.Key)
		}
		if !advanced {
			return fmt.Errorf("rent funding future index made no progress after %q", cursor)
		}
	}
	i.byOwner = byOwner
	i.built = true
	return nil
}

func rentFundingOwnerScopes(sv StateView, owner string) (scanAssets, scanSpot, scanFuture bool, err error) {
	initKey := keys.KeyRentOwnerMarker(owner, rent.OwnerMarkerInit)
	assetKey := keys.KeyRentOwnerMarker(owner, rent.OwnerMarkerNonFBBalance)
	spotKey := keys.KeyRentOwnerMarker(owner, rent.OwnerMarkerSpotOrder)
	futureKey := keys.KeyRentOwnerMarker(owner, rent.OwnerMarkerFutureOrder)
	markers, err := getStateMany(sv, []string{initKey, assetKey, spotKey, futureKey})
	if err != nil {
		return false, false, false, err
	}
	if _, complete := markers[initKey]; !complete {
		                                                                      
		                         
		return true, true, true, nil
	}
	_, scanAssets = markers[assetKey]
	_, scanSpot = markers[spotKey]
	_, scanFuture = markers[futureKey]
	return scanAssets, scanSpot, scanFuture, nil
}

                                                                              
                                                                               
                                                                             
func newRentFundingAttempt(sv StateView, seedDiff []WriteOp) func(string, uint64, *big.Int, *rent.RentWork) (rent.FundingResult, error) {
	assets := rent.DefaultPolicy().ExemptAssets
	sort.Slice(assets, func(i, j int) bool { return assets[i].Token < assets[j].Token })
	                                                                         
	previousPrices := make(map[string][]byte)
	for _, op := range seedDiff {
		if strings.HasPrefix(op.Key, keys.KeySpotLastPricePrefix()) {
			previousPrices[op.Key] = nil
			if op.HasOldValue && op.OldExists {
				previousPrices[op.Key] = op.OldValue
			}
		}
	}
	                                                                          
	                                                                             
	                                                                               
	conversionOrdinals := make(map[string]uint64)
	futureOrders := &rentFundingFutureOrderIndex{}
	return func(owner string, height uint64, requiredFB *big.Int, _ *rent.RentWork) (rent.FundingResult, error) {
		result := rent.FundingResult{}
		var remaining *big.Int
		if requiredFB != nil {
			remaining = new(big.Int).Set(requiredFB)
			if remaining.Sign() <= 0 {
				return result, nil
			}
		}
		scanAssets, scanSpot, scanFuture, scopeErr := rentFundingOwnerScopes(sv, owner)
		if scopeErr != nil {
			return result, scopeErr
		}
		if scanAssets {
			converted, err := completeRentFundingConversionPages(sv, owner, height, assets, previousPrices, remaining, conversionOrdinals, &result)
			if err != nil || converted {
				return result, err
			}
		}

		removedKeys, cancelErr := cancelRentFundingOrders(sv, owner, height, scanSpot, scanFuture, futureOrders)
		if cancelErr != nil {
			return result, cancelErr
		}
		result.Progress = result.Progress || len(removedKeys) > 0

		if scanAssets {
			_, err := completeRentFundingConversionPages(sv, owner, height, assets, previousPrices, remaining, conversionOrdinals, &result)
			return result, err
		}
		return result, nil
	}
}

                                                                             
                                                                                 
                                                                             
func completeRentFundingConversionPages(sv StateView, owner string, height uint64, assets []rent.ExemptAsset, previousPrices map[string][]byte, remaining *big.Int, conversionOrdinals map[string]uint64, result *rent.FundingResult) (bool, error) {
	for {
		page := rent.FundingResult{}
		if err := convertAvailableRentFunding(sv, owner, height, assets, previousPrices, remaining, conversionOrdinals, &page); err != nil {
			return false, err
		}
		result.TouchedOwners = append(result.TouchedOwners, page.TouchedOwners...)
		result.Progress = result.Progress || page.Progress
		if page.ReceivedFB != nil && page.ReceivedFB.Sign() > 0 {
			if result.ReceivedFB == nil {
				result.ReceivedFB = new(big.Int)
			}
			result.ReceivedFB.Add(result.ReceivedFB, page.ReceivedFB)
			return true, nil
		}
		if !page.Deferred {
			return false, nil
		}
		if len(page.RemovedWorkKeys) == 0 {
			return false, fmt.Errorf("rent funding %s: deferred conversion page removed no blockers", owner)
		}
	}
}

                                                                            
                                                                                
                                                                                
                                                                  
func convertAvailableRentFunding(sv StateView, owner string, height uint64, assets []rent.ExemptAsset, previousPrices map[string][]byte, remaining *big.Int, conversionOrdinals map[string]uint64, result *rent.FundingResult) error {
	for _, asset := range assets {
		                                                                 
		                                                                   
		for _, sell := range []bool{true, false} {
			balance, err := getBalanceStrict(sv, owner, asset.Token)
			if err != nil {
				return err
			}
			available, err := parseBalanceStrict("rent funding balance", balance.Balance)
			if err != nil {
				return err
			}
			if available.Sign() <= 0 {
				break
			}
			base, quote, side := asset.Token, rent.FeeToken, pb.OrderSide_SELL
			if !sell {
				base, quote, side = rent.FeeToken, asset.Token, pb.OrderSide_BUY
			}
			pair := utils.GeneratePairKey(base, quote)
			if _, exists, err := sv.Get(keys.KeySpotMarketConfig(pair)); err != nil {
				return err
			} else if !exists {
				continue
			}
			priceData, changed := previousPrices[keys.KeySpotLastPrice(pair)]
			if !changed {
				var err error
				priceData, _, err = sv.Get(keys.KeySpotLastPrice(pair))
				if err != nil {
					return err
				}
			}
			price := new(big.Int).SetBytes(priceData)
			if price.Sign() <= 0 {
				continue
			}
			bestPrice, touchedMakers, scanExhausted, removedKeys, prepErr := prepareRentFundingMakerPage(sv, pair, side, price, owner, height)
			if prepErr != nil {
				return prepErr
			}
			result.TouchedOwners = append(result.TouchedOwners, touchedMakers...)
			result.RemovedWorkKeys = append(result.RemovedWorkKeys, removedKeys...)
			result.Progress = result.Progress || len(removedKeys) > 0
			if bestPrice == nil {
				if scanExhausted {
					if len(removedKeys) == 0 {
						return fmt.Errorf("rent funding %s %s: full maker page made no progress", owner, pair)
					}
					deferRentFundingResult(result, height, rent.RentWorkFunding, "")
					return nil
				}
				continue
			}
			amount := new(big.Int).Set(available)
			if sell && remaining != nil {
				                                                               
				                                                            
				                                                            
				amount = MulDivCeil(remaining, PriceScale, bestPrice)
				if amount.Cmp(available) > 0 {
					amount.Set(available)
				}
			} else if !sell {
				worst := new(big.Int).Mul(price, big.NewInt(10_000+rentFundingSlippageBps)) // safe-math-ok: 租金换汇滑点价格分子，先除以 10000 再限制交易金额
				worst.Quo(worst, big.NewInt(10_000))
				amount.Mul(amount, PriceScale).Quo(amount, worst)
				if remaining != nil && amount.Cmp(remaining) > 0 {
					amount.Set(remaining)
				}
			}
			if amount.Sign() <= 0 || amount.Cmp(MaxUint256) > 0 {
				continue
			}
			ordinalKey := fmt.Sprintf("%d\x00%s\x00%s", height, owner, pair)
			ordinal := conversionOrdinals[ordinalKey]
			conversionOrdinals[ordinalKey] = ordinal + 1
			id := sha256.Sum256([]byte(fmt.Sprintf("rent/%d/%s/%s/%d", height, owner, pair, ordinal)))
			ord := &pb.OrderTx{
				Base: &pb.BaseMessage{TxId: fmt.Sprintf("%x", id), FromAddress: owner, ExecutedHeight: height},
				Op:   pb.OrderOp_ADD, BaseToken: base, QuoteToken: quote, Side: side,
				Amount: amount.String(), IsMarket: true, MaxSlippageBps: rentFundingSlippageBps,
			}
			                                                                 
			                                                                      
			                                                                     
			                                                                      
			                                                        
			matcherOwners, ownerErr := rentFundingMatcherCandidateOwners(sv, pair, side)
			if ownerErr != nil {
				return ownerErr
			}
			result.TouchedOwners = append(result.TouchedOwners, matcherOwners...)
			snap := sv.Snapshot()
			_, receipt, err := (&OrderTxHandler{}).handleAddOrderWithLimits(ord, sv, price, rentFundingMakerLimit)
			if err != nil || receipt == nil || receipt.Status != "SUCCEED" || len(receipt.Events) == 0 {
				if revertErr := sv.Revert(snap); revertErr != nil {
					return revertErr
				}
				if err != nil {
					return err
				}
				if receipt == nil {
					return fmt.Errorf("rent funding %s %s: matcher returned no receipt", owner, pair)
				}
				switch receipt.Status {
				case "FAILED":
					                                                          
					                                                             
					                                                       
					continue
				case "SUCCEED":
					                                                            
					                                                        
					                                                      
					continue
				default:
					return fmt.Errorf("rent funding %s %s: unexpected matcher status %q", owner, pair, receipt.Status)
				}
			}
			received := new(big.Int)
			for _, event := range receipt.Events {
				quantity, err := parseBalanceStrict("rent filled quantity", event.Payload["quantity"])
				if err != nil {
					return err
				}
				if sell {
					tradePrice, err := parseBalanceStrict("rent filled price", event.Payload["price"])
					if err != nil {
						return err
					}
					quantity, err = QuoteFromBasePrice(quantity, tradePrice)
					if err != nil {
						return err
					}
				}
				received.Add(received, quantity)
				result.TouchedOwners = append(result.TouchedOwners, event.Payload["maker"])
			}
			if result.ReceivedFB == nil {
				result.ReceivedFB = new(big.Int)
			}
			result.ReceivedFB.Add(result.ReceivedFB, received)
			if received.Sign() > 0 {
				result.Progress = true
			}
			recordUserActivity(sv, owner, &UserActivityRecord{Kind: ActKindRentConversion, Height: height, TxID: ord.Base.TxId, Amount: received.String(), Token: rent.FeeToken, Ref: asset.Token, Note: "Automatic conversion for account rent"})
			if remaining == nil {
				return nil
			}
			remaining.Sub(remaining, received)
			if remaining.Sign() <= 0 {
				return nil
			}
			more, moreTouched, moreRemovedKeys, moreErr := rentFundingHasRemainingLiquidity(sv, pair, side, price, owner, height)
			result.TouchedOwners = append(result.TouchedOwners, moreTouched...)
			result.RemovedWorkKeys = append(result.RemovedWorkKeys, moreRemovedKeys...)
			result.Progress = result.Progress || len(moreRemovedKeys) > 0
			if moreErr != nil {
				return moreErr
			}
			if more {
				deferRentFundingResult(result, height, rent.RentWorkFunding, "")
				return nil
			}
		}
	}
	return nil
}

func rentFundingMatcherCandidateOwners(sv StateView, pair string, takerSide pb.OrderSide) ([]string, error) {
	counterSide := pb.OrderSide_BUY
	reverse := true
	if takerSide == pb.OrderSide_BUY {
		counterSide = pb.OrderSide_SELL
		reverse = false
	}
	indexKeys, err := scanStateViewOrderedKeysAfter(
		sv,
		keys.KeyOrderPriceIndexPrefix(pair, counterSide, false),
		"",
		rentFundingMakerLimit,
		reverse,
	)
	if err != nil {
		return nil, err
	}
	if len(indexKeys) > rentFundingMakerLimit {
		indexKeys = indexKeys[:rentFundingMakerLimit]
	}
	stateKeys := make([]string, 0, len(indexKeys))
	for _, indexKey := range indexKeys {
		if orderID := extractOrderIDFromKey(indexKey); orderID != "" {
			stateKeys = append(stateKeys, keys.KeyOrderState(orderID))
		}
	}
	states, err := getStateMany(sv, stateKeys)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(states))
	owners := make([]string, 0, len(states))
	for _, stateKey := range stateKeys {
		data := states[stateKey]
		if len(data) == 0 {
			continue
		}
		maker, parseErr := parseMakerOrderSnapshot(data, MarketSpot)
		if parseErr != nil {
			return nil, fmt.Errorf("rent maker %s: %w", strings.TrimPrefix(stateKey, keys.KeyOrderState("")), parseErr)
		}
		if maker == nil || strings.TrimSpace(maker.owner) == "" {
			continue
		}
		if _, exists := seen[maker.owner]; exists {
			continue
		}
		seen[maker.owner] = struct{}{}
		owners = append(owners, maker.owner)
	}
	return owners, nil
}

                                                                            
                                                                               
                                                                              
                                                                                
                                                                              
func prepareRentFundingMakerPage(sv StateView, pair string, takerSide pb.OrderSide, referencePrice *big.Int, owner string, height uint64) (*big.Int, []string, bool, []string, error) {
	counterSide := pb.OrderSide_BUY
	reverse := true
	if takerSide == pb.OrderSide_BUY {
		counterSide = pb.OrderSide_SELL
		reverse = false
	}
	prefix := keys.KeyOrderPriceIndexPrefix(pair, counterSide, false)
	items, err := scanStateViewOrderedAfter(sv, prefix, "", rentFundingMakerLimit+1, reverse)
	if err != nil {
		return nil, nil, false, nil, err
	}
	scanExhausted := len(items) > rentFundingMakerLimit
	if scanExhausted {
		items = items[:rentFundingMakerLimit]
	}
	worstBuy := new(big.Int).Mul(referencePrice, big.NewInt(10_000+rentFundingSlippageBps)) // safe-math-ok: 只读 maker 买入价格边界，随后除以 10000
	worstBuy.Quo(worstBuy, big.NewInt(10_000))
	worstSell := new(big.Int).Mul(referencePrice, big.NewInt(10_000-rentFundingSlippageBps)) // safe-math-ok: 只读 maker 卖出价格边界，随后除以 10000
	worstSell.Quo(worstSell, big.NewInt(10_000))
	seenOwners := make(map[string]struct{})
	touched := make([]string, 0)
	removedKeys := make([]string, 0)
	var bestPrice *big.Int
	for _, item := range items {
		indexPrice := extractPriceFromKey(item.Key)
		if indexPrice == nil || indexPrice.Sign() <= 0 {
			sv.Del(item.Key)
			removedKeys = append(removedKeys, item.Key)
			continue
		}
		if takerSide == pb.OrderSide_BUY && indexPrice.Cmp(worstBuy) > 0 {
			scanExhausted = false
			break
		}
		if takerSide == pb.OrderSide_SELL && indexPrice.Cmp(worstSell) < 0 {
			scanExhausted = false
			break
		}
		orderID := extractOrderIDFromKey(item.Key)
		if orderID == "" {
			sv.Del(item.Key)
			removedKeys = append(removedKeys, item.Key)
			continue
		}
		data, exists, getErr := sv.Get(keys.KeyOrderState(orderID))
		if getErr != nil {
			return nil, nil, false, removedKeys, getErr
		}
		if !exists || len(data) == 0 {
			sv.Del(item.Key)
			removedKeys = append(removedKeys, item.Key)
			continue
		}
		var order pb.OrderState
		if err := unmarshalProtoCompat(data, &order); err != nil {
			return nil, nil, false, removedKeys, fmt.Errorf("rent maker %s: %w", orderID, err)
		}
		expectedIndex, indexOK := spotOrderPriceIndexKey(&order)
		if order.OrderId != orderID || order.Status != pb.OrderStateStatus_ORDER_OPEN || order.IsFilled || !indexOK || expectedIndex != item.Key {
			                                                                   
			                                                               
			sv.Del(item.Key)
			removedKeys = append(removedKeys, item.Key)
			continue
		}
		maker, parseErr := parseMakerOrderSnapshot(data, MarketSpot)
		if parseErr != nil {
			amount, amountOK := new(big.Int).SetString(order.Amount, 10)
			filled := new(big.Int)
			filledOK := order.FilledBase == ""
			if !filledOK {
				filled, filledOK = new(big.Int).SetString(order.FilledBase, 10)
			}
			if amountOK && filledOK && amount.Sign() > 0 && filled.Cmp(amount) >= 0 {
				sv.Del(item.Key)
				removedKeys = append(removedKeys, item.Key)
				continue
			}
			return nil, nil, false, removedKeys, fmt.Errorf("rent maker %s: %w", orderID, parseErr)
		}
		if maker == nil || maker.owner == "" || maker.remain == nil || maker.remain.Sign() <= 0 || maker.price == nil || maker.price.Sign() <= 0 {
			sv.Del(item.Key)
			removedKeys = append(removedKeys, item.Key)
			continue
		}
		if _, seen := seenOwners[maker.owner]; !seen {
			seenOwners[maker.owner] = struct{}{}
			touched = append(touched, maker.owner)
		}
		notional, notionalErr := QuoteFromBasePrice(maker.remain, maker.price)
		if notionalErr != nil {
			return nil, nil, false, removedKeys, notionalErr
		}
		if notional.Sign() == 0 {
			                                                                    
			                                                                       
			if err := cancelSpotOpenOrderState(sv, &order, height, "rent_funding_dust_unfreeze"); err != nil {
				return nil, nil, false, removedKeys, err
			}
			removedKeys = append(removedKeys, item.Key)
			continue
		}
		if maker.owner == owner {
			if err := cancelSpotOpenOrderState(sv, &order, height, "rent_funding_self_unfreeze"); err != nil {
				return nil, nil, false, removedKeys, err
			}
			removedKeys = append(removedKeys, item.Key)
			continue
		}
		eligible, rentErr := makerRentCanTrade(sv, maker.owner, height, 0)
		if rentErr != nil {
			return nil, nil, false, removedKeys, rentErr
		}
		if !eligible {
			                                                                    
			                                                               
			if err := cancelSpotOpenOrderState(sv, &order, height, "rent_funding_maker_unfreeze"); err != nil {
				return nil, nil, false, removedKeys, err
			}
			removedKeys = append(removedKeys, item.Key)
			continue
		}
		if bestPrice == nil {
			bestPrice = new(big.Int).Set(maker.price)
		}
	}
	return bestPrice, touched, scanExhausted, removedKeys, nil
}

func deferRentFundingResult(result *rent.FundingResult, height uint64, phase, cursor string) {
	if result == nil {
		return
	}
	result.Deferred = true
	result.NextActionHeight = height
	result.Phase = phase
	result.Cursor = cursor
}

func cancelRentFundingOrders(sv StateView, owner string, height uint64, scanSpot, scanFuture bool, futureOrders *rentFundingFutureOrderIndex) ([]string, error) {
	removedKeys := make([]string, 0)
	if scanSpot {
		prefix := keys.KeyAccountOrdersPrefix(owner)
		cursor := ""
		for {
			var (
				items []KVPair
				err   error
			)
			if scanner, ok := sv.(orderedStateScannerAfter); ok {
				items, err = scanner.ScanWithLimitOrderedAfter(prefix, cursor, rentFundingCancelLimit, false)
			} else {
				items, err = scanStateViewOrderedAfter(sv, prefix, cursor, rentFundingCancelLimit, false)
			}
			if err != nil {
				return removedKeys, err
			}
			if len(items) == 0 {
				break
			}
			for _, item := range items {
				if item.Key <= cursor {
					return removedKeys, fmt.Errorf("rent funding spot cancellation: non-monotonic key %q after %q", item.Key, cursor)
				}
				cursor = item.Key
				id := strings.TrimPrefix(item.Key, prefix)
				data, exists, err := sv.Get(keys.KeyOrderState(id))
				if err != nil {
					return removedKeys, err
				}
				if !exists {
					sv.Del(item.Key)
					removedKeys = append(removedKeys, item.Key)
					continue
				}
				var order pb.OrderState
				if err := unmarshalProtoCompat(data, &order); err != nil {
					return removedKeys, err
				}
				if order.Owner == owner && order.Status == pb.OrderStateStatus_ORDER_OPEN && !order.IsFilled {
					if err := cancelSpotOpenOrderState(sv, &order, height, "rent_funding_unfreeze"); err != nil {
						return removedKeys, err
					}
					removedKeys = append(removedKeys, item.Key)
				} else {
					sv.Del(item.Key)
					removedKeys = append(removedKeys, item.Key)
				}
			}
		}
	}
	if !scanFuture {
		return removedKeys, nil
	}
	if futureOrders == nil {
		return removedKeys, fmt.Errorf("rent funding future index is nil")
	}
	if err := futureOrders.ensure(sv); err != nil {
		return removedKeys, err
	}
	for _, stateKey := range futureOrders.byOwner[owner] {
		data, exists, err := sv.Get(stateKey)
		if err != nil {
			return removedKeys, err
		}
		if !exists || len(data) == 0 {
			continue
		}
		var order pb.FutureOrderState
		if err := unmarshalProtoCompat(data, &order); err != nil {
			return removedKeys, err
		}
		address := order.OwnerAddress
		if address == "" {
			address = order.Owner
		}
		if address == owner && order.Status == pb.FutureOrderStatus_FUTURE_ORDER_OPEN && !order.IsClose {
			if _, _, err := cancelFutureOpenOrderState(sv, &order, height, "rent_funding_unfreeze"); err != nil {
				return removedKeys, err
			}
			removedKeys = append(removedKeys, stateKey)
		}
	}
	delete(futureOrders.byOwner, owner)
	return removedKeys, nil
}

                                                                              
                                                                            
                                                                               
                               
func rentFundingHasRemainingLiquidity(sv StateView, pair string, takerSide pb.OrderSide, referencePrice *big.Int, owner string, height uint64) (bool, []string, []string, error) {
	price, touched, exhausted, removedKeys, err := prepareRentFundingMakerPage(sv, pair, takerSide, referencePrice, owner, height)
	if err != nil {
		return false, touched, removedKeys, err
	}
	return price != nil || exhausted, touched, removedKeys, nil
}
