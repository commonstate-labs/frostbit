package vm

import (
	"dex/db"
	"dex/future"
	"dex/keys"
	"dex/pb"
	"encoding/binary"
	"fmt"
	"math/big"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
)

const (
	parallelFutureHookPreviewMinPositions = 512
	parallelFutureHookPreviewMaxWork      = 8
)

                                                 
                                                     

                                                                   
func getFuturePairs(sv StateView) ([]string, error) {
	prefix := keys.KeyFutureMarketConfigPrefix()
	kvs, err := sv.Scan(prefix)
	if err != nil {
		return nil, err
	}
	pairs := make([]string, 0, len(kvs))
	for _, v := range kvs {
		pairs = append(pairs, string(v))
	}
	sort.Strings(pairs)
	return pairs, nil
}

type futurePairHookState struct {
	spotChanged   bool
	futureChanged bool
	due           bool
}

type futureBlockHookStats struct {
	ExpireDuration               time.Duration
	DueScanDuration              time.Duration
	FallbackScanDuration         time.Duration
	PairLoopDuration             time.Duration
	EMADuration                  time.Duration
	FundingDuration              time.Duration
	FundingRateDuration          time.Duration
	FundingApplyDuration         time.Duration
	LiquidationDuration          time.Duration
	LiquidationCollectDuration   time.Duration
	LiquidationOrderbookDuration time.Duration
	LiquidationMarkDuration      time.Duration
	LiquidationADLDuration       time.Duration
	Pairs                        int
	DuePairs                     int
	FallbackPairs                int
	PriceChangedPairs            int
	FundingSettledPairs          int
	LiquidationCheckedPairs      int
	LiquidationCandidates        int
	LiquidationAtRisk            int
}

                                                                                                          
                                                                                                                       
                                                                                                        
func processFutureBlockHooks(sv StateView, height uint64, spotChanged, futureChanged map[string]bool) error {
	_, err := processFutureBlockHooksWithStats(sv, height, spotChanged, futureChanged)
	return err
}

func processFutureBlockHooksWithStats(sv StateView, height uint64, spotChanged, futureChanged map[string]bool) (futureBlockHookStats, error) {
	var stats futureBlockHookStats
	hookSnap := sv.Snapshot()
	rollbackHooks := func(cause error) (futureBlockHookStats, error) {
		if revertErr := sv.Revert(hookSnap); revertErr != nil {
			return stats, fmt.Errorf("%v (rollback future hooks: %w)", cause, revertErr)
		}
		return stats, cause
	}
	expireAt := time.Now()
	if err := expireFutureOrders(sv, height); err != nil {
		stats.ExpireDuration = time.Since(expireAt)
		return rollbackHooks(fmt.Errorf("expire future orders at height %d: %w", height, err))
	}
	stats.ExpireDuration = time.Since(expireAt)

	pairStates := make(map[string]futurePairHookState)
	for pair, changed := range spotChanged {
		if !changed || pair == "" {
			continue
		}
		st := pairStates[pair]
		st.spotChanged = true
		pairStates[pair] = st
	}
	for pair, changed := range futureChanged {
		if !changed || pair == "" {
			continue
		}
		st := pairStates[pair]
		st.futureChanged = true
		pairStates[pair] = st
	}

	dueScanAt := time.Now()
	duePairs, err := getFutureFundingDuePairs(sv, height)
	stats.DueScanDuration = time.Since(dueScanAt)
	if err != nil {
		return rollbackHooks(fmt.Errorf("scan future funding due pairs at height %d: %w", height, err))
	}
	stats.DuePairs = len(duePairs)
	for _, pair := range duePairs {
		st := pairStates[pair]
		st.due = true
		pairStates[pair] = st
	}

	                                                                                                
	if len(duePairs) == 0 && future.DefaultFundingInterval > 0 && height%future.DefaultFundingInterval == 0 {
		fallbackScanAt := time.Now()
		fallbackPairs, err := getFuturePairs(sv)
		stats.FallbackScanDuration = time.Since(fallbackScanAt)
		if err != nil {
			return rollbackHooks(fmt.Errorf("scan future markets for funding fallback: %w", err))
		}
		stats.FallbackPairs = len(fallbackPairs)
		for _, pair := range fallbackPairs {
			if !isFutureFundingDue(sv, pair, height) {
				continue
			}
			st := pairStates[pair]
			st.due = true
			pairStates[pair] = st
		}
	}

	pairs := make([]string, 0, len(pairStates))
	for pair := range pairStates {
		pairs = append(pairs, pair)
	}
	sort.Strings(pairs)
	stats.Pairs = len(pairs)

	pairLoopAt := time.Now()
	for _, pair := range pairs {
		st := pairStates[pair]
		if st.due {
			sv.Del(keys.KeyFutureFundingDue(height, pair))
		}
		marketExists, err := futureMarketExists(sv, pair)
		if err != nil {
			stats.PairLoopDuration = time.Since(pairLoopAt)
			return rollbackHooks(fmt.Errorf("read future market %s: %w", pair, err))
		}
		if !marketExists {
			continue
		}

		priceChanged := st.spotChanged || st.futureChanged
		if priceChanged {
			emaAt := time.Now()
			if err := updateMarkPriceEMA(sv, pair, st.spotChanged, st.futureChanged); err != nil {
				stats.EMADuration += time.Since(emaAt)
				stats.PairLoopDuration = time.Since(pairLoopAt)
				return rollbackHooks(fmt.Errorf("update future mark price for %s: %w", pair, err))
			}
			stats.EMADuration += time.Since(emaAt)
			stats.PriceChangedPairs++
		}

		wasDue := isFutureFundingDue(sv, pair, height)
		fundingAt := time.Now()
		fundingSettled, fundingErr := settleFundingRateIfDueWithStats(sv, pair, height, &stats)
		stats.FundingDuration += time.Since(fundingAt)
		if fundingErr != nil {
			stats.PairLoopDuration = time.Since(pairLoopAt)
			return rollbackHooks(fmt.Errorf("settle funding for %s: %w", pair, fundingErr))
		}
		if fundingSettled {
			stats.FundingSettledPairs++
		}
		if !fundingSettled {
			scheduleFutureFundingDue(sv, pair, nextFutureFundingDueHeight(sv, pair, height, wasDue))
		}

		                                                            
		if priceChanged || fundingSettled {
			stats.LiquidationCheckedPairs++
			if err := checkFutureLiquidationsWithStats(sv, pair, height, &stats); err != nil {
				stats.PairLoopDuration = time.Since(pairLoopAt)
				return rollbackHooks(fmt.Errorf("check future liquidations for %s: %w", pair, err))
			}
		}
	}
	stats.PairLoopDuration = time.Since(pairLoopAt)
	return stats, nil
}

