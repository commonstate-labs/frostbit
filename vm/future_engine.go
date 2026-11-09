package vm

import (
	"dex/db"
	"dex/future"
	"dex/keys"
	"dex/pb"
	"dex/referral"
	"dex/rent"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"
)

func futureOrderIsOpen(st *pb.FutureOrderState) bool {
	return st != nil && st.Status == pb.FutureOrderStatus_FUTURE_ORDER_OPEN
}

func futureOrderBookSide(st *pb.FutureOrderState) pb.OrderSide {
	if st != nil && st.Side == int32(future.Short) {
		return pb.OrderSide_SELL
	}
	return pb.OrderSide_BUY
}

func futureOrderPriceIndexKey(st *pb.FutureOrderState) (string, bool) {
	if st == nil || st.Pair == "" || st.Price == "" || st.OrderId == "" {
		return "", false
	}
	priceKey67, err := db.PriceToKey128(st.Price)
	if err != nil {
		return "", false
	}
	return keys.KeyFutureOrderPriceIndex(st.Pair, futureOrderBookSide(st), false, priceKey67, st.OrderId), true
}

func futureOrderExpiryKeyByDueHeight(st *pb.FutureOrderState, dueHeight uint64) (string, bool) {
	if st == nil || st.OrderId == "" || dueHeight == 0 {
		return "", false
	}
	return keys.KeyFutureOrderExpiry(dueHeight, st.OrderId), true
}

func futureOrderExpiryDueHeight(anchorHeight uint64) uint64 {
	if anchorHeight == 0 || future.DefaultNoFillOrderTTLBlocks == 0 {
		return 0
	}
	return anchorHeight + future.DefaultNoFillOrderTTLBlocks
}

func futureOrderNoFillExpiryHeight(st *pb.FutureOrderState) uint64 {
	if st == nil {
		return 0
	}
	return futureOrderExpiryDueHeight(st.GetCreateHeight())
}

func futureOrderCurrentExpiryHeight(st *pb.FutureOrderState) uint64 {
	if st == nil || st.OrderId == "" || future.DefaultNoFillOrderTTLBlocks == 0 {
		return 0
	}
	if st.ExpiryHeight != 0 {
		return st.ExpiryHeight
	}
	return futureOrderNoFillExpiryHeight(st)
}

func normalizeFutureOrderExpiryHeight(st *pb.FutureOrderState) {
	if st == nil {
		return
	}
	if futureOrderIsOpen(st) && !st.IsClose {
		if st.ExpiryHeight == 0 {
			st.ExpiryHeight = futureOrderNoFillExpiryHeight(st)
		}
		return
	}
	st.ExpiryHeight = 0
}

func futureOrderExpiryValue(st *pb.FutureOrderState) []byte {
	if st == nil || st.OrderId == "" {
		return nil
	}
	return []byte{1}
}

func futureOrderIDFromExpiryKey(expiryKey string, height uint64) string {
	prefix := keys.KeyFutureOrderExpiryPrefix(height)
	if strings.HasPrefix(expiryKey, prefix) {
		return strings.TrimPrefix(expiryKey, prefix)
	}
	return ""
}

func parseFutureOrderBigInt(raw string) *big.Int {
	if raw == "" {
		return new(big.Int)
	}
	v, ok := new(big.Int).SetString(raw, 10)
	if !ok {
		return new(big.Int)
	}
	return v
}

func syncFutureOrderExpiryIndex(sv StateView, st *pb.FutureOrderState, oldStates ...*pb.FutureOrderState) {
	newKey := ""
	if futureOrderIsOpen(st) && !st.IsClose {
		if key, ok := futureOrderExpiryKeyByDueHeight(st, st.ExpiryHeight); ok {
			newKey = key
			sv.Set(key, futureOrderExpiryValue(st))
		}
	}
	for _, old := range oldStates {
		if key, ok := futureOrderExpiryKeyByDueHeight(old, futureOrderCurrentExpiryHeight(old)); ok && key != newKey {
			sv.Del(key)
		}
	}
}

func writeFutureOrderSnapshot(sv StateView, st *pb.FutureOrderState, oldStates ...*pb.FutureOrderState) error {
	if st == nil || st.OrderId == "" {
		return nil
	}
	normalizeFutureOrderExpiryHeight(st)
	data, err := proto.Marshal(st)
	if err != nil {
		return err
	}
	newIndexKey := ""
	if futureOrderIsOpen(st) {
		sv.Set(keys.KeyFutureOrderState(st.OrderId), data)
		rent.MarkOwnerActivity(sv, futureOrderOwner(st), rent.OwnerMarkerFutureOrder)
		if indexKey, ok := futureOrderPriceIndexKey(st); ok {
			newIndexKey = indexKey
			sv.Set(indexKey, data)
		}
	} else {
		sv.Del(keys.KeyFutureOrderState(st.OrderId))
	}
	newHistoryKey := keys.KeyFutureAccountOrderFromState(st)
	for _, old := range oldStates {
		if futureOrderIsOpen(old) {
			if indexKey, ok := futureOrderPriceIndexKey(old); ok {
				if indexKey != newIndexKey {
					sv.Del(indexKey)
				}
			}
		}
		oldHistoryKey := keys.KeyFutureAccountOrderFromState(old)
		if oldHistoryKey != "" && oldHistoryKey != newHistoryKey {
			sv.Del(oldHistoryKey)
		}
	}
	if newHistoryKey != "" {
		sv.Set(newHistoryKey, data)
	}
	syncFutureOrderExpiryIndex(sv, st, oldStates...)
	return nil
}

func futureOrderOwner(st *pb.FutureOrderState) string {
	if st == nil {
		return ""
	}
	if st.OwnerAddress != "" {
		return st.OwnerAddress
	}
	return st.Owner
}

var futureReservedMarginAllocationMagic = []byte{0, 'F', 'M', 'R', 3}

