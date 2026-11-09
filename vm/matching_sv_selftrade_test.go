package vm

import (
	"dex/db"
	"dex/future"
	"dex/keys"
	"dex/pb"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
)

                                                        
                                                                              
                                                                                                                        

const selfTradePair = "BTC_USDT"

func placeSpotMaker(t *testing.T, sv StateView, orderID, owner string, side pb.OrderSide, price, amount *big.Int) {
	t.Helper()
	os := &pb.OrderState{
		OrderId:    orderID,
		Owner:      owner,
		FilledBase: "0", FilledQuote: "0",
		IsFilled: false, Status: pb.OrderStateStatus_ORDER_OPEN,
		BaseToken: "BTC", QuoteToken: "USDT",
		Amount: amount.String(), Price: price.String(), Side: side,
	}
	data, err := proto.Marshal(os)
	if err != nil {
		t.Fatalf("marshal order state: %v", err)
	}
	sv.Set(keys.KeyOrderState(orderID), data)
	priceKey, err := db.PriceToKey128(price.String())
	if err != nil {
		t.Fatalf("price key: %v", err)
	}
	sv.Set(keys.KeyOrderPriceIndex(selfTradePair, side, false, priceKey, orderID), data)
}

func putSpotMakerRaw(t *testing.T, store map[string][]byte, orderID, owner string, side pb.OrderSide, price, amount *big.Int) {
	t.Helper()
	os := &pb.OrderState{
		OrderId:    orderID,
		Owner:      owner,
		FilledBase: "0", FilledQuote: "0",
		IsFilled: false, Status: pb.OrderStateStatus_ORDER_OPEN,
		BaseToken: "BTC", QuoteToken: "USDT",
		Amount: amount.String(), Price: price.String(), Side: side,
	}
	data, err := proto.Marshal(os)
	if err != nil {
		t.Fatalf("marshal order state: %v", err)
	}
	store[keys.KeyOrderState(orderID)] = data
	priceKey, err := db.PriceToKey128(price.String())
	if err != nil {
		t.Fatalf("price key: %v", err)
	}
	store[keys.KeyOrderPriceIndex(selfTradePair, side, false, priceKey, orderID)] = []byte{1}
}

func newPagedMemStateView(store map[string][]byte) StateView {
	read := func(key string) ([]byte, error) {
		if v, ok := store[key]; ok {
			cp := append([]byte(nil), v...)
			return cp, nil
		}
		return nil, nil
	}
	scanLimit := func(prefix string, limit int) (map[string][]byte, error) {
		keysOut := make([]string, 0)
		for k := range store {
			if strings.HasPrefix(k, prefix) {
				keysOut = append(keysOut, k)
			}
		}
		sort.Strings(keysOut)
		if limit > 0 && len(keysOut) > limit {
			keysOut = keysOut[:limit]
		}
		out := make(map[string][]byte, len(keysOut))
		for _, k := range keysOut {
			out[k] = append([]byte(nil), store[k]...)
		}
		return out, nil
	}
	scanLimitReverse := func(prefix string, limit int) (map[string][]byte, error) {
		out, err := scanLimit(prefix, 0)
		if err != nil {
			return nil, err
		}
		keysOut := make([]string, 0, len(out))
		for k := range out {
			keysOut = append(keysOut, k)
		}
		sort.Sort(sort.Reverse(sort.StringSlice(keysOut)))
		if limit > 0 && len(keysOut) > limit {
			keysOut = keysOut[:limit]
		}
		limited := make(map[string][]byte, len(keysOut))
		for _, k := range keysOut {
			limited[k] = out[k]
		}
		return limited, nil
	}
	scan := func(prefix string) (map[string][]byte, error) { return scanLimit(prefix, 0) }
	return NewStateViewWithLimitScan(read, scan, scanLimit, scanLimitReverse)
}

func placeFutureMaker(t *testing.T, sv StateView, orderID, owner string, side pb.OrderSide, price, amount *big.Int) {
	placeFutureMakerAtHeight(t, sv, orderID, owner, side, price, amount, 1)
}