func expireFutureOrders(sv StateView, height uint64) error {
	if height == 0 || future.DefaultNoFillOrderTTLBlocks == 0 {
		return nil
	}
	kvs, err := sv.Scan(keys.KeyFutureOrderExpiryPrefix(height))
	if err != nil {
		return fmt.Errorf("scan expiry index: %w", err)
	}
	if len(kvs) == 0 {
		return nil
	}
	expiryKeys := make([]string, 0, len(kvs))
	for key := range kvs {
		expiryKeys = append(expiryKeys, key)
	}
	sort.Strings(expiryKeys)
	for _, expiryKey := range expiryKeys {
		orderID := futureOrderIDFromExpiryKey(expiryKey, height)
		if orderID == "" {
			sv.Del(expiryKey)
			continue
		}
		data, exists, err := sv.Get(keys.KeyFutureOrderState(orderID))
		if err != nil {
			return fmt.Errorf("read expiring order %s: %w", orderID, err)
		}
		if !exists || len(data) == 0 {
			sv.Del(expiryKey)
			continue
		}
		var state pb.FutureOrderState
		if err := proto.Unmarshal(data, &state); err != nil {
			return fmt.Errorf("decode expiring order %s: %w", orderID, err)
		}
		if state.OrderId == "" {
			return fmt.Errorf("decode expiring order %s: empty order id", orderID)
		}
		if state.Status != pb.FutureOrderStatus_FUTURE_ORDER_OPEN || state.IsClose {
			sv.Del(expiryKey)
			continue
		}
		if state.ExpiryHeight != height {
			sv.Del(expiryKey)
			continue
		}
		if _, _, err := cancelFutureOpenOrderState(sv, &state, height, "future_order_margin_unfreeze_expired"); err != nil {
			return fmt.Errorf("cancel expiring order %s: %w", orderID, err)
		}
		sv.Del(expiryKey)
	}
	return nil
}

func getFutureFundingDuePairs(sv StateView, height uint64) ([]string, error) {
	kvs, err := sv.Scan(keys.KeyFutureFundingDuePrefix(height))
	if err != nil {
		return nil, err
	}
	pairs := make([]string, 0, len(kvs))
	for _, v := range kvs {
		if len(v) == 0 {
			continue
		}
		pairs = append(pairs, string(v))
	}
	sort.Strings(pairs)
	return pairs, nil
}

func futureFundingLastHeight(sv StateView, pair string) uint64 {
	if data, exists, _ := sv.Get(keys.KeyFutureFundingHeight(pair)); exists && len(data) == 8 {
		return binary.BigEndian.Uint64(data)
	}
	return 0
}