type futureReservedMarginAllocation struct {
	Remaining       *big.Int
	BaselineReserve *big.Int
	BaselineQty     *big.Int
	ConsumedQty     *big.Int
	OpenConsumedQty *big.Int
	MakerPrice      *big.Int
	MakerLeverage   uint64
}

func encodeFutureReservedMarginAllocation(state *futureReservedMarginAllocation) []byte {
	payload := fmt.Sprintf("%s:%s:%s:%s:%s:%s:%d",
		state.Remaining.String(),
		state.BaselineReserve.String(),
		state.BaselineQty.String(),
		state.ConsumedQty.String(),
		state.OpenConsumedQty.String(),
		state.MakerPrice.String(),
		state.MakerLeverage,
	)
	out := make([]byte, 0, len(futureReservedMarginAllocationMagic)+len(payload))
	out = append(out, futureReservedMarginAllocationMagic...)
	return append(out, payload...)
}

func parseFutureReservedMarginUint(field, raw string) (*big.Int, error) {
	v, ok := new(big.Int).SetString(raw, 10)
	if !ok || v.Sign() < 0 {
		return nil, fmt.Errorf("invalid %s %q", field, raw)
	}
	return v, nil
}

                                                                       
                                                                             
                                                                            
                                                       
func decodeFutureReservedMarginAllocation(data []byte) (*futureReservedMarginAllocation, bool, error) {
	if len(data) < len(futureReservedMarginAllocationMagic) || string(data[:len(futureReservedMarginAllocationMagic)]) != string(futureReservedMarginAllocationMagic) {
		return &futureReservedMarginAllocation{Remaining: new(big.Int).SetBytes(data)}, false, nil
	}
	parts := strings.Split(string(data[len(futureReservedMarginAllocationMagic):]), ":")
	if len(parts) != 7 {
		return nil, true, fmt.Errorf("malformed future reserved margin allocation: field count=%d", len(parts))
	}
	remaining, err := parseFutureReservedMarginUint("remaining", parts[0])
	if err != nil {
		return nil, true, err
	}
	baselineReserve, err := parseFutureReservedMarginUint("baseline reserve", parts[1])
	if err != nil {
		return nil, true, err
	}
	baselineQty, err := parseFutureReservedMarginUint("baseline quantity", parts[2])
	if err != nil {
		return nil, true, err
	}
	consumedQty, err := parseFutureReservedMarginUint("consumed quantity", parts[3])
	if err != nil {
		return nil, true, err
	}
	openConsumedQty, err := parseFutureReservedMarginUint("open consumed quantity", parts[4])
	if err != nil {
		return nil, true, err
	}
	makerPrice, err := parseFutureReservedMarginUint("maker price", parts[5])
	if err != nil {
		return nil, true, err
	}
	makerLeverageBI, err := parseFutureReservedMarginUint("maker leverage", parts[6])
	if err != nil {
		return nil, true, err
	}
	if baselineQty.Sign() <= 0 {
		return nil, true, fmt.Errorf("future reserved margin baseline quantity must be positive")
	}
	if consumedQty.Cmp(baselineQty) > 0 {
		return nil, true, fmt.Errorf("future reserved margin consumed quantity %s exceeds baseline %s", consumedQty, baselineQty)
	}
	if openConsumedQty.Cmp(consumedQty) > 0 {
		return nil, true, fmt.Errorf("future reserved margin open consumed quantity %s exceeds total consumed %s", openConsumedQty, consumedQty)
	}
	if remaining.Cmp(baselineReserve) > 0 {
		return nil, true, fmt.Errorf("future reserved margin remaining %s exceeds baseline reserve %s", remaining, baselineReserve)
	}
	if makerPrice.Sign() <= 0 {
		return nil, true, fmt.Errorf("future reserved margin maker price must be positive")
	}
	if !makerLeverageBI.IsUint64() || makerLeverageBI.Sign() <= 0 {
		return nil, true, fmt.Errorf("future reserved margin maker leverage is invalid: %s", makerLeverageBI)
	}
	return &futureReservedMarginAllocation{
		Remaining:       remaining,
		BaselineReserve: baselineReserve,
		BaselineQty:     baselineQty,
		ConsumedQty:     consumedQty,
		OpenConsumedQty: openConsumedQty,
		MakerPrice:      makerPrice,
		MakerLeverage:   makerLeverageBI.Uint64(),
	}, true, nil
}

func futureReservedMarginAmount(data []byte) (*big.Int, error) {
	state, _, err := decodeFutureReservedMarginAllocation(data)
	if err != nil {
		return nil, err
	}
	return new(big.Int).Set(state.Remaining), nil
}