func placeFutureMakerAtHeight(t *testing.T, sv StateView, orderID, owner string, side pb.OrderSide, price, amount *big.Int, createHeight uint64) {
	t.Helper()
	fos := &pb.FutureOrderState{
		OrderId: orderID, Owner: owner, Pair: selfTradePair,
		Side: int32(side), Price: price.String(), Amount: amount.String(),
		FilledBase: "0", FilledQuote: "0",
		Leverage: 10, Status: pb.FutureOrderStatus_FUTURE_ORDER_OPEN,
		OwnerAddress: owner, CreateHeight: createHeight,
	}
	data, err := proto.Marshal(fos)
	if err != nil {
		t.Fatalf("marshal future order state: %v", err)
	}
	sv.Set(keys.KeyFutureOrderState(orderID), data)
	priceKey, err := db.PriceToKey128(price.String())
	if err != nil {
		t.Fatalf("price key: %v", err)
	}
	sv.Set(keys.KeyFutureOrderPriceIndex(selfTradePair, side, false, priceKey, orderID), data)
	required := future.CalcRequiredMargin(amount, price, fos.Leverage)
	bal := GetBalance(sv, owner, "USDT")
	frozen := parseBigOrZero(bal.OrderFrozenBalance)
	bal.OrderFrozenBalance = new(big.Int).Add(frozen, required).String()
	SetBalance(sv, owner, "USDT", bal)
}

func assertFutureMakerOpen(t *testing.T, sv StateView, orderID string, side pb.OrderSide, price *big.Int, wantFilled string) {
	t.Helper()
	data, exists, err := sv.Get(keys.KeyFutureOrderState(orderID))
	if err != nil || !exists {
		t.Fatalf("future maker state %s missing: exists=%v err=%v", orderID, exists, err)
	}
	var state pb.FutureOrderState
	if err := proto.Unmarshal(data, &state); err != nil {
		t.Fatalf("decode future maker %s: %v", orderID, err)
	}
	if state.Status != pb.FutureOrderStatus_FUTURE_ORDER_OPEN || state.FilledBase != wantFilled {
		t.Fatalf("future maker %s changed unexpectedly: status=%s filled=%s want_filled=%s", orderID, state.Status, state.FilledBase, wantFilled)
	}
	priceKey, err := db.PriceToKey128(price.String())
	if err != nil {
		t.Fatalf("price key: %v", err)
	}
	indexKey := keys.KeyFutureOrderPriceIndex(selfTradePair, side, false, priceKey, orderID)
	if _, indexExists, err := sv.Get(indexKey); err != nil || !indexExists {
		t.Fatalf("future maker index %s missing: exists=%v err=%v", orderID, indexExists, err)
	}
}

                                                                                  
func TestMatchSpotOrder_AllowsSelfTrade(t *testing.T) {
	sv := newMemStateView()
	                                                                                                      
	price := big.NewInt(100_000_000)
	qty := big.NewInt(10)
	placeSpotMaker(t, sv, "alice_sell", "alice", pb.OrderSide_SELL, price, qty)

	trades, err := MatchSpotOrder(sv, selfTradePair, pb.OrderSide_BUY, qty, price, false, 0, "alice")
	if err != nil {
		t.Fatalf("MatchSpotOrder err: %v", err)
	}
	if len(trades) != 1 || trades[0].MakerOrderID != "alice_sell" {
		t.Fatalf("self trade should match alice_sell, got %+v", trades)
	}
}

                                                                             
func TestMatchSpotOrder_AllowsCrossOwnerTrade(t *testing.T) {
	sv := newMemStateView()
	                                                                                                      
	price := big.NewInt(100_000_000)
	qty := big.NewInt(10)
	placeSpotMaker(t, sv, "alice_sell", "alice", pb.OrderSide_SELL, price, qty)

	trades, err := MatchSpotOrder(sv, selfTradePair, pb.OrderSide_BUY, qty, price, false, 0, "bob")
	if err != nil {
		t.Fatalf("MatchSpotOrder err: %v", err)
	}
	if len(trades) != 1 || trades[0].MakerOrderID != "alice_sell" {
		t.Fatalf("expect alice_sell matched, got %+v", trades)
	}
	if trades[0].Quantity.Cmp(qty) != 0 {
		t.Fatalf("qty mismatch: got %s want %s", trades[0].Quantity, qty)
	}
}

                                             
func TestMatchSpotOrder_DustBatchDoesNotBlockLaterLiquidity(t *testing.T) {
	store := make(map[string][]byte)
	for i := 0; i < 200; i++ {
		putSpotMakerRaw(t, store, fmt.Sprintf("dust_%03d", i), "dust", pb.OrderSide_SELL, big.NewInt(1), big.NewInt(1))
	}
	price := big.NewInt(100_000_000)
	qty := big.NewInt(100_000_000)
	putSpotMakerRaw(t, store, "valid_sell", "alice", pb.OrderSide_SELL, price, qty)

	trades, err := MatchSpotOrder(newPagedMemStateView(store), selfTradePair, pb.OrderSide_BUY, qty, price, false, 0, "bob")
	if err != nil {
		t.Fatalf("MatchSpotOrder err: %v", err)
	}
	if len(trades) != 1 || trades[0].MakerOrderID != "valid_sell" {
		t.Fatalf("dust batch should not block valid liquidity, got %+v", trades)
	}
}