func futureFundingLastHeightChecked(sv StateView, pair string) (uint64, error) {
	data, exists, err := sv.Get(keys.KeyFutureFundingHeight(pair))
	if err != nil {
		return 0, fmt.Errorf("read funding height: %w", err)
	}
	if !exists || len(data) == 0 {
		return 0, nil
	}
	if len(data) != 8 {
		return 0, fmt.Errorf("invalid funding height encoding: %d bytes", len(data))
	}
	return binary.BigEndian.Uint64(data), nil
}

func isFutureFundingDue(sv StateView, pair string, height uint64) bool {
	lastHeight := futureFundingLastHeight(sv, pair)
	return height >= lastHeight && height-lastHeight >= future.DefaultFundingInterval
}

func nextFutureFundingDueHeight(sv StateView, pair string, height uint64, wasDue bool) uint64 {
	if wasDue {
		return height + future.DefaultFundingInterval
	}
	lastHeight := futureFundingLastHeight(sv, pair)
	next := lastHeight + future.DefaultFundingInterval
	if next <= height {
		return height + future.DefaultFundingInterval
	}
	return next
}

func scheduleFutureFundingDue(sv StateView, pair string, dueHeight uint64) {
	if pair == "" || dueHeight == 0 {
		return
	}
	sv.Set(keys.KeyFutureFundingDue(dueHeight, pair), []byte(pair))
}

func futureMarketExists(sv StateView, pair string) (bool, error) {
	data, exists, err := sv.Get(keys.KeyFutureMarketConfig(pair))
	if err != nil {
		return false, err
	}
	return exists && len(data) > 0, nil
}

                                                                         
  
                                         
                                                                                        
                                                                                        
                                                            
  
                                                                                                
                                                                      
  
                                                                                      
                                                                                         
                                                                                                  
                                               
                                                                 
func updateMarkPriceEMA(sv StateView, pair string, spotChanged, futureChanged bool) error {
	spotKey := keys.KeyFutureSpotEMA(pair)
	futKey := keys.KeyFutureEMA(pair)
	markKey := keys.KeyFutureMarkPrice(pair)

	spotEMA, err := readPosBigChecked(sv, spotKey)
	if err != nil {
		return fmt.Errorf("read spot EMA: %w", err)
	}
	futEMA, err := readPosBigChecked(sv, futKey)
	if err != nil {
		return fmt.Errorf("read future EMA: %w", err)
	}
	lastMark, err := readPosBigChecked(sv, markKey)
	if err != nil {
		return fmt.Errorf("read previous mark price: %w", err)
	}

	if spotChanged {
		spot, err := readPosBigChecked(sv, keys.KeySpotLastPrice(pair))
		if err != nil {
			return fmt.Errorf("read spot last price: %w", err)
		}
		if spot != nil {
			spotEMA = future.CalcEMA(spotEMA, spot, future.DefaultEMAWindow)
			sv.Set(spotKey, spotEMA.Bytes())
		}
	}
	if futureChanged {
		fut, err := readPosBigChecked(sv, keys.KeyFutureLastPrice(pair))
		if err != nil {
			return fmt.Errorf("read future last price: %w", err)
		}
		if fut != nil {
			futEMA = future.CalcEMA(futEMA, fut, future.DefaultEMAWindow)
			sv.Set(futKey, futEMA.Bytes())
		}
	}

	                                                                                            
	if spotEMA == nil {
		spotEMA, err = readPosBigChecked(sv, keys.KeySpotLastPrice(pair))
		if err != nil {
			return fmt.Errorf("read cold-start spot price: %w", err)
		}
	}
	if futEMA == nil && spotEMA != nil {
		futEMA = new(big.Int).Set(spotEMA)
	}
	if lastMark == nil {
		if spotEMA != nil {
			lastMark = new(big.Int).Set(spotEMA)
		} else if futEMA != nil {
			lastMark = new(big.Int).Set(futEMA)
		}
	}

	mark := medianOfThreePos(spotEMA, futEMA, lastMark)
	if mark == nil || mark.Sign() <= 0 {
		return nil
	}
	sv.Set(markKey, mark.Bytes())

	                                                                                                   
	                                                                                     
	                                                                           
	                                                                                                    
	                                                                                
	settleKey := keys.KeyFutureSettleMarkPrice(pair)
	_, exists, err := sv.Get(settleKey)
	if err != nil {
		return fmt.Errorf("read settle mark seed: %w", err)
	}
	if !exists {
		sv.Set(settleKey, mark.Bytes())
	}
	return nil
}

                                                                 
func readPosBig(sv StateView, key string) *big.Int {
	data, exists, _ := sv.Get(key)
	if !exists || len(data) == 0 {
		return nil
	}
	v := new(big.Int).SetBytes(data)
	if v.Sign() <= 0 {
		return nil
	}
	return v
}