func futureTradeExposureSplit(sv StateView, owner, pair string, side future.Side, qty *big.Int) (closeQty, openQty *big.Int, err error) {
	closeQty = new(big.Int)
	openQty = new(big.Int)
	if qty == nil || qty.Sign() <= 0 {
		return closeQty, openQty, fmt.Errorf("future trade quantity must be positive")
	}
	store := &svPositionStore{sv: sv}
	sameSide, err := store.GetPositionByOwnerAndPairSide(owner, pair, side)
	if err != nil {
		return nil, nil, err
	}
	if sameSide != nil && sameSide.Status == future.StatusOpen {
		return closeQty, new(big.Int).Set(qty), nil
	}
	opposite, err := store.GetPositionByOwnerAndPairSide(owner, pair, side.Opposite())
	if err != nil {
		return nil, nil, err
	}
	if opposite != nil && opposite.Status == future.StatusOpen && opposite.Quantity != nil && opposite.Quantity.Sign() > 0 {
		closeQty.Set(qty)
		if closeQty.Cmp(opposite.Quantity) > 0 {
			closeQty.Set(opposite.Quantity)
		}
	}
	openQty.Sub(qty, closeQty)
	return closeQty, openQty, nil
}

                                                                             
                                                                          
                                                                             
                                                                            
                                                                           
func futureMakerOpenMarginNeeded(
	sv StateView,
	owner, pair string,
	side future.Side,
	openQty, price *big.Int,
	leverage uint64,
) (*big.Int, error) {
	if openQty == nil || openQty.Sign() == 0 {
		return new(big.Int), nil
	}
	if openQty.Sign() < 0 || price == nil || price.Sign() <= 0 || leverage == 0 {
		return nil, fmt.Errorf("invalid future maker open-margin inputs: qty=%v price=%v leverage=%d", openQty, price, leverage)
	}

	store := &svPositionStore{sv: sv}
	existing, err := store.GetPositionByOwnerAndPairSide(owner, pair, side)
	if err != nil {
		return nil, err
	}
	if existing == nil || existing.Status != future.StatusOpen || existing.Quantity == nil || existing.Quantity.Sign() <= 0 {
		return future.CalcRequiredMargin(openQty, price, leverage), nil
	}

	entryNumerator := new(big.Int)
	if existing.EntryNotionalNumerator != nil && existing.EntryNotionalNumerator.Sign() > 0 {
		entryNumerator.Set(existing.EntryNotionalNumerator)
	} else if existing.EntryPrice != nil && existing.Quantity != nil {
		entryNumerator.Mul(existing.EntryPrice, existing.Quantity)
	}
	entryNumerator.Add(entryNumerator, new(big.Int).Mul(openQty, price)) // safe-math-ok: 保留完整开仓名义价值分子，后续除价格精度与杠杆
	eng := newFutureEngine(sv, 0)
	cfg, err := eng.GetMarketConfig(pair)
	if err != nil {
		return nil, fmt.Errorf("load future market config for maker margin: %w", err)
	}
	if cfg == nil {
		return nil, fmt.Errorf("missing future market config for maker margin: pair=%s", pair)
	}
	                                                                           
	                                                                          
	                                                                       
	                                                                             
	                                                                         
	                                                      
	denominator := new(big.Int).Mul(future.PriceScale, new(big.Int).SetUint64(cfg.MaxLeverageOrDefault())) // safe-math-ok: 保证金公式的精度与杠杆分母，不是余额写入
	targetMargin := MulDivCeil(entryNumerator, big.NewInt(1), denominator)

	currentMargin := new(big.Int)
	if existing.Margin != nil {
		currentMargin.Set(existing.Margin)
	}
	                                                                            
	                                                                            
	if pending := eng.PreviewPendingFunding(existing); pending != nil && pending.Sign() < 0 {
		if new(big.Int).Neg(new(big.Int).Set(pending)).Cmp(currentMargin) >= 0 {
			return nil, fmt.Errorf("pending funding makes maker position terminal before fill")
		}
		currentMargin.Add(currentMargin, pending)
	}
	if currentMargin.Cmp(targetMargin) >= 0 {
		return new(big.Int), nil
	}
	return new(big.Int).Sub(targetMargin, currentMargin), nil // safe-math-ok: 已排除 currentMargin >= targetMargin，仅返回正的补仓需求
}

func newFutureReservedMarginAllocation(remaining, baselineQty, price *big.Int, leverage uint64) (*futureReservedMarginAllocation, error) {
	if remaining == nil || remaining.Sign() < 0 {
		return nil, fmt.Errorf("future reserved margin must be non-negative")
	}
	if baselineQty == nil || baselineQty.Sign() <= 0 {
		return nil, fmt.Errorf("future reserved margin baseline quantity must be positive")
	}
	if price == nil || price.Sign() <= 0 {
		return nil, fmt.Errorf("future reserved margin maker price must be positive")
	}
	if leverage == 0 {
		return nil, fmt.Errorf("future reserved margin maker leverage must be positive")
	}
	return &futureReservedMarginAllocation{
		Remaining:       new(big.Int).Set(remaining),
		BaselineReserve: new(big.Int).Set(remaining),
		BaselineQty:     new(big.Int).Set(baselineQty),
		ConsumedQty:     new(big.Int),
		OpenConsumedQty: new(big.Int),
		MakerPrice:      new(big.Int).Set(price),
		MakerLeverage:   leverage,
	}, nil
}