func TestMatchSpotOrder_DustTakerDoesNotDeleteValidMakerIndex(t *testing.T) {
	sv := newMemStateView()
	price := big.NewInt(1)
	makerQty := new(big.Int).Set(future.PriceScale)
	takerQty := big.NewInt(1)
	placeSpotMaker(t, sv, "valid_sell", "alice", pb.OrderSide_SELL, price, makerQty)
	priceKey, err := db.PriceToKey128(price.String())
	if err != nil {
		t.Fatalf("price key: %v", err)
	}
	indexKey := keys.KeyOrderPriceIndex(selfTradePair, pb.OrderSide_SELL, false, priceKey, "valid_sell")

	trades, err := MatchSpotOrder(sv, selfTradePair, pb.OrderSide_BUY, takerQty, price, false, 0, "bob")
	if err != nil {
		t.Fatalf("MatchSpotOrder err: %v", err)
	}
	if len(trades) != 0 {
		t.Fatalf("dust taker should not trade, got %+v", trades)
	}
	if _, exists, _ := sv.Get(indexKey); !exists {
		t.Fatalf("dust taker must not delete maker index %s", indexKey)
	}
}

func TestMatchFutureOrder_AllowsSelfTrade(t *testing.T) {
	sv := newMemStateView()
	                                                                                                      
	price := big.NewInt(100_000_000)
	qty := big.NewInt(10)
	placeFutureMaker(t, sv, "alice_sell", "alice", pb.OrderSide_SELL, price, qty)

	trades, err := MatchFutureOrder(sv, selfTradePair, pb.OrderSide_BUY, qty, price, false, 0, "alice")
	if err != nil {
		t.Fatalf("MatchFutureOrder err: %v", err)
	}
	if len(trades) != 1 || trades[0].MakerOrderID != "alice_sell" {
		t.Fatalf("self trade should match alice_sell, got %+v", trades)
	}
}

                                                                                                                
func TestMatchFutureOrderForLiquidation_SkipsSelfTrade(t *testing.T) {
	sv := newMemStateView()
	                                                                                                      
	price := big.NewInt(100_000_000)
	qty := big.NewInt(10)
	placeFutureMaker(t, sv, "alice_sell", "alice", pb.OrderSide_SELL, price, qty)

	trades, err := MatchFutureOrderForLiquidation(sv, selfTradePair, pb.OrderSide_BUY, qty, price, false, 0, "alice")
	if err != nil {
		t.Fatalf("MatchFutureOrderForLiquidation err: %v", err)
	}
	if len(trades) != 0 {
		t.Fatalf("liquidation must skip self maker, got %+v", trades)
	}
}

func TestMatchFutureOrderForLiquidationAtHeight_IgnoresCurrentHeightMakerAndKeepsItOpen(t *testing.T) {
	sv := newMemStateView()
	const height uint64 = 10
	price := big.NewInt(100_000_000)
	qty := big.NewInt(10)
	placeFutureMakerAtHeight(t, sv, "current_sell", "maker", pb.OrderSide_SELL, price, qty, height)

	trades, err := MatchFutureOrderForLiquidationAtHeight(
		sv, selfTradePair, pb.OrderSide_BUY, qty, price, true, 0, "liquidated", height,
	)
	if err != nil {
		t.Fatalf("MatchFutureOrderForLiquidationAtHeight err: %v", err)
	}
	if len(trades) != 0 {
		t.Fatalf("current-height maker must be ignored, got %+v", trades)
	}
	assertFutureMakerOpen(t, sv, "current_sell", pb.OrderSide_SELL, price, "0")
}

func TestMatchFutureOrderForLiquidationAtHeight_AcceptsPriorHeightMaker(t *testing.T) {
	sv := newMemStateView()
	const height uint64 = 10
	price := big.NewInt(100_000_000)
	qty := big.NewInt(10)
	placeFutureMakerAtHeight(t, sv, "prior_sell", "maker", pb.OrderSide_SELL, price, qty, height-1)

	trades, err := MatchFutureOrderForLiquidationAtHeight(
		sv, selfTradePair, pb.OrderSide_BUY, qty, price, true, 0, "liquidated", height,
	)
	if err != nil {
		t.Fatalf("MatchFutureOrderForLiquidationAtHeight err: %v", err)
	}
	if len(trades) != 1 || trades[0].MakerOrderID != "prior_sell" || trades[0].Quantity.Cmp(qty) != 0 {
		t.Fatalf("prior-height maker should match, got %+v", trades)
	}
}