func readPosBigChecked(sv StateView, key string) (*big.Int, error) {
	data, exists, err := sv.Get(key)
	if err != nil {
		return nil, err
	}
	if !exists || len(data) == 0 {
		return nil, nil
	}
	v := new(big.Int).SetBytes(data)
	if v.Sign() <= 0 {
		return nil, nil
	}
	return v, nil
}

                                                                       
                                                                                    
func medianOfThreePos(a, b, c *big.Int) *big.Int {
	vals := make([]*big.Int, 0, 3)
	for _, v := range []*big.Int{a, b, c} {
		if v != nil && v.Sign() > 0 {
			vals = append(vals, v)
		}
	}
	switch len(vals) {
	case 0:
		return nil
	case 1:
		return new(big.Int).Set(vals[0])
	case 2:
		sum := new(big.Int).Add(vals[0], vals[1]) // safe-math-ok: 两路价格的均值分子，下一步右移除以二，不写资金余额
		return sum.Rsh(sum, 1)
	}
	if vals[0].Cmp(vals[1]) > 0 {
		vals[0], vals[1] = vals[1], vals[0]
	}
	if vals[1].Cmp(vals[2]) > 0 {
		vals[1], vals[2] = vals[2], vals[1]
	}
	if vals[0].Cmp(vals[1]) > 0 {
		vals[0], vals[1] = vals[1], vals[0]
	}
	return new(big.Int).Set(vals[1])
}

                                                                                                           
func settleFundingRateIfDue(sv StateView, pair string, height uint64) (bool, error) {
	return settleFundingRateIfDueWithStats(sv, pair, height, nil)
}

func settleFundingRateIfDueWithStats(sv StateView, pair string, height uint64, stats *futureBlockHookStats) (bool, error) {
	lastHeight, err := futureFundingLastHeightChecked(sv, pair)
	if err != nil {
		return false, err
	}
	if height < lastHeight || height-lastHeight < future.DefaultFundingInterval {
		return false, nil
	}
	rateAt := time.Now()

	                                                                            
	  
	                                                                                              
	                                                                                                    
	                                                                                                
	                                                                           
	  
	                                                                                        
	                                                                                 
	spotPrice, err := readPosBigChecked(sv, keys.KeyFutureSpotEMA(pair))
	if err != nil {
		return false, fmt.Errorf("read spot EMA: %w", err)
	}
	if spotPrice == nil {
		                                                          
		spotPrice, err = readPosBigChecked(sv, keys.KeySpotLastPrice(pair))
		if err != nil {
			return false, fmt.Errorf("read spot last price: %w", err)
		}
	}
	if spotPrice == nil {
		if stats != nil {
			stats.FundingRateDuration += time.Since(rateAt)
		}
		return false, nil
	}

	                                                                                                  
	futurePrice, err := readPosBigChecked(sv, keys.KeyFutureEMA(pair))
	if err != nil {
		return false, fmt.Errorf("read future EMA: %w", err)
	}
	if futurePrice == nil {
		futurePrice, err = readPosBigChecked(sv, keys.KeyFutureLastPrice(pair))
		if err != nil {
			return false, fmt.Errorf("read future last price: %w", err)
		}
	}
	if futurePrice == nil {
		if stats != nil {
			stats.FundingRateDuration += time.Since(rateAt)
		}
		return false, nil
	}
	                                                                           
	                                                                           
	                                                                   
	snap := sv.Snapshot()
	rollbackFundingRound := func(stage string, err error) (bool, error) {
		if revertErr := sv.Revert(snap); revertErr != nil {
			return false, fmt.Errorf("funding round %s failed: %v (rollback: %w)", stage, err, revertErr)
		}
		return false, fmt.Errorf("funding round %s failed: %w", stage, err)
	}

	                     
	rate := future.CalcFundingRate(futurePrice, spotPrice, future.DefaultMaxFundingRateBps)

	                                                                                   
	                                                                                               
	                                                                                                  
	cumFundingStore := &svCumulativeFundingStore{sv: sv}
	prevLong, prevShort, err := cumFundingStore.GetCumulativeFunding(pair)
	if err != nil {
		return rollbackFundingRound("read_previous_cumulative", err)
	}
	if err := cumFundingStore.SetPrevCumulativeFunding(pair, prevLong, prevShort); err != nil {
		return rollbackFundingRound("write_previous_cumulative", err)
	}

	                                                                                                  
	                                                                                             
	mp, err := readPosBigChecked(sv, keys.KeyFutureMarkPrice(pair))
	if err != nil {
		return rollbackFundingRound("read_settle_mark", err)
	}
	if mp != nil {
		sv.Set(keys.KeyFutureSettleMarkPrice(pair), mp.Bytes())
	}

	eng := newFutureEngineWithActivity(sv, height)
	if err := eng.SettleFundingRate(pair, rate); err != nil {
		return rollbackFundingRound("advance_cumulative", err)
	}
	if stats != nil {
		stats.FundingRateDuration += time.Since(rateAt)
	}
	applyAt := time.Now()
	if err := eng.ApplyPendingFundingForPair(pair); err != nil {
		return rollbackFundingRound("apply_positions", err)
	}
	if stats != nil {
		stats.FundingApplyDuration += time.Since(applyAt)
	}
	rateAt = time.Now()

	                              
	heightBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(heightBytes, height)
	sv.Set(keys.KeyFutureFundingHeight(pair), heightBytes)

	                                                                                                             
	rateBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(rateBytes, uint64(rate))
	sv.Set(keys.KeyFutureFundingRate(pair), rateBytes)
	scheduleFutureFundingDue(sv, pair, height+future.DefaultFundingInterval)
	if stats != nil {
		stats.FundingRateDuration += time.Since(rateAt)
	}

	return true, nil
}

                                                         
                                                                                           
  
                                                                                         
                                                                                                 
                                                                                                     
                                                                                  
type futureLiquidationCandidate struct {
	id              string
	shouldLiquidate bool
}