func consumeFutureOrderReservedMargin(
	sv StateView,
	orderID, owner, pair, quoteToken string,
	makerSide future.Side,
	tradeQty, makerRemainBefore, makerOpenQty, makerPrice *big.Int,
	makerLeverage uint64,
	makerFilled bool,
	height uint64,
) (*big.Int, error) {
	key := keys.KeyFutureOrderReservedMargin(orderID)
	data, exists, err := sv.Get(key)
	if err != nil {
		return nil, err
	}
	if tradeQty == nil || tradeQty.Sign() <= 0 {
		return nil, fmt.Errorf("future maker trade quantity must be positive: order=%s", orderID)
	}
	baselineQty := makerRemainBefore
	if baselineQty == nil || baselineQty.Sign() <= 0 {
		baselineQty = tradeQty
	}
	var allocation *futureReservedMarginAllocation
	if !exists {
		                                                                      
		                                                                        
		                                                                       
		                                                       
		derivedRemaining := future.CalcRequiredMargin(baselineQty, makerPrice, makerLeverage)
		allocation, err = newFutureReservedMarginAllocation(derivedRemaining, baselineQty, makerPrice, makerLeverage)
		if err != nil {
			return nil, fmt.Errorf("reconstruct future maker reserved margin: order=%s: %w", orderID, err)
		}
	} else {
		var versioned bool
		allocation, versioned, err = decodeFutureReservedMarginAllocation(data)
		if err != nil {
			return nil, fmt.Errorf("decode future maker reserved margin failed: order=%s: %w", orderID, err)
		}
		if !versioned {
			allocation, err = newFutureReservedMarginAllocation(allocation.Remaining, baselineQty, makerPrice, makerLeverage)
			if err != nil {
				return nil, fmt.Errorf("initialize future maker reserved margin: order=%s: %w", orderID, err)
			}
		}
	}
	if makerPrice == nil || allocation.MakerPrice.Cmp(makerPrice) != 0 {
		return nil, fmt.Errorf("future maker allocation price mismatch: order=%s got=%v want=%s", orderID, makerPrice, allocation.MakerPrice)
	}
	if allocation.MakerLeverage != makerLeverage {
		return nil, fmt.Errorf("future maker allocation leverage mismatch: order=%s got=%d want=%d", orderID, makerLeverage, allocation.MakerLeverage)
	}

	expectedRemain := new(big.Int).Sub(allocation.BaselineQty, allocation.ConsumedQty) // safe-math-ok: 随后拒绝非正剩余量并校验 maker 剩余数量一致
	if expectedRemain.Sign() <= 0 {
		return nil, fmt.Errorf("future maker allocation already exhausted: order=%s", orderID)
	}
	if makerRemainBefore != nil && makerRemainBefore.Sign() > 0 && makerRemainBefore.Cmp(expectedRemain) != 0 {
		return nil, fmt.Errorf("future maker remaining quantity mismatch: order=%s got=%s want=%s", orderID, makerRemainBefore, expectedRemain)
	}
	if tradeQty.Cmp(expectedRemain) > 0 {
		return nil, fmt.Errorf("future maker trade quantity exceeds remaining: order=%s trade=%s remain=%s", orderID, tradeQty, expectedRemain)
	}
	if makerOpenQty == nil || makerOpenQty.Sign() < 0 || makerOpenQty.Cmp(tradeQty) > 0 {
		return nil, fmt.Errorf("future maker open quantity invalid: order=%s open=%v trade=%s", orderID, makerOpenQty, tradeQty)
	}

	nextConsumed := new(big.Int).Add(allocation.ConsumedQty, tradeQty) // safe-math-ok: tradeQty 已限制为不超过 expectedRemain，累计值不超 BaselineQty
	nextOpenConsumed := new(big.Int).Add(allocation.OpenConsumedQty, makerOpenQty) // safe-math-ok: 下一步校验累计开仓量不超累计成交量与 BaselineQty
	if nextOpenConsumed.Cmp(nextConsumed) > 0 || nextOpenConsumed.Cmp(allocation.BaselineQty) > 0 {
		return nil, fmt.Errorf("future maker cumulative open quantity invalid: order=%s open=%s consumed=%s baseline=%s", orderID, nextOpenConsumed, nextConsumed, allocation.BaselineQty)
	}
	consume, err := futureMakerOpenMarginNeeded(sv, owner, pair, makerSide, makerOpenQty, allocation.MakerPrice, allocation.MakerLeverage)
	if err != nil {
		return nil, fmt.Errorf("calculate future maker live margin: order=%s: %w", orderID, err)
	}
	previousOrderTarget := future.CalcRequiredMargin(allocation.OpenConsumedQty, allocation.MakerPrice, allocation.MakerLeverage)
	nextOrderTarget := future.CalcRequiredMargin(nextOpenConsumed, allocation.MakerPrice, allocation.MakerLeverage)
	orderDelta := new(big.Int).Sub(nextOrderTarget, previousOrderTarget) // safe-math-ok: 随后显式拒绝负的累计保证金差额
	if orderDelta.Sign() < 0 {
		return nil, fmt.Errorf("future maker cumulative margin regressed: order=%s previous=%s next=%s", orderID, previousOrderTarget, nextOrderTarget)
	}
	if orderDelta.Cmp(consume) > 0 {
		consume.Set(orderDelta)
	}
	remainAfter := new(big.Int).Sub(allocation.BaselineQty, nextConsumed) // safe-math-ok: 本次成交已限制为不超剩余量，nextConsumed 不超 BaselineQty
	reserveAfter := future.CalcRequiredMargin(remainAfter, allocation.MakerPrice, allocation.MakerLeverage)
	requiredNow := new(big.Int).Add(consume, reserveAfter) // safe-math-ok: 仅计算资金需求，随后要求不超过已预留 Remaining
	if consume.Sign() < 0 || requiredNow.Cmp(allocation.Remaining) > 0 {
		return nil, fmt.Errorf("future maker reserve cannot independently cover fill and remainder: order=%s fill=%s remainder=%s remaining=%s", orderID, consume, reserveAfter, allocation.Remaining)
	}
	if makerFilled && nextConsumed.Cmp(allocation.BaselineQty) != 0 {
		return nil, fmt.Errorf("future maker filled flag before allocation end: order=%s consumed=%s baseline=%s", orderID, nextConsumed, allocation.BaselineQty)
	}
	if !makerFilled && nextConsumed.Cmp(allocation.BaselineQty) == 0 {
		return nil, fmt.Errorf("future maker allocation ended without filled flag: order=%s", orderID)
	}
	if consume.Sign() > 0 {
		if err := NewLedger(sv).Move(MoveRequest{
			Account: owner, Token: quoteToken, Amount: consume,
			From: BucketOrderFrozen, To: BucketMargin,
			Reason: "future_maker_margin_fill",
			Height: height, Ref: orderID,
		}); err != nil {
			return nil, err
		}
	}
	allocation.Remaining.Sub(allocation.Remaining, consume)
	allocation.ConsumedQty.Set(nextConsumed)
	allocation.OpenConsumedQty.Set(nextOpenConsumed)
	if makerFilled {
		if allocation.Remaining.Sign() > 0 {
			if err := NewLedger(sv).Move(MoveRequest{
				Account: owner, Token: quoteToken, Amount: allocation.Remaining,
				From: BucketOrderFrozen, To: BucketAvailable,
				Reason: "future_maker_margin_refund",
				Height: height, Ref: orderID,
			}); err != nil {
				return nil, err
			}
		}
		sv.Del(key)
	} else {
		if allocation.Remaining.Cmp(reserveAfter) < 0 {
			return nil, fmt.Errorf("future maker remainder became under-reserved: order=%s have=%s need=%s", orderID, allocation.Remaining, reserveAfter)
		}
		                                                                       
		                                                                        
		                                                                  
		sv.Set(key, encodeFutureReservedMarginAllocation(allocation))
	}
	return consume, nil
}

                                                                                         
                                                                                     
  
                  
                                                                                                     
                                             
  
               
                                                  
                                                                                                              
                                                                    
  
                                                           
  
                                                                                                              
                                                                                                                  