func TestMatchFutureOrderForLiquidationAtHeight_SkipsNewBestAndContinuesToOldWorsePrice(t *testing.T) {
	sv := newMemStateView()
	const height uint64 = 10
	newPrice := big.NewInt(100_000_000)
	oldPrice := big.NewInt(110_000_000)
	qty := big.NewInt(10)
	                                                                         
	                                                                            
	                                                                     
	for i := 0; i < 200; i++ {
		placeFutureMakerAtHeight(t, sv, fmt.Sprintf("new_best_sell_%03d", i), fmt.Sprintf("new-maker-%03d", i), pb.OrderSide_SELL, newPrice, qty, height)
	}
	placeFutureMakerAtHeight(t, sv, "old_worse_sell", "old-maker", pb.OrderSide_SELL, oldPrice, qty, height-1)

	trades, err := MatchFutureOrderForLiquidationAtHeight(
		sv, selfTradePair, pb.OrderSide_BUY, qty, oldPrice, true, 0, "liquidated", height,
	)
	if err != nil {
		t.Fatalf("MatchFutureOrderForLiquidationAtHeight err: %v", err)
	}
	if len(trades) != 1 || trades[0].MakerOrderID != "old_worse_sell" {
		t.Fatalf("liquidation should skip new best and consume old worse maker, got %+v", trades)
	}
	assertFutureMakerOpen(t, sv, "new_best_sell_000", pb.OrderSide_SELL, newPrice, "0")
}

func TestMatchFutureOrderForLiquidationAtHeight_PriorMakerPartialFillRemainsEligible(t *testing.T) {
	sv := newMemStateView()
	const height uint64 = 10
	price := big.NewInt(100_000_000)
	                                                                           
	                                                                        
	                         
	placeFutureMakerAtHeight(t, sv, "old_partial_sell", "maker", pb.OrderSide_SELL, price, big.NewInt(100), height-1)

	first, err := MatchFutureOrderForLiquidationAtHeight(
		sv, selfTradePair, pb.OrderSide_BUY, big.NewInt(40), price, true, 0, "liquidated-a", height,
	)
	if err != nil || len(first) != 1 || first[0].MakerOrderID != "old_partial_sell" {
		t.Fatalf("first partial liquidation fill failed: trades=%+v err=%v", first, err)
	}
	assertFutureMakerOpen(t, sv, "old_partial_sell", pb.OrderSide_SELL, price, "40")

	second, err := MatchFutureOrderForLiquidationAtHeight(
		sv, selfTradePair, pb.OrderSide_BUY, big.NewInt(60), price, true, 0, "liquidated-b", height,
	)
	if err != nil || len(second) != 1 || second[0].MakerOrderID != "old_partial_sell" || second[0].Quantity.Cmp(big.NewInt(60)) != 0 {
		t.Fatalf("old partially-filled maker should remain eligible: trades=%+v err=%v", second, err)
	}
}

func TestMatchFutureOrderAtHeight_OrdinaryMatchingStillAcceptsCurrentHeightMaker(t *testing.T) {
	sv := newMemStateView()
	const height uint64 = 10
	price := big.NewInt(100_000_000)
	qty := big.NewInt(10)
	placeFutureMakerAtHeight(t, sv, "ordinary_current_sell", "maker", pb.OrderSide_SELL, price, qty, height)

	trades, err := MatchFutureOrderAtHeight(sv, selfTradePair, pb.OrderSide_BUY, qty, price, false, 0, "taker", height)
	if err != nil {
		t.Fatalf("MatchFutureOrderAtHeight err: %v", err)
	}
	if len(trades) != 1 || trades[0].MakerOrderID != "ordinary_current_sell" {
		t.Fatalf("ordinary matching must keep same-height behavior, got %+v", trades)
	}
}

                                                                                            
func TestMatchSpotOrder_SelfAndOthersBothMatch(t *testing.T) {
	sv := newMemStateView()
	qty := big.NewInt(10)
	                                                                        
	placeSpotMaker(t, sv, "alice_sell", "alice", pb.OrderSide_SELL, big.NewInt(100_000_000), qty)
	placeSpotMaker(t, sv, "bob_sell", "bob", pb.OrderSide_SELL, big.NewInt(100_000_000), qty)

	                                                                     
	trades, err := MatchSpotOrder(sv, selfTradePair, pb.OrderSide_BUY, new(big.Int).Mul(qty, big.NewInt(2)), big.NewInt(100_000_000), false, 0, "alice")
	if err != nil {
		t.Fatalf("MatchSpotOrder err: %v", err)
	}
	if len(trades) != 2 {
		t.Fatalf("expect alice and bob matched, got %d trades: %+v", len(trades), trades)
	}
	if trades[0].MakerOrderID != "alice_sell" || trades[1].MakerOrderID != "bob_sell" {
		t.Fatalf("expect alice_sell then bob_sell matched, got %+v", trades)
	}
}