func checkFutureLiquidations(sv StateView, pair string, height uint64) error {
	return checkFutureLiquidationsWithStats(sv, pair, height, nil)
}

func checkFutureLiquidationsWithStats(sv StateView, pair string, height uint64, stats *futureBlockHookStats) (retErr error) {
	totalAt := time.Now()
	checkSnap := sv.Snapshot()
	defer func() {
		if stats != nil {
			stats.LiquidationDuration += time.Since(totalAt)
		}
		if retErr != nil {
			if revertErr := sv.Revert(checkSnap); revertErr != nil {
				retErr = fmt.Errorf("%v (revert liquidation check: %w)", retErr, revertErr)
			}
		}
	}()
	eng := newFutureEngineWithActivity(sv, height)
	cfg, err := eng.GetMarketConfig(pair)
	if err != nil {
		return fmt.Errorf("load future market config for liquidation %s: %w", pair, err)
	}
	if cfg == nil {
		return fmt.Errorf("missing future market config for liquidation %s", pair)
	}
	markData, markExists, err := sv.Get(keys.KeyFutureMarkPrice(pair))
	if err != nil {
		return fmt.Errorf("read future mark price for liquidation %s: %w", pair, err)
	}
	if !markExists || len(markData) == 0 {
		return nil
	}
	markPrice := new(big.Int).SetBytes(markData)
	if markPrice.Sign() <= 0 {
		return nil
	}

	posStore := &svPositionStore{sv: sv}
	collectAt := time.Now()
	collectSnap := sv.Snapshot()
	candidates, err := collectFutureLiquidationCandidates(posStore, eng, cfg, pair, markPrice)
	if stats != nil {
		stats.LiquidationCollectDuration += time.Since(collectAt)
	}
	if err != nil {
		if revertErr := sv.Revert(collectSnap); revertErr != nil {
			return fmt.Errorf("collect candidates: %v (revert: %w)", err, revertErr)
		}
		return fmt.Errorf("collect candidates: %w", err)
	}
	if stats != nil {
		stats.LiquidationCandidates += len(candidates)
		for _, cand := range candidates {
			if cand.shouldLiquidate {
				stats.LiquidationAtRisk++
			}
		}
	}
	if len(candidates) == 0 {
		return nil
	}

	var obLiqResults []*future.LiquidationResult
	orderbookAt := time.Now()
	for _, cand := range candidates {
		if !cand.shouldLiquidate {
			continue
		}
		pos, err := posStore.GetPosition(cand.id)
		if err != nil {
			return fmt.Errorf("read orderbook liquidation candidate %s: %w", cand.id, err)
		}
		if pos == nil || pos.Status != future.StatusOpen {
			continue
		}
		snap := sv.Snapshot()
		if err := eng.ApplyPendingFunding(pos, cfg); err != nil {
			if revertErr := sv.Revert(snap); revertErr != nil {
				return fmt.Errorf("apply candidate funding %s: %v (revert: %w)", cand.id, err, revertErr)
			}
			return fmt.Errorf("apply candidate funding %s: %w", cand.id, err)
		}
		pos, err = posStore.GetPosition(cand.id)
		if err != nil {
			return fmt.Errorf("re-read funded liquidation candidate %s: %w", cand.id, err)
		}
		if pos == nil || pos.Status != future.StatusOpen {
			continue
		}
		res, err := tryOrderbookLiquidatePositionChecked(sv, pair, height, eng, cfg, markPrice, pos)
		if err != nil {
			return fmt.Errorf("orderbook liquidation candidate %s: %w", cand.id, err)
		}
		if res != nil {
			obLiqResults = append(obLiqResults, res)
		}
	}
	if stats != nil {
		stats.LiquidationOrderbookDuration += time.Since(orderbookAt)
	}

	var adlResults []future.ADLResult
	adlAt := time.Now()
	for _, liq := range obLiqResults {
		if liq == nil || !liq.NeedsADL || liq.ADLShortfall == nil || liq.ADLShortfall.Sign() <= 0 {
			continue
		}
		adlResult, err := eng.ExecuteADL(pair, liq.Side.Opposite(), liq.ADLShortfall)
		if err != nil {
			return fmt.Errorf("execute orderbook liquidation ADL for %s: %w", liq.PositionID, err)
		}
		if adlResult != nil {
			adlResults = append(adlResults, *adlResult)
		}
	}
	if stats != nil {
		stats.LiquidationADLDuration += time.Since(adlAt)
	}

	var liqResults []future.LiquidationResult
	markAt := time.Now()
	for _, cand := range candidates {
		if !cand.shouldLiquidate {
			continue
		}
		pos, err := posStore.GetPosition(cand.id)
		if err != nil {
			return fmt.Errorf("read mark-price liquidation candidate %s: %w", cand.id, err)
		}
		if pos == nil || pos.Status != future.StatusOpen {
			continue
		}
		if !future.ShouldLiquidatePosition(pos, pos.Margin, markPrice, cfg.MaintenanceMarginRateOrDefault()) {
			eng.RefreshLiquidationIndex(pos, cfg)
			continue
		}
		liqResult, err := eng.LiquidateAtPrice(pos, markPrice, cfg)
		if err != nil {
			return fmt.Errorf("liquidate candidate %s at mark price: %w", cand.id, err)
		}
		liqResults = append(liqResults, *liqResult)
		if liqResult.NeedsADL && liqResult.ADLShortfall != nil && liqResult.ADLShortfall.Sign() > 0 {
			adlAt := time.Now()
			adlResult, err := eng.ExecuteADL(pair, pos.Side.Opposite(), liqResult.ADLShortfall)
			if stats != nil {
				stats.LiquidationADLDuration += time.Since(adlAt)
			}
			if err != nil {
				return fmt.Errorf("execute mark-price liquidation ADL for %s: %w", liqResult.PositionID, err)
			}
			if adlResult != nil {
				adlResults = append(adlResults, *adlResult)
			}
		}
	}

	writeFutureADLActivities(sv, pair, height, adlResults)

	writeMarkPriceLiquidationRecords(sv, pair, height, liqResults)
	if stats != nil {
		stats.LiquidationMarkDuration += time.Since(markAt)
	}
	return nil
}