type FutureMatchEngine struct {
	sv  StateView
	eng *future.Engine
}

                                                                          
                                                                            
                                                                             
                                                 
type futureOrderExecutionMode struct {
	reduceOnlyPositionID string
	immediateOrCancel    bool
	orderAmount          *big.Int
}

func (m futureOrderExecutionMode) isReduceOnly() bool {
	return m.reduceOnlyPositionID != ""
}

                                                                                                     
func NewFutureMatchEngine(sv StateView, height uint64) *FutureMatchEngine {
	return &FutureMatchEngine{sv: sv, eng: newFutureEngineWithActivity(sv, height)}
}

                                                                             
type OpenOrderResult struct {
	MatchedQty      *big.Int
	MatchedQuote    *big.Int
	RemainQty       *big.Int
	RemainingMargin *big.Int
	Status          pb.FutureOrderStatus
	PnLBatch        *future.RealizedPnLBatchResult
	Events          []Event
}

                                                                                                                                    
                                                                                                               
                                                                                                                    
                                                                                                  
func (f *FutureMatchEngine) OpenOrder(
	from, pair, quoteToken string,
	side future.Side,
	qty, price *big.Int,
	leverage uint64,
	isMarket bool,
	maxSlippageBps uint32,
	requiredMargin *big.Int,
	base *pb.BaseMessage,
) (*OpenOrderResult, error) {
	return f.executeOrder(
		from, pair, quoteToken, side, qty, price, leverage, isMarket,
		maxSlippageBps, requiredMargin, base, futureOrderExecutionMode{},
	)
}