func deleverageActivityKind(withheldProfit *big.Int) string {
	if withheldProfit != nil && withheldProfit.Sign() > 0 {
		return ActKindADL
	}
	return ActKindInsuredDeleverage
}

func writeFutureADLActivities(sv StateView, pair string, height uint64, adlResults []future.ADLResult) {
	for _, adl := range adlResults {
		for _, reduced := range adl.Reduced {
			if reduced.Owner == "" {
				continue
			}
			settlePrice := ""
			if reduced.SettlePrice != nil {
				settlePrice = reduced.SettlePrice.String()
			}
			reducedQty := ""
			if reduced.ReducedQuantity != nil {
				reducedQty = reduced.ReducedQuantity.String()
			}
			grossProfit, withheldProfit, paidProfit := "", "", ""
			if reduced.GrossProfit != nil {
				grossProfit = reduced.GrossProfit.String()
			}
			if reduced.WithheldProfit != nil {
				withheldProfit = reduced.WithheldProfit.String()
			}
			if reduced.PaidProfit != nil {
				paidProfit = reduced.PaidProfit.String()
			}
			recordUserActivity(sv, reduced.Owner, &UserActivityRecord{
				Kind: deleverageActivityKind(reduced.WithheldProfit), Height: height,
				Amount: reducedQty, Token: pair,
				Ref:  reduced.PositionID,
				Note: fmt.Sprintf("settle_price=%s gross_profit=%s withheld_profit=%s paid_profit=%s", settlePrice, grossProfit, withheldProfit, paidProfit),
			})
		}
	}
}

func collectFutureLiquidationCandidates(posStore *svPositionStore, eng *future.Engine, cfg *future.MarketConfig, pair string, markPrice *big.Int) ([]futureLiquidationCandidate, error) {
	                                                                           
	                                                                           
	                                                                          
	                                   
	if _, backfilled, err := posStore.sv.Get(keys.KeyFutureLiqIndexBackfillDone(pair)); err != nil {
		return nil, fmt.Errorf("read liquidation-index backfill marker: %w", err)
	} else if !backfilled {
		candidates, err := collectAllFutureLiquidationCandidates(posStore, eng, cfg, pair, markPrice)
		if err != nil {
			return nil, err
		}
		posStore.sv.Set(keys.KeyFutureLiqIndexBackfillDone(pair), []byte{1})
		return candidates, nil
	}
	atRisk, err := eng.FindAtRiskPositions(pair, markPrice)
	if err != nil {
		return nil, err
	}
	if len(atRisk) == 0 {
		return collectAllFutureLiquidationCandidates(posStore, eng, cfg, pair, markPrice)
	}
	positions := make([]*future.Position, 0, len(atRisk))
	seen := make(map[string]struct{}, len(atRisk))
	indexInconsistent := false
	for _, posID := range atRisk {
		if posID == "" {
			indexInconsistent = true
			continue
		}
		if _, ok := seen[posID]; ok {
			continue
		}
		seen[posID] = struct{}{}
		pos, err := posStore.GetPosition(posID)
		if err != nil {
			return nil, fmt.Errorf("read indexed position %s: %w", posID, err)
		}
		if pos == nil {
			                                                                     
			                                                                      
			                                                                       
			                                                                
			_ = (&svLiquidationIndex{sv: posStore.sv}).Remove(posID, pair, future.Long, nil)
			indexInconsistent = true
			continue
		}
		if pos.Pair != pair {
			                                                               
			                                                                       
			                                                                    
			                                                                     
			                                                                    
			                                             
			indexInconsistent = true
			continue
		}
		if pos.Status != future.StatusOpen {
			                                                                      
			                                                          
			_ = (&svLiquidationIndex{sv: posStore.sv}).Remove(posID, pair, pos.Side, nil)
			indexInconsistent = true
			continue
		}
		positions = append(positions, pos)
	}
	candidates, err := previewFutureLiquidationCandidates(eng, cfg, positions, markPrice)
	if err != nil {
		return nil, fmt.Errorf("preview indexed liquidation candidates for %s: %w", pair, err)
	}
	for i, cand := range candidates {
		if i >= len(positions) || cand.shouldLiquidate {
			continue
		}
		pos := positions[i]
		if futureLiquidationIndexClaimsAtRisk(posStore.sv, pos, markPrice) {
			                                                                      
			                                                                   
			                                                                   
			indexInconsistent = true
		}
		eng.RefreshLiquidationIndex(pos, cfg)
	}
	if indexInconsistent {
		return collectAllFutureLiquidationCandidates(posStore, eng, cfg, pair, markPrice)
	}
	return candidates, nil
}

                                                                           
                                                                               
                                                                                
                                                                 
  
                                                                               
                                                                         
                                                                            
func collectAllFutureLiquidationCandidates(posStore *svPositionStore, eng *future.Engine, cfg *future.MarketConfig, pair string, markPrice *big.Int) ([]futureLiquidationCandidate, error) {
	positions, err := posStore.GetAllOpenPositions(pair)
	if err != nil {
		return nil, fmt.Errorf("scan all open positions for %s: %w", pair, err)
	}
	if len(positions) == 0 {
		return nil, nil
	}
	openPositions := make([]*future.Position, 0, len(positions))
	for _, pos := range positions {
		if pos == nil || pos.Pair != pair || pos.Status != future.StatusOpen {
			continue
		}
		openPositions = append(openPositions, pos)
	}
	candidates, err := previewFutureLiquidationCandidates(eng, cfg, openPositions, markPrice)
	if err != nil {
		return nil, fmt.Errorf("preview full-scan liquidation candidates for %s: %w", pair, err)
	}
	for _, pos := range openPositions {
		eng.RefreshLiquidationIndex(pos, cfg)
	}
	return candidates, nil
}

                                                                              
                                                                              
                                                                               
                                                            
func futureLiquidationIndexClaimsAtRisk(sv StateView, pos *future.Position, markPrice *big.Int) bool {
	if pos == nil || markPrice == nil || markPrice.Sign() <= 0 {
		return true
	}
	ref, exists, err := sv.Get(keys.KeyFutureLiqPosRef(pos.ID))
	if err != nil || !exists || len(ref) == 0 {
		return true
	}
	prefix := keys.KeyFutureLiqIndexPrefix(sideTag(pos.Side), pos.Pair)
	indexKey := string(ref)
	if !strings.HasPrefix(indexKey, prefix) {
		return true
	}
	afterPrefix := indexKey[len(prefix):]
	if len(afterPrefix) < 67 {
		return true
	}
	markKey, err := db.PriceToKey128(markPrice.String())
	if err != nil {
		return true
	}
	liqPriceKey := afterPrefix[:67]
	if pos.Side == future.Long {
		return liqPriceKey >= markKey
	}
	return liqPriceKey <= markKey
}