func (f *FutureMatchEngine) executeOrder(
	from, pair, quoteToken string,
	side future.Side,
	qty, price *big.Int,
	leverage uint64,
	isMarket bool,
	maxSlippageBps uint32,
	requiredMargin *big.Int,
	base *pb.BaseMessage,
	mode futureOrderExecutionMode,
) (res *OpenOrderResult, err error) {
	snapshot := f.sv.Snapshot()
	defer func() {
		if err != nil {
			_ = f.sv.Revert(snapshot)
		}
	}()

	obSide := pb.OrderSide_BUY
	if side == future.Short {
		obSide = pb.OrderSide_SELL
	}
	var trades []TradeResult
	if mode.isReduceOnly() {
		trades, err = MatchFutureReduceOnlyOrderAtHeight(f.sv, pair, obSide, qty, price, isMarket, maxSlippageBps, from, base.ExecutedHeight)
	} else {
		trades, err = MatchFutureOrderAtHeight(f.sv, pair, obSide, qty, price, isMarket, maxSlippageBps, from, base.ExecutedHeight)
	}
	if err != nil {
		return nil, fmt.Errorf("matching failed: %w", err)
	}

	matchedQty := new(big.Int)
	matchedQuote := new(big.Int)
	matchedMarginNumerator := new(big.Int)
	usedMargin := new(big.Int)
	fundedMargin := new(big.Int)
	if requiredMargin != nil {
		fundedMargin.Set(requiredMargin)
	}
	ensureMarginFunded := func(target *big.Int) error {
		if target == nil || target.Cmp(fundedMargin) <= 0 {
			return nil
		}
		extra := new(big.Int).Sub(target, fundedMargin) // safe-math-ok: 仅 target > fundedMargin 分支执行，实际补款经过 Ledger.Move
		if err := NewLedger(f.sv).Move(MoveRequest{
			Account: from, Token: quoteToken, Amount: extra,
			From: BucketAvailable, To: BucketMargin,
			Reason: "future_order_margin_topup",
			Height: base.ExecutedHeight, TxID: base.TxId, Ref: base.TxId,
		}); err != nil {
			return fmt.Errorf("insufficient margin topup: %w", err)
		}
		fundedMargin.Add(fundedMargin, extra)
		return nil
	}
	events := make([]Event, 0, len(trades)*3)
	refEng := newReferralEngine(f.sv, base.ExecutedHeight)
	feeSeq := 0
	type pendingFutureFee struct {
		addr     string
		role     referral.Role
		tag      string
		notional *big.Int
	}
	pendingFees := make([]pendingFutureFee, 0, len(trades)*2)
	if err := f.eng.BeginRealizedPnLBatch(); err != nil {
		return nil, fmt.Errorf("begin realized PnL batch: %w", err)
	}
	batchActive := true
	defer func() {
		if batchActive {
			f.eng.AbortRealizedPnLBatch()
		}
	}()

	                                                                        
	                                                                        
	                                                                      
	                                                                        
	                                    
	takerCloseRemaining := new(big.Int)
	positionStore := &svPositionStore{sv: f.sv}
	if mode.isReduceOnly() {
		takerCloseRemaining.Set(qty)
	} else {
		sameSidePosition, positionErr := positionStore.GetPositionByOwnerAndPairSide(from, pair, side)
		if positionErr != nil {
			return nil, fmt.Errorf("load taker same-side position: %w", positionErr)
		}
		if sameSidePosition == nil || sameSidePosition.Status != future.StatusOpen {
			oppositePosition, oppositeErr := positionStore.GetPositionByOwnerAndPairSide(from, pair, side.Opposite())
			if oppositeErr != nil {
				return nil, fmt.Errorf("load taker opposite position: %w", oppositeErr)
			}
			if oppositePosition != nil && oppositePosition.Status == future.StatusOpen && oppositePosition.Quantity != nil {
				takerCloseRemaining.Set(oppositePosition.Quantity)
			}
		}
	}

	for i, tr := range trades {
		matchedQty.Add(matchedQty, tr.Quantity)
		tradeNotional := future.CalcNotional(tr.Quantity, tr.Price)
		matchedQuote.Add(matchedQuote, tradeNotional)
		takerCloseQty := new(big.Int)
		if takerCloseRemaining.Sign() > 0 {
			takerCloseQty.Set(tr.Quantity)
			if takerCloseQty.Cmp(takerCloseRemaining) > 0 {
				takerCloseQty.Set(takerCloseRemaining)
			}
			takerCloseRemaining.Sub(takerCloseRemaining, takerCloseQty)
		}
		takerOpenQty := new(big.Int).Sub(tr.Quantity, takerCloseQty) // safe-math-ok: 平仓份额已取本次成交量与待平仓量的较小值
		if mode.isReduceOnly() && takerOpenQty.Sign() > 0 {
			return nil, fmt.Errorf("reduce-only order would open reverse exposure: position=%s quantity=%s", mode.reduceOnlyPositionID, takerOpenQty)
		}
		if takerOpenQty.Sign() > 0 {
			matchedMarginNumerator.Add(matchedMarginNumerator, new(big.Int).Mul(takerOpenQty, tr.Price)) // safe-math-ok: 累计真实成交名义价值分子，保持跨成交取整一致
		}

		portionMargin := new(big.Int)
		if takerOpenQty.Sign() > 0 {
			                                                                                             
			if leverage == 0 {
				return nil, fmt.Errorf("future trade leverage must be positive")
			}
			                                                                    
			                                                                       
			                                                        
			marginDenominator := new(big.Int).Mul(future.PriceScale, new(big.Int).SetUint64(leverage)) // safe-math-ok: 保证金公式分母，leverage 已确认大于零
			cumulativeRequired := MulDivCeil(matchedMarginNumerator, big.NewInt(1), marginDenominator)
			portionMargin.Sub(cumulativeRequired, usedMargin)
			if portionMargin.Sign() < 0 {
				return nil, fmt.Errorf("future cumulative margin regressed: required=%s used=%s", cumulativeRequired, usedMargin)
			}
			if err := ensureMarginFunded(cumulativeRequired); err != nil {
				return nil, err
			}
			usedMargin.Set(cumulativeRequired)
		}
		                               
		                                                                                                       
		                                                                                                    
		                                                                                                            
		makerSide := future.Side(tr.MakerSide)
		if tr.MakerOwner != "" && tr.MakerLeverage == 0 {
			return nil, fmt.Errorf("future maker order missing leverage: order=%s", tr.MakerOrderID)
		}
		processTaker := func() error {
			if takerCloseQty.Sign() > 0 {
				if err := f.eng.ProcessTradeForPosition(from, pair, side, takerCloseQty, tr.Price, new(big.Int), base.ExecutedHeight); err != nil {
					return fmt.Errorf("taker close segment ProcessTradeForPosition failed: %w", err)
				}
			}
			if takerOpenQty.Sign() > 0 {
				if err := f.eng.ProcessTradeForPosition(from, pair, side, takerOpenQty, tr.Price, portionMargin, base.ExecutedHeight); err != nil {
					return fmt.Errorf("taker open segment ProcessTradeForPosition failed: %w", err)
				}
			}
			return nil
		}
		processMaker := func() error {
			if tr.MakerOwner == "" {
				return nil
			}
			makerCloseQty, makerOpenQty, err := futureTradeExposureSplit(f.sv, tr.MakerOwner, pair, makerSide, tr.Quantity)
			if err != nil {
				return fmt.Errorf("split maker close/open exposure failed: %w", err)
			}
			makerPortion, err := consumeFutureOrderReservedMargin(
				f.sv, tr.MakerOrderID, tr.MakerOwner, pair, quoteToken, makerSide,
				tr.Quantity, tr.MakerRemainBefore, makerOpenQty, tr.Price, tr.MakerLeverage,
				tr.MakerFilled, base.ExecutedHeight,
			)
			if err != nil {
				return fmt.Errorf("consume maker reserved margin failed: %w", err)
			}
			if makerCloseQty.Sign() > 0 {
				if err := f.eng.ProcessTradeForPosition(tr.MakerOwner, pair, makerSide, makerCloseQty, tr.Price, new(big.Int), base.ExecutedHeight); err != nil {
					return fmt.Errorf("maker close segment ProcessTradeForPosition failed: %w", err)
				}
			}
			if makerOpenQty.Sign() > 0 {
				if err := f.eng.ProcessTradeForPosition(tr.MakerOwner, pair, makerSide, makerOpenQty, tr.Price, makerPortion, base.ExecutedHeight); err != nil {
					return fmt.Errorf("maker open segment ProcessTradeForPosition failed: %w", err)
				}
			}
			return nil
		}

		                                                                    
		                                                                        
		                                              
		if err := processTaker(); err != nil {
			return nil, err
		}
		if err := processMaker(); err != nil {
			return nil, err
		}
		events = append(events, Event{
			Kind: EventOrderMatched,
			Payload: map[string]string{
				"pair":           pair,
				"taker":          from,
				"maker":          tr.MakerOwner,
				"maker_order_id": tr.MakerOrderID,
				"quantity":       tr.Quantity.String(),
				"price":          tr.Price.String(),
				"seq":            strconv.Itoa(i),
				"is_close":       strconv.FormatBool(mode.isReduceOnly()),
				"close_position": mode.reduceOnlyPositionID,
			},
		})

		                                                                        
		                                                            
		pendingFees = append(pendingFees,
			pendingFutureFee{addr: from, role: referral.RoleTaker, tag: "taker", notional: new(big.Int).Set(tradeNotional)},
			pendingFutureFee{addr: tr.MakerOwner, role: referral.RoleMaker, tag: "maker", notional: new(big.Int).Set(tradeNotional)},
		)

		               
		tradeTsKey, tradeTsText := deterministicTradeTime(base.ExecutedHeight, i)
		tradeRecord := &pb.TradeRecord{
			TradeId:      fmt.Sprintf("%s_%d", base.TxId, i),
			Pair:         pair,
			Price:        tr.Price.String(),
			Amount:       tr.Quantity.String(),
			TakerSide:    obSide,
			MakerOrderId: tr.MakerOrderID,
			TakerOrderId: base.TxId,
			Timestamp:    tradeTsText,
			Height:       base.ExecutedHeight,
		}
		if tradeData, err := proto.Marshal(tradeRecord); err == nil {
			f.sv.Set(keys.KeyFutureTradeRecord(pair, tradeTsKey, tradeRecord.TradeId), tradeData)
		}
	}

	pnlBatch, err := f.eng.CommitRealizedPnLBatch()
	if err != nil {
		return nil, fmt.Errorf("commit realized PnL batch: %w", err)
	}
	batchActive = false
	for _, pending := range pendingFees {
		if pending.addr == "" {
			continue
		}
		feeResult, feeErr := refEng.CalcAndDistributeFeeWithRole(pending.addr, pair, quoteToken, pending.notional, pending.role)
		if feeErr != nil {
			return nil, fmt.Errorf("future trading fee failed role=%s owner=%s: %w", pending.tag, pending.addr, feeErr)
		}
		if feeResult == nil {
			continue
		}
		writeTradingFeeRecord(f.sv, feeResult, pair, pending.notional, base.ExecutedHeight, base.TxId, feeSeq)
		feeSeq++
		events = append(events, Event{
			Kind: EventFeeCharged,
			Payload: map[string]string{
				"pair":     pair,
				"owner":    pending.addr,
				"token":    quoteToken,
				"notional": pending.notional.String(),
				"role":     pending.tag,
				"fee":      feeResult.ActualFee.String(),
				"is_close": strconv.FormatBool(mode.isReduceOnly()),
			},
		})
	}

	remainQty := new(big.Int).Sub(qty, matchedQty) // safe-math-ok: 撮合剩余数量，后续仅正数分支可形成挂单或退款
	if !isMarket && !mode.immediateOrCancel && remainQty.Sign() > 0 {
		if leverage == 0 {
			return nil, fmt.Errorf("future order leverage must be positive")
		}
		                                                                       
		                                                                        
		                                                                       
		                                                                           
		                                                                        
		                                                                           
		                                                                 
		restingRequired := future.CalcRequiredMargin(remainQty, price, leverage)
		independentTarget := new(big.Int).Add(usedMargin, restingRequired) // safe-math-ok: 需求汇总不直接写余额，ensureMarginFunded 经 Ledger 校验扣款
		if err := ensureMarginFunded(independentTarget); err != nil {
			return nil, err
		}
	}
	remainingMargin := new(big.Int).Sub(fundedMargin, usedMargin) // safe-math-ok: 已拨付与已使用保证金差值，随后显式处理负值
	if remainingMargin.Sign() < 0 {
		remainingMargin = new(big.Int)
	}
	isFilled := remainQty.Sign() <= 0
	status := pb.FutureOrderStatus_FUTURE_ORDER_OPEN
	if mode.isReduceOnly() {
		status = pb.FutureOrderStatus_FUTURE_ORDER_CLOSED
	} else if isFilled {
		status = pb.FutureOrderStatus_FUTURE_ORDER_FILLED
	} else if isMarket || mode.immediateOrCancel {
		status = pb.FutureOrderStatus_FUTURE_ORDER_CANCELLED
	}
	orderAmount := qty
	if mode.orderAmount != nil {
		orderAmount = mode.orderAmount
	}
	orderState := &pb.FutureOrderState{
		OrderId: base.TxId, Owner: from, Pair: pair,
		Side: int32(side), Price: price.String(),
		Amount: orderAmount.String(), FilledBase: matchedQty.String(),
		FilledQuote: matchedQuote.String(),
		Leverage:    leverage, Status: status,
		OwnerAddress: from, CreateHeight: base.ExecutedHeight,
		IsClose: mode.isReduceOnly(), ClosePositionId: mode.reduceOnlyPositionID,
	}
	if err := writeFutureOrderSnapshot(f.sv, orderState); err != nil {
		return nil, fmt.Errorf("write order state: %w", err)
	}

	return &OpenOrderResult{
		MatchedQty:      matchedQty,
		MatchedQuote:    matchedQuote,
		RemainQty:       remainQty,
		RemainingMargin: remainingMargin,
		Status:          status,
		PnLBatch:        pnlBatch,
		Events:          events,
	}, nil
}

                                         
type CloseOrderResult struct {
	AveragePrice *big.Int
	RequestedQty *big.Int
	EffectiveQty *big.Int
	MatchedQty   *big.Int
	RemainQty    *big.Int
	Notional     *big.Int
	RealizedPnL  *big.Int
	PnLShortfall *big.Int
	FullyClosed  bool
	FeePaid      bool
	Status       pb.FutureOrderStatus
	Events       []Event
}

                                                                            
                                                                             
                                                                              
                                 