func previewFutureLiquidationCandidates(eng *future.Engine, cfg *future.MarketConfig, positions []*future.Position, markPrice *big.Int) ([]futureLiquidationCandidate, error) {
	if len(positions) == 0 {
		return nil, nil
	}
	mmRate := cfg.MaintenanceMarginRateOrDefault()
	workers := futureHookParallelWorkers(len(positions), parallelFutureHookPreviewMinPositions)
	candidates := make([]futureLiquidationCandidate, len(positions))
	if workers <= 1 {
		for i, pos := range positions {
			candidate, err := previewFutureLiquidationCandidate(eng, pos, markPrice, mmRate)
			if err != nil {
				return nil, err
			}
			candidates[i] = candidate
		}
		return candidates, nil
	}
	chunk := (len(positions) + workers - 1) / workers
	errs := make([]error, len(positions))
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		start := i * chunk
		if start >= len(positions) {
			break
		}
		end := start + chunk
		if end > len(positions) {
			end = len(positions)
		}
		wg.Add(1)
		go func(start, end int) {
			defer wg.Done()
			for j := start; j < end; j++ {
				candidates[j], errs[j] = previewFutureLiquidationCandidate(eng, positions[j], markPrice, mmRate)
			}
		}(start, end)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return candidates, nil
}

func previewFutureLiquidationCandidate(eng *future.Engine, pos *future.Position, markPrice *big.Int, mmRate uint64) (futureLiquidationCandidate, error) {
	if pos == nil || pos.Status != future.StatusOpen {
		return futureLiquidationCandidate{}, nil
	}
	pending, err := eng.PreviewPendingFundingStrict(pos)
	if err != nil {
		return futureLiquidationCandidate{}, fmt.Errorf("preview pending funding for liquidation candidate %s: %w", pos.ID, err)
	}
	projectedMargin := new(big.Int).Add(pos.Margin, pending) // safe-math-ok: 资金费 pending 可为负，只读清算预览随后将负值归零
	if projectedMargin.Sign() < 0 {
		projectedMargin = new(big.Int)
	}
	shouldLiquidate := future.ShouldLiquidatePosition(pos, projectedMargin, markPrice, mmRate)
	return futureLiquidationCandidate{id: pos.ID, shouldLiquidate: shouldLiquidate}, nil
}

func futureHookParallelWorkers(items, minItems int) int {
	if items < minItems {
		return 1
	}
	workers := runtime.GOMAXPROCS(0)
	if workers > parallelFutureHookPreviewMaxWork {
		workers = parallelFutureHookPreviewMaxWork
	}
	if workers < 2 || items < workers*2 {
		return 1
	}
	return workers
}

func writeMarkPriceLiquidationRecords(sv StateView, pair string, height uint64, liqResults []future.LiquidationResult) {
	for i, liq := range liqResults {
		closeSide := pb.OrderSide_BUY
		if liq.Side == future.Long {
			closeSide = pb.OrderSide_SELL
		}
		tradeTsKey, tradeTsText := deterministicTradeTime(height, i)
		tradeRecord := &pb.TradeRecord{
			TradeId:   fmt.Sprintf("liq_%s", liq.PositionID),
			Pair:      pair,
			Price:     liq.ClosePrice.String(),
			Amount:    liq.Quantity.String(),
			TakerSide: closeSide,
			Timestamp: tradeTsText,
			Height:    height,
		}
		if tradeData, err := proto.Marshal(tradeRecord); err == nil {
			sv.Set(keys.KeyFutureTradeRecord(pair, tradeTsKey, tradeRecord.TradeId), tradeData)
		}

		liqOrderID := fmt.Sprintf("liq_%s", liq.PositionID)
		liqNotional := future.CalcNotional(liq.Quantity, liq.ClosePrice)
		liqState := &pb.FutureOrderState{
			OrderId: liqOrderID, Owner: liq.Owner, Pair: pair,
			Side: int32(closeSide), Price: liq.ClosePrice.String(),
			Amount: liq.Quantity.String(), FilledBase: liq.Quantity.String(),
			FilledQuote: liqNotional.String(),
			IsClose:     true, ClosePositionId: liq.PositionID,
			Status:       pb.FutureOrderStatus_FUTURE_ORDER_LIQUIDATED,
			OwnerAddress: liq.Owner, CreateHeight: height,
		}
		_ = writeFutureOrderSnapshot(sv, liqState)
		recordUserActivity(sv, liq.Owner, &UserActivityRecord{
			Kind: ActKindLiquidation, Height: height,
			Amount: liq.Quantity.String(), Token: pair,
			Ref:  liq.PositionID,
			Note: fmt.Sprintf("close_price=%s side=%d", liq.ClosePrice.String(), int(liq.Side)),
		})
	}
}