func (f *FutureMatchEngine) CloseOrder(
	from, pair, quoteToken string,
	side future.Side,
	qty, price *big.Int,
	leverage uint64,
	isMarket bool,
	maxSlippageBps uint32,
	closePositionID string,
	base *pb.BaseMessage,
) (res *CloseOrderResult, err error) {
	snapshot := f.sv.Snapshot()
	defer func() {
		if err != nil {
			_ = f.sv.Revert(snapshot)
		}
	}()

	if closePositionID == "" {
		return nil, fmt.Errorf("close_position_id is required")
	}
	if base == nil {
		return nil, fmt.Errorf("close order base message is required")
	}
	if price == nil || price.Sign() <= 0 {
		return nil, fmt.Errorf("close order price/reference price must be positive")
	}
	positionStore := &svPositionStore{sv: f.sv}
	pos, err := positionStore.GetPosition(closePositionID)
	if err != nil {
		return nil, err
	}
	if pos == nil || pos.Status != future.StatusOpen {
		return nil, fmt.Errorf("position not found or not open: %s", closePositionID)
	}
	if pos.Owner != from {
		return nil, fmt.Errorf("position owner mismatch: position=%s owner=%s tx_from=%s", closePositionID, pos.Owner, from)
	}
	if pos.Pair != pair {
		return nil, fmt.Errorf("position pair mismatch: position=%s pair=%s tx_pair=%s", closePositionID, pos.Pair, pair)
	}
	if pos.Side.Opposite() != side {
		return nil, fmt.Errorf("close side must be opposite to position side: position=%s pos_side=%s close_side=%s", closePositionID, pos.Side.String(), side.String())
	}
	if qty == nil || qty.Sign() <= 0 {
		return nil, fmt.Errorf("close quantity must be positive")
	}
	if pos.Quantity == nil || pos.Quantity.Sign() <= 0 {
		return nil, fmt.Errorf("open position has invalid quantity: %s", closePositionID)
	}
	indexed, err := positionStore.GetPositionByOwnerAndPairSide(from, pair, pos.Side)
	if err != nil {
		return nil, fmt.Errorf("load indexed close position: %w", err)
	}
	if indexed == nil || indexed.Status != future.StatusOpen || indexed.ID != closePositionID {
		indexedID := ""
		if indexed != nil {
			indexedID = indexed.ID
		}
		return nil, fmt.Errorf("close position is not the active indexed position: requested=%s indexed=%s", closePositionID, indexedID)
	}

	                                                                         
	                                                             
	effectiveQty := new(big.Int).Set(qty)
	if effectiveQty.Cmp(pos.Quantity) > 0 {
		effectiveQty.Set(pos.Quantity)
	}

	orderRes, err := f.executeOrder(
		from, pair, quoteToken, side, effectiveQty, price, leverage, isMarket,
		maxSlippageBps, new(big.Int), base,
		futureOrderExecutionMode{reduceOnlyPositionID: closePositionID, immediateOrCancel: true, orderAmount: qty},
	)
	if err != nil {
		return nil, err
	}

	averagePrice := new(big.Int)
	if orderRes.MatchedQty.Sign() > 0 {
		averagePrice.Mul(orderRes.MatchedQuote, future.PriceScale)
		averagePrice.Quo(averagePrice, orderRes.MatchedQty)
	}
	realizedPnL := orderRes.PnLBatch.ActualPnLForPosition(closePositionID)
	pnlShortfall := orderRes.PnLBatch.ShortfallForPosition(closePositionID)
	requestedRemain := new(big.Int).Sub(qty, orderRes.MatchedQty) // safe-math-ok: 只用于核对撮合后剩余量，不直接增加资金

	events := append([]Event(nil), orderRes.Events...)
	fullyClosed := false
	if orderRes.MatchedQty.Sign() > 0 {
		positionEvent := EventPositionReduced
		remainingPositionQty := new(big.Int)
		after, loadErr := positionStore.GetPosition(closePositionID)
		if loadErr != nil {
			return nil, fmt.Errorf("load position after close: %w", loadErr)
		}
		if after == nil || after.Status != future.StatusOpen {
			positionEvent = EventPositionClosed
			fullyClosed = true
		} else if after.Quantity != nil {
			remainingPositionQty.Set(after.Quantity)
		}
		events = append(events, Event{
			Kind: positionEvent,
			Payload: map[string]string{
				"pair":               pair,
				"owner":              from,
				"side":               side.String(),
				"quantity":           orderRes.MatchedQty.String(),
				"requested_quantity": qty.String(),
				"effective_quantity": effectiveQty.String(),
				"unfilled_quantity":  requestedRemain.String(),
				"remaining_quantity": remainingPositionQty.String(),
				"average_price":      averagePrice.String(),
				"close_position":     closePositionID,
				"notional":           orderRes.MatchedQuote.String(),
				"realized_pnl":       realizedPnL.String(),
				"pnl_shortfall":      pnlShortfall.String(),
				"fully_closed":       strconv.FormatBool(fullyClosed),
			},
		})
	}
	if requestedRemain.Sign() > 0 {
		events = append(events, Event{
			Kind: EventOrderCancelled,
			Payload: map[string]string{
				"pair":               pair,
				"owner":              from,
				"order_id":           base.TxId,
				"close_position":     closePositionID,
				"requested_quantity": qty.String(),
				"effective_quantity": effectiveQty.String(),
				"matched_quantity":   orderRes.MatchedQty.String(),
				"matched_quote":      orderRes.MatchedQuote.String(),
				"unfilled_quantity":  requestedRemain.String(),
				"realized_pnl":       realizedPnL.String(),
				"pnl_shortfall":      pnlShortfall.String(),
				"fully_closed":       strconv.FormatBool(fullyClosed),
				"reason":             "ioc_remainder",
				"is_close":           "true",
			},
		})
	}

	feePaid := false
	for _, event := range orderRes.Events {
		if event.Kind == EventFeeCharged && event.Payload["owner"] == from && event.Payload["role"] == "taker" {
			feePaid = true
			break
		}
	}

	return &CloseOrderResult{
		AveragePrice: averagePrice,
		RequestedQty: new(big.Int).Set(qty),
		EffectiveQty: new(big.Int).Set(effectiveQty),
		MatchedQty:   new(big.Int).Set(orderRes.MatchedQty),
		RemainQty:    requestedRemain,
		Notional:     new(big.Int).Set(orderRes.MatchedQuote),
		RealizedPnL:  realizedPnL,
		PnLShortfall: pnlShortfall,
		FullyClosed:  fullyClosed,
		FeePaid:      feePaid,
		Status:       orderRes.Status,
		Events:       events,
	}, nil
}
