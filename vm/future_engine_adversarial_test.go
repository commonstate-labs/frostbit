package vm

import (
	"dex/future"
	"dex/keys"
	"dex/pb"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
)

type futureRoleSwapOutcome struct {
	loserPnL  *big.Int
	winnerPnL *big.Int
	insurance *big.Int
	shortfall *big.Int
}

func TestFutureMatchEngineSettlesLosingLegBeforeWinningLeg(t *testing.T) {
	loserAsMaker := runFutureRoleSwapTrade(t, true)
	loserAsTaker := runFutureRoleSwapTrade(t, false)
	wantPnL := new(big.Int).Mul(big.NewInt(10), future.PriceScale)

	for name, got := range map[string]futureRoleSwapOutcome{
		"loser_as_maker": loserAsMaker,
		"loser_as_taker": loserAsTaker,
	} {
		t.Run(name, func(t *testing.T) {
			if got.loserPnL.Cmp(new(big.Int).Neg(wantPnL)) != 0 {
				t.Fatalf("losing close realized pnl = %s, want -%s", got.loserPnL, wantPnL)
			}
			if got.winnerPnL.Cmp(wantPnL) != 0 {
				t.Fatalf("winning close realized pnl = %s, want %s", got.winnerPnL, wantPnL)
			}
			if got.shortfall.Sign() != 0 {
				t.Fatalf("balanced trade must not create insurance shortfall, got %s", got.shortfall)
			}
		})
	}

	if loserAsMaker.insurance.Cmp(loserAsTaker.insurance) != 0 {
		t.Fatalf("insurance balance depends on maker/taker role swap: loser-maker=%s loser-taker=%s", loserAsMaker.insurance, loserAsTaker.insurance)
	}
}

func runFutureRoleSwapTrade(t *testing.T, loserIsMaker bool) futureRoleSwapOutcome {
	t.Helper()
	sv := newMemStateView()
	pair := "ROLE_SWAP_USDT"
	qty := new(big.Int).Set(future.PriceScale)
	entryPrice := new(big.Int).Mul(big.NewInt(100), future.PriceScale)
	tradePrice := new(big.Int).Mul(big.NewInt(110), future.PriceScale)
	positionMargin := new(big.Int).Mul(big.NewInt(50), future.PriceScale)
	initialBalance := big.NewInt(1_000_000_000_000_000)

	sv.Set(keys.KeySpotLastPrice(pair), tradePrice.Bytes())
	giveBalance(t, sv, "loser", "USDT", initialBalance)
	giveBalance(t, sv, "winner", "USDT", initialBalance)
	writeFuturePosition(t, sv, &pb.FuturePositionState{
		PositionId: "loser_pos", Owner: "loser", Pair: pair,
		Side: int32(future.Short), EntryPrice: entryPrice.String(), Quantity: qty.String(),
		Margin: positionMargin.String(), Status: int32(future.StatusOpen), OpenHeight: 1,
		RealizedPnl: "0", MaxQuantity: qty.String(), InitialMargin: positionMargin.String(),
	})
	writeFuturePosition(t, sv, &pb.FuturePositionState{
		PositionId: "winner_pos", Owner: "winner", Pair: pair,
		Side: int32(future.Long), EntryPrice: entryPrice.String(), Quantity: qty.String(),
		Margin: positionMargin.String(), Status: int32(future.StatusOpen), OpenHeight: 1,
		RealizedPnl: "0", MaxQuantity: qty.String(), InitialMargin: positionMargin.String(),
	})

	makerOwner, takerOwner := "winner", "loser"
	makerSide, takerSide := future.Short, future.Long
	if loserIsMaker {
		makerOwner, takerOwner = "loser", "winner"
		makerSide, takerSide = future.Long, future.Short
	}
	applyFutureOrderForTest(t, sv, "role_swap_maker", makerOwner, pair, makerSide, qty, tradePrice, 10, 10)
	applyFutureOrderForTest(t, sv, "role_swap_taker", takerOwner, pair, takerSide, qty, tradePrice, 10, 11)

	loser := readFuturePositionHistory(t, sv, "loser_pos")
	winner := readFuturePositionHistory(t, sv, "winner_pos")
	return futureRoleSwapOutcome{
		loserPnL:  parseBigOrZero(loser.RealizedPnl),
		winnerPnL: parseBigOrZero(winner.RealizedPnl),
		insurance: readFutureBigIntKey(sv, keys.KeyFutureInsuranceFund(pair)),
		shortfall: readFutureBigIntKey(sv, keys.KeyFutureInsuranceShortfall(pair)),
	}
}

func TestFutureOpenOrderSplitFillsUseAggregateCeilMargin(t *testing.T) {
	for _, tc := range []struct {
		name  string
		price int64
	}{
		{name: "ceil_residue_is_not_charged_twice", price: 101},
		{name: "former_floor_split_bypass", price: 199},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sv := newMemStateView()
			pair := "SPLIT_" + tc.name + "_USDT"
			oneQty := new(big.Int).Set(future.PriceScale)
			totalQty := new(big.Int).Mul(big.NewInt(2), oneQty)
			price := big.NewInt(tc.price)
			const leverage uint64 = 100

			sv.Set(keys.KeySpotLastPrice(pair), price.Bytes())
			for _, owner := range []string{"maker1", "maker2", "taker"} {
				giveBalance(t, sv, owner, "USDT", big.NewInt(1_000_000))
			}
			applyFutureOrderForTest(t, sv, "maker1_order", "maker1", pair, future.Short, oneQty, price, leverage, 1)
			applyFutureOrderForTest(t, sv, "maker2_order", "maker2", pair, future.Short, oneQty, price, leverage, 2)
			applyFutureOrderForTest(t, sv, "taker_order", "taker", pair, future.Long, totalQty, price, leverage, 3)

			pos, err := (&svPositionStore{sv: sv}).GetPositionByOwnerAndPairSide("taker", pair, future.Long)
			if err != nil || pos == nil {
				t.Fatalf("taker position missing: pos=%+v err=%v", pos, err)
			}
			wantMargin := future.CalcRequiredMargin(totalQty, price, leverage)
			if pos.Margin.Cmp(wantMargin) != 0 {
				t.Fatalf("split fills allocated margin %s, aggregate order requires %s", pos.Margin, wantMargin)
			}
		})
	}
}

func TestFutureMakerUnsafePartialSkipsWithoutBlockingFullFill(t *testing.T) {
	sv := newMemStateView()
	pair := "MAKER_SPLIT_USDT"
	oneQty := new(big.Int).Set(future.PriceScale)
	totalQty := new(big.Int).Mul(big.NewInt(2), oneQty)
	price := big.NewInt(101)
	worsePrice := big.NewInt(102)
	const leverage uint64 = 100

	sv.Set(keys.KeySpotLastPrice(pair), price.Bytes())
	for _, owner := range []string{"maker", "fallback_maker", "taker1", "taker2"} {
		giveBalance(t, sv, owner, "USDT", big.NewInt(1_000_000))
	}
	applyFutureOrderForTest(t, sv, "maker_order", "maker", pair, future.Short, totalQty, price, leverage, 1)
	applyFutureOrderForTest(t, sv, "fallback_order", "fallback_maker", pair, future.Short, oneQty, worsePrice, leverage, 2)
	applyFutureOrderForTest(t, sv, "taker1_order", "taker1", pair, future.Long, oneQty, worsePrice, leverage, 3)

	pos, err := (&svPositionStore{sv: sv}).GetPositionByOwnerAndPairSide("maker", pair, future.Short)
	if err != nil || pos != nil {
		t.Fatalf("unsafe partial maker fill must be skipped: pos=%+v err=%v", pos, err)
	}
	fallback, err := (&svPositionStore{sv: sv}).GetPositionByOwnerAndPairSide("fallback_maker", pair, future.Short)
	if err != nil || fallback == nil || fallback.Quantity.Cmp(oneQty) != 0 {
		t.Fatalf("unsafe best maker blocked the safe next level: pos=%+v err=%v", fallback, err)
	}
	data, exists, err := sv.Get(keys.KeyFutureOrderReservedMargin("maker_order"))
	if err != nil || !exists {
		t.Fatalf("skipped maker reserve missing: exists=%v err=%v", exists, err)
	}
	reserved, err := futureReservedMarginAmount(data)
	wantReserved := future.CalcRequiredMargin(totalQty, price, leverage)
	if err != nil || reserved.Cmp(wantReserved) != 0 {
		t.Fatalf("skipped maker reserve=%v err=%v, want %s", reserved, err, wantReserved)
	}

	                                                                        
	                                                                       
	applyFutureOrderForTest(t, sv, "taker2_order", "taker2", pair, future.Long, totalQty, price, leverage, 4)
	pos, err = (&svPositionStore{sv: sv}).GetPositionByOwnerAndPairSide("maker", pair, future.Short)
	if err != nil || pos == nil {
		t.Fatalf("maker position after safe full fill missing: pos=%+v err=%v", pos, err)
	}
	wantTotal := future.CalcRequiredMargin(totalQty, price, leverage)
	if pos.Margin.Cmp(wantTotal) != 0 {
		t.Fatalf("final maker margin = %s, want aggregate requirement %s", pos.Margin, wantTotal)
	}
	if data, exists, _ := sv.Get(keys.KeyFutureOrderReservedMargin("maker_order")); exists || len(data) != 0 {
		t.Fatalf("filled maker reserve should be consumed: exists=%v data=%x", exists, data)
	}
}

func TestFutureMakerMarkerLargerThanFrozenBucketIsSkipped(t *testing.T) {
	sv := newMemStateView()
	pair := "MAKER_BUCKET_MISMATCH_USDT"
	unit := new(big.Int).Set(future.PriceScale)
	badPrice := big.NewInt(101)
	safePrice := big.NewInt(102)
	const leverage uint64 = 100
	sv.Set(keys.KeySpotLastPrice(pair), badPrice.Bytes())

	bad := &pb.FutureOrderState{
		OrderId: "bad_bucket_order", Owner: "bad_maker", OwnerAddress: "bad_maker", Pair: pair,
		Side: int32(future.Short), Price: badPrice.String(), Amount: unit.String(), FilledBase: "0", Leverage: leverage,
		Status: pb.FutureOrderStatus_FUTURE_ORDER_OPEN, CreateHeight: 1,
	}
	if err := writeFutureOrderSnapshot(sv, bad); err != nil {
		t.Fatalf("write bad maker: %v", err)
	}
	                                                                     
	                                                                          
	                                                                             
	reserveFutureOrderMarginForTest(t, sv, "bad_maker", "USDT", bad.OrderId, big.NewInt(1))
	sv.Set(keys.KeyFutureOrderReservedMargin(bad.OrderId), encodeFutureReservedMarginAllocation(&futureReservedMarginAllocation{
		Remaining:       big.NewInt(2),
		BaselineReserve: big.NewInt(2),
		BaselineQty:     new(big.Int).Set(unit),
		ConsumedQty:     new(big.Int),
		OpenConsumedQty: new(big.Int),
		MakerPrice:      new(big.Int).Set(badPrice),
		MakerLeverage:   leverage,
	}))

	for _, owner := range []string{"safe_maker", "taker"} {
		giveBalance(t, sv, owner, "USDT", big.NewInt(1_000_000))
	}
	applyFutureOrderForTest(t, sv, "safe_order", "safe_maker", pair, future.Short, unit, safePrice, leverage, 2)
	applyFutureOrderForTest(t, sv, "taker_order", "taker", pair, future.Long, unit, safePrice, leverage, 3)

	if pos, err := (&svPositionStore{sv: sv}).GetPositionByOwnerAndPairSide("safe_maker", pair, future.Short); err != nil || pos == nil {
		t.Fatalf("bad bucket maker blocked next safe level: pos=%+v err=%v", pos, err)
	}
	if _, exists, _ := sv.Get(keys.KeyFutureOrderState(bad.OrderId)); !exists {
		t.Fatal("skipped bad-bucket maker was unexpectedly mutated")
	}
}

func TestFutureMakerMissingReserveMarkerChecksRealFrozenBucket(t *testing.T) {
	sv := newMemStateView()
	pair := "MAKER_MISSING_MARKER_USDT"
	unit := new(big.Int).Set(future.PriceScale)
	price := big.NewInt(101)
	const leverage uint64 = 100
	state := &pb.FutureOrderState{
		OrderId: "legacy_missing_marker", Owner: "maker", OwnerAddress: "maker", Pair: pair,
		Side: int32(future.Short), Price: price.String(), Amount: unit.String(), FilledBase: "0", Leverage: leverage,
		Status: pb.FutureOrderStatus_FUTURE_ORDER_OPEN, CreateHeight: 1,
	}
	data, err := proto.Marshal(state)
	if err != nil {
		t.Fatalf("marshal missing-marker maker: %v", err)
	}
	maker, err := parseMakerOrderSnapshot(data, MarketFuture)
	if err != nil {
		t.Fatalf("parse missing-marker maker: %v", err)
	}
	                                                                           
	                                                                      
	                                                                           
	SetBalance(sv, "maker", "USDT", &pb.TokenBalance{OrderFrozenBalance: "1"})
	if futureMakerReserveCoversTrade(sv, pair, "taker", maker, unit, false) {
		t.Fatal("missing marker preflight ignored an insufficient real frozen bucket")
	}
}

func TestFutureMakerDifferentLeverageOrdersDoNotReleverageExistingPosition(t *testing.T) {
	sv := newMemStateView()
	pair := "MAKER_MIXED_LEVERAGE_USDT"
	unit := new(big.Int).Set(future.PriceScale)
	totalQty := new(big.Int).Mul(big.NewInt(2), unit)
	price := big.NewInt(1_000)
	const (
		firstLeverage  uint64 = 100
		secondLeverage uint64 = 10
	)
	sv.Set(keys.KeySpotLastPrice(pair), price.Bytes())
	for _, owner := range []string{"maker", "taker"} {
		giveBalance(t, sv, owner, "USDT", big.NewInt(1_000_000))
	}

	                                                                           
	                                                   
	applyFutureOrderForTest(t, sv, "a_100x", "maker", pair, future.Short, unit, price, firstLeverage, 1)
	applyFutureOrderForTest(t, sv, "b_10x", "maker", pair, future.Short, unit, price, secondLeverage, 2)
	applyFutureOrderForTest(t, sv, "mixed_leverage_taker", "taker", pair, future.Long, totalQty, price, firstLeverage, 3)

	pos, err := (&svPositionStore{sv: sv}).GetPositionByOwnerAndPairSide("maker", pair, future.Short)
	if err != nil || pos == nil {
		t.Fatalf("mixed-leverage maker position missing: pos=%+v err=%v", pos, err)
	}
	wantMargin := new(big.Int).Add(
		future.CalcRequiredMargin(unit, price, firstLeverage),
		future.CalcRequiredMargin(unit, price, secondLeverage),
	)
	if pos.Quantity.Cmp(totalQty) != 0 || pos.Margin.Cmp(wantMargin) != 0 {
		t.Fatalf("mixed-leverage maker qty/margin=%s/%s, want %s/%s", pos.Quantity, pos.Margin, totalQty, wantMargin)
	}
}

func TestFutureMakerSecondOrderPreflightDoesNotReuseOppositePosition(t *testing.T) {
	sv := newMemStateView()
	pair := "MAKER_STALE_OPPOSITE_USDT"
	unit := new(big.Int).Set(future.PriceScale)
	twoUnits := new(big.Int).Mul(big.NewInt(2), unit)
	price := big.NewInt(101)
	const leverage uint64 = 100
	sv.Set(keys.KeySpotLastPrice(pair), price.Bytes())
	for _, owner := range []string{"maker", "taker"} {
		giveBalance(t, sv, owner, "USDT", big.NewInt(1_000_000))
	}
	writeFuturePosition(t, sv, reverseBoundaryPosition("maker_old_long", "maker", pair, future.Long, unit, price))

	                                                                           
	                                                                           
	                                                                              
	applyFutureOrderForTest(t, sv, "a_close_old", "maker", pair, future.Short, unit, price, leverage, 1)
	applyFutureOrderForTest(t, sv, "b_partial_after_close", "maker", pair, future.Short, twoUnits, price, leverage, 2)
	applyFutureOrderForTest(t, sv, "stale_preflight_taker", "taker", pair, future.Long, twoUnits, price, leverage, 3)

	if pos, err := (&svPositionStore{sv: sv}).GetPositionByOwnerAndPairSide("maker", pair, future.Short); err != nil || pos != nil {
		t.Fatalf("unsafe second maker order unexpectedly opened: pos=%+v err=%v", pos, err)
	}
	data, exists, err := sv.Get(keys.KeyFutureOrderState("b_partial_after_close"))
	if err != nil || !exists {
		t.Fatalf("skipped second maker order missing: exists=%v err=%v", exists, err)
	}
	var state pb.FutureOrderState
	if err := proto.Unmarshal(data, &state); err != nil || state.FilledBase != "0" || state.Status != pb.FutureOrderStatus_FUTURE_ORDER_OPEN {
		t.Fatalf("skipped second maker state mutated: state=%+v err=%v", &state, err)
	}
	reserveData, exists, err := sv.Get(keys.KeyFutureOrderReservedMargin(state.OrderId))
	if err != nil || !exists {
		t.Fatalf("skipped second maker reserve missing: exists=%v err=%v", exists, err)
	}
	reserve, err := futureReservedMarginAmount(reserveData)
	wantReserve := future.CalcRequiredMargin(twoUnits, price, leverage)
	if err != nil || reserve.Cmp(wantReserve) != 0 {
		t.Fatalf("skipped second maker reserve=%v err=%v, want %s", reserve, err, wantReserve)
	}
}

func TestFutureMakerReservedMarginCumulativeAllocationIsAssociative(t *testing.T) {
	runPrefix := func(t *testing.T, chunks ...int64) (*big.Int, *futureReservedMarginAllocation) {
		t.Helper()
		sv := newMemStateView()
		reserveFutureOrderMarginForTest(t, sv, "maker", "USDT", "maker_order", big.NewInt(4))
		remainingQty := big.NewInt(4)
		totalConsumed := new(big.Int)
		for _, chunk := range chunks {
			qty := big.NewInt(chunk)
			portion, err := consumeFutureOrderReservedMargin(
				sv, "maker_order", "maker", "ALLOC_USDT", "USDT", future.Long,
				qty, new(big.Int).Set(remainingQty), qty, new(big.Int).Mul(big.NewInt(2), future.PriceScale), 2, false, 10,
			)
			if err != nil {
				t.Fatalf("consume chunk %d: %v", chunk, err)
			}
			totalConsumed.Add(totalConsumed, portion)
			remainingQty.Sub(remainingQty, qty)
		}
		data, exists, err := sv.Get(keys.KeyFutureOrderReservedMargin("maker_order"))
		if err != nil || !exists {
			t.Fatalf("non-terminal reserve key missing: exists=%v err=%v", exists, err)
		}
		allocation, versioned, err := decodeFutureReservedMarginAllocation(data)
		if err != nil || !versioned {
			t.Fatalf("allocation state missing: versioned=%v err=%v data=%x", versioned, err, data)
		}
		return totalConsumed, allocation
	}

	splitConsumed, split := runPrefix(t, 1, 1)
	mergedConsumed, merged := runPrefix(t, 2)
	if splitConsumed.Cmp(mergedConsumed) != 0 || splitConsumed.Cmp(big.NewInt(2)) != 0 {
		t.Fatalf("split/merged prefix allocation differs: split=%s merged=%s", splitConsumed, mergedConsumed)
	}
	if split.Remaining.Cmp(merged.Remaining) != 0 || split.Remaining.Cmp(big.NewInt(2)) != 0 {
		t.Fatalf("split/merged remaining reserve differs: split=%s merged=%s", split.Remaining, merged.Remaining)
	}
	if split.ConsumedQty.Cmp(merged.ConsumedQty) != 0 || split.ConsumedQty.Cmp(big.NewInt(2)) != 0 {
		t.Fatalf("split/merged cumulative quantity differs: split=%s merged=%s", split.ConsumedQty, merged.ConsumedQty)
	}
}

func TestFutureMakerReservedMarginRejectsUnsafeFragment(t *testing.T) {
	sv := newMemStateView()
	reserveFutureOrderMarginForTest(t, sv, "maker", "USDT", "maker_order", big.NewInt(2))
	if _, err := consumeFutureOrderReservedMargin(
		sv, "maker_order", "maker", "ALLOC_USDT", "USDT", future.Long,
		big.NewInt(1), big.NewInt(4), big.NewInt(1), future.PriceScale, 2, false, 1,
	); err == nil || !strings.Contains(err.Error(), "independently cover") {
		t.Fatalf("unsafe fragmented fill must fail closed, got %v", err)
	}
	data, exists, err := sv.Get(keys.KeyFutureOrderReservedMargin("maker_order"))
	if err != nil || !exists {
		t.Fatalf("failed fragment mutated marker existence: exists=%v err=%v", exists, err)
	}
	amount, err := futureReservedMarginAmount(data)
	if err != nil || amount.Cmp(big.NewInt(2)) != 0 {
		t.Fatalf("failed fragment reserve=%v err=%v, want 2", amount, err)
	}
	if frozen := parseBigOrZero(GetBalance(sv, "maker", "USDT").OrderFrozenBalance); frozen.Cmp(big.NewInt(2)) != 0 {
		t.Fatalf("failed fragment moved frozen balance %s", frozen)
	}
}

func TestFutureReservedMarginMalformedEncodingFailsClosed(t *testing.T) {
	sv := newMemStateView()
	reserveFutureOrderMarginForTest(t, sv, "maker", "USDT", "bad_order", big.NewInt(2))
	bad := append(append([]byte{}, futureReservedMarginAllocationMagic...), []byte("bad")...)
	sv.Set(keys.KeyFutureOrderReservedMargin("bad_order"), bad)

	if _, err := consumeFutureOrderReservedMargin(
		sv, "bad_order", "maker", "BAD_USDT", "USDT", future.Long,
		big.NewInt(1), big.NewInt(4), big.NewInt(1), future.PriceScale, 2, false, 1,
	); err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("malformed allocation must fail closed, got %v", err)
	}
	data, exists, _ := sv.Get(keys.KeyFutureOrderReservedMargin("bad_order"))
	if !exists || string(data) != string(bad) {
		t.Fatalf("failed decode mutated reserve key: exists=%v data=%x", exists, data)
	}
	if frozen := parseBigOrZero(GetBalance(sv, "maker", "USDT").OrderFrozenBalance); frozen.Cmp(big.NewInt(2)) != 0 {
		t.Fatalf("failed decode moved frozen funds: got %s want=2", frozen)
	}

	state := &pb.FutureOrderState{
		OrderId: "bad_order", Owner: "maker", OwnerAddress: "maker", Pair: "BAD_USDT",
		Side: int32(future.Long), Price: "100", Amount: "4", FilledBase: "1", Leverage: 10,
		Status: pb.FutureOrderStatus_FUTURE_ORDER_OPEN, CreateHeight: 1,
	}
	writeFutureOrderState(t, sv, state)
	if _, _, err := cancelFutureOpenOrderState(sv, state, 2, "test_cancel"); err == nil || !strings.Contains(err.Error(), "decode future reserved margin") {
		t.Fatalf("cancel must reject malformed reserve state, got %v", err)
	}
	if _, exists, _ := sv.Get(keys.KeyFutureOrderState("bad_order")); !exists {
		t.Fatal("malformed reserve cancellation deleted live order")
	}
}

func TestFutureMakerSelfTradePreflightIgnoresDisposablePositionMargin(t *testing.T) {
	sv := newMemStateView()
	pair := "SELF_PREFLIGHT_USDT"
	unit := new(big.Int).Set(future.PriceScale)
	totalQty := new(big.Int).Mul(big.NewInt(2), unit)
	price := big.NewInt(101)
	const leverage uint64 = 100

	                                                                         
	                                                                      
	writeFuturePosition(t, sv, reverseBoundaryPosition("alice_short", "alice", pair, future.Short, unit, price))
	state := &pb.FutureOrderState{
		OrderId: "self_maker", Owner: "alice", OwnerAddress: "alice", Pair: pair,
		Side: int32(future.Short), Price: price.String(), Amount: totalQty.String(), FilledBase: unit.String(), Leverage: leverage,
		Status: pb.FutureOrderStatus_FUTURE_ORDER_OPEN, CreateHeight: 1,
	}
	data, err := proto.Marshal(state)
	if err != nil {
		t.Fatalf("marshal self maker: %v", err)
	}
	maker, err := parseMakerOrderSnapshot(data, MarketFuture)
	if err != nil {
		t.Fatalf("parse self maker: %v", err)
	}
	reserveFutureOrderMarginForTest(t, sv, "alice", "USDT", state.OrderId, big.NewInt(1))
	sv.Set(keys.KeyFutureOrderReservedMargin(state.OrderId), encodeFutureReservedMarginAllocation(&futureReservedMarginAllocation{
		Remaining:       big.NewInt(1),
		BaselineReserve: big.NewInt(3),
		BaselineQty:     new(big.Int).Set(totalQty),
		ConsumedQty:     new(big.Int).Set(unit),
		OpenConsumedQty: new(big.Int).Set(unit),
		MakerPrice:      new(big.Int).Set(price),
		MakerLeverage:   leverage,
	}))

	if !futureMakerReserveCoversTrade(sv, pair, "other_taker", maker, unit, false) {
		t.Fatal("test precondition: a non-self fill can use the live same-side position margin")
	}
	                                                                           
	                                                                            
	                                              
	if futureMakerReserveCoversTrade(sv, pair, "alice", maker, unit, false) {
		t.Fatal("self-trade preflight borrowed margin that the taker leg can release")
	}
}

func TestLiquidationOwnMakerAcceptsZeroReservedMarker(t *testing.T) {
	sv := newMemStateView()
	state := &pb.FutureOrderState{
		OrderId: "zero_order", Owner: "alice", OwnerAddress: "alice", Pair: "ZERO_USDT",
		Side: int32(future.Long), Price: "100", Amount: "4", FilledBase: "3", Leverage: 10,
		Status: pb.FutureOrderStatus_FUTURE_ORDER_OPEN, CreateHeight: 1,
	}
	writeFutureOrderState(t, sv, state)
	sv.Set(keys.KeyFutureOrderReservedMargin(state.OrderId), encodeFutureReservedMarginAllocation(&futureReservedMarginAllocation{
		Remaining:       new(big.Int),
		BaselineReserve: big.NewInt(2),
		BaselineQty:     big.NewInt(4),
		ConsumedQty:     big.NewInt(3),
		OpenConsumedQty: big.NewInt(3),
		MakerPrice:      big.NewInt(100),
		MakerLeverage:   10,
	}))

	amount, err := consumeLiquidationOwnMakerMargin(sv, state, nil, "USDT", 10)
	if err != nil {
		t.Fatalf("zero-reserve own maker cancellation failed: %v", err)
	}
	if amount.Sign() != 0 {
		t.Fatalf("zero-reserve own maker moved margin %s", amount)
	}
	if _, exists, _ := sv.Get(keys.KeyFutureOrderReservedMargin(state.OrderId)); exists {
		t.Fatal("zero-reserve own maker retained reserve marker")
	}
	if _, exists, _ := sv.Get(keys.KeyFutureOrderState(state.OrderId)); exists {
		t.Fatal("zero-reserve own maker retained live order")
	}
}

func TestFutureTakerReverseSplitEqualsMerged(t *testing.T) {
	run := func(t *testing.T, makerChunks ...int64) *future.Position {
		t.Helper()
		sv := newMemStateView()
		pair := "TAKER_REVERSE_USDT"
		unit := new(big.Int).Set(future.PriceScale)
		price := big.NewInt(49)
		const leverage uint64 = 100
		sv.Set(keys.KeySpotLastPrice(pair), price.Bytes())
		giveBalance(t, sv, "reverser", "USDT", big.NewInt(1_000_000))
		writeFuturePosition(t, sv, reverseBoundaryPosition("reverser_old", "reverser", pair, future.Long, unit, price))

		totalQty := new(big.Int)
		for i, chunk := range makerChunks {
			owner := fmt.Sprintf("maker_%d", i)
			qty := new(big.Int).Mul(big.NewInt(chunk), unit)
			totalQty.Add(totalQty, qty)
			giveBalance(t, sv, owner, "USDT", big.NewInt(1_000_000))
			applyFutureOrderForTest(t, sv, fmt.Sprintf("maker_order_%d", i), owner, pair, future.Long, qty, price, leverage, uint64(i+1))
		}
		applyFutureOrderForTest(t, sv, "reverse_taker", "reverser", pair, future.Short, totalQty, price, leverage, 20)

		pos, err := (&svPositionStore{sv: sv}).GetPositionByOwnerAndPairSide("reverser", pair, future.Short)
		if err != nil || pos == nil {
			t.Fatalf("reverse taker position missing: pos=%+v err=%v", pos, err)
		}
		return pos
	}

	split := run(t, 1, 1, 1, 1)
	merged := run(t, 4)
	wantQty := new(big.Int).Mul(big.NewInt(3), future.PriceScale)
	wantMargin := future.CalcRequiredMargin(wantQty, big.NewInt(49), 100)
	for name, pos := range map[string]*future.Position{"split": split, "merged": merged} {
		if pos.Quantity.Cmp(wantQty) != 0 || pos.Margin.Cmp(wantMargin) != 0 {
			t.Fatalf("%s reverse result qty=%s margin=%s, want qty=%s margin=%s", name, pos.Quantity, pos.Margin, wantQty, wantMargin)
		}
	}
}

func TestFutureMakerReverseSplitEqualsMerged(t *testing.T) {
	run := func(t *testing.T, takerChunks ...int64) *future.Position {
		t.Helper()
		sv := newMemStateView()
		pair := "MAKER_REVERSE_USDT"
		unit := new(big.Int).Set(future.PriceScale)
		price := big.NewInt(49)
		const leverage uint64 = 100
		sv.Set(keys.KeySpotLastPrice(pair), price.Bytes())
		giveBalance(t, sv, "reverser", "USDT", big.NewInt(1_000_000))
		writeFuturePosition(t, sv, reverseBoundaryPosition("reverser_old", "reverser", pair, future.Long, unit, price))
		totalQty := new(big.Int).Mul(big.NewInt(4), unit)
		applyFutureOrderForTest(t, sv, "reverse_maker", "reverser", pair, future.Short, totalQty, price, leverage, 1)

		for i, chunk := range takerChunks {
			owner := fmt.Sprintf("taker_%d", i)
			qty := new(big.Int).Mul(big.NewInt(chunk), unit)
			giveBalance(t, sv, owner, "USDT", big.NewInt(1_000_000))
			applyFutureOrderForTest(t, sv, fmt.Sprintf("taker_order_%d", i), owner, pair, future.Long, qty, price, leverage, uint64(i+2))
		}

		pos, err := (&svPositionStore{sv: sv}).GetPositionByOwnerAndPairSide("reverser", pair, future.Short)
		if err != nil || pos == nil {
			t.Fatalf("reverse maker position missing: pos=%+v err=%v", pos, err)
		}
		return pos
	}

	split := run(t, 1, 1, 1, 1)
	merged := run(t, 4)
	wantQty := new(big.Int).Mul(big.NewInt(3), future.PriceScale)
	wantMargin := future.CalcRequiredMargin(wantQty, big.NewInt(49), 100)
	for name, pos := range map[string]*future.Position{"split": split, "merged": merged} {
		if pos.Quantity.Cmp(wantQty) != 0 || pos.Margin.Cmp(wantMargin) != 0 {
			t.Fatalf("%s reverse result qty=%s margin=%s, want qty=%s margin=%s", name, pos.Quantity, pos.Margin, wantQty, wantMargin)
		}
	}
}

func TestFutureMakerReverseAllocationTracksInterveningPositionChange(t *testing.T) {
	sv := newMemStateView()
	pair := "MAKER_REVERSE_INTERVENING_CLOSE_USDT"
	unit := new(big.Int).Set(future.PriceScale)
	price := big.NewInt(49)
	const leverage uint64 = 100
	sv.Set(keys.KeySpotLastPrice(pair), price.Bytes())
	sv.Set(keys.KeyFutureMarkPrice(pair), price.Bytes())
	for _, owner := range []string{"reverser", "taker_1", "taker_2", "taker_3", "taker_4"} {
		giveBalance(t, sv, owner, "USDT", big.NewInt(1_000_000))
	}

	oppositeQty := new(big.Int).Mul(big.NewInt(2), unit)
	makerQty := new(big.Int).Mul(big.NewInt(4), unit)
	writeFuturePosition(t, sv, reverseBoundaryPosition("reverser_old", "reverser", pair, future.Long, oppositeQty, price))
	applyFutureOrderForTest(t, sv, "reverse_maker", "reverser", pair, future.Short, makerQty, price, leverage, 1)

	                                                                           
	applyFutureOrderForTest(t, sv, "taker_order_1", "taker_1", pair, future.Long, unit, price, leverage, 2)
	allocationData, exists, err := sv.Get(keys.KeyFutureOrderReservedMargin("reverse_maker"))
	if err != nil || !exists {
		t.Fatalf("maker reserve after close fill missing: exists=%v err=%v", exists, err)
	}
	allocation, versioned, err := decodeFutureReservedMarginAllocation(allocationData)
	if err != nil || !versioned || allocation.OpenConsumedQty.Sign() != 0 {
		t.Fatalf("first maker fill must be tracked as close-only: allocation=%+v versioned=%v err=%v", allocation, versioned, err)
	}

	                                                                          
	                                                                             
	                                                                            
	seedFutureMakerForCloseTest(t, sv, pair, "intervening_close_maker", "close_counterparty", future.Long, unit, price, leverage, 2)
	_, err = NewFutureMatchEngine(sv, 3).CloseOrder(
		"reverser", pair, "USDT", future.Short, unit, price, leverage, false, 0, "reverser_old",
		&pb.BaseMessage{TxId: "separate_close", FromAddress: "reverser", ExecutedHeight: 3},
	)
	if err != nil {
		t.Fatalf("intervening close failed: %v", err)
	}
	for i := 2; i <= 4; i++ {
		owner := fmt.Sprintf("taker_%d", i)
		applyFutureOrderForTest(t, sv, fmt.Sprintf("taker_order_%d", i), owner, pair, future.Long, unit, price, leverage, uint64(i+2))
	}

	pos, err := (&svPositionStore{sv: sv}).GetPositionByOwnerAndPairSide("reverser", pair, future.Short)
	if err != nil || pos == nil {
		t.Fatalf("reverse maker position missing after intervening close: pos=%+v err=%v", pos, err)
	}
	wantQty := new(big.Int).Mul(big.NewInt(3), unit)
	wantMargin := future.CalcRequiredMargin(wantQty, price, leverage)
	if pos.Quantity.Cmp(wantQty) != 0 || pos.Margin.Cmp(wantMargin) != 0 {
		t.Fatalf("reverse maker final qty=%s margin=%s, want qty=%s margin=%s", pos.Quantity, pos.Margin, wantQty, wantMargin)
	}
	if _, exists, _ := sv.Get(keys.KeyFutureOrderReservedMargin("reverse_maker")); exists {
		t.Fatal("filled maker retained reserved margin state")
	}
}

func TestFuturePartialTakerReservesRestingRemainderIndependently(t *testing.T) {
	sv := newMemStateView()
	pair := "INDEPENDENT_RESTING_USDT"
	unit := new(big.Int).Set(future.PriceScale)
	price := big.NewInt(49)
	const leverage uint64 = 100
	sv.Set(keys.KeySpotLastPrice(pair), price.Bytes())
	for _, owner := range []string{"first_maker", "resting", "next_taker"} {
		giveBalance(t, sv, owner, "USDT", big.NewInt(1_000_000))
	}

	threeQty := new(big.Int).Mul(big.NewInt(3), unit)
	fourQty := new(big.Int).Mul(big.NewInt(4), unit)
	applyFutureOrderForTest(t, sv, "first_maker_order", "first_maker", pair, future.Short, threeQty, price, leverage, 1)
	applyFutureOrderForTest(t, sv, "resting_order", "resting", pair, future.Long, fourQty, price, leverage, 2)

	data, exists, err := sv.Get(keys.KeyFutureOrderReservedMargin("resting_order"))
	if err != nil || !exists {
		t.Fatalf("resting reserve missing: exists=%v err=%v", exists, err)
	}
	amount, err := futureReservedMarginAmount(data)
	wantReserve := future.CalcRequiredMargin(unit, price, leverage)
	if err != nil || amount.Cmp(wantReserve) != 0 {
		t.Fatalf("resting reserve amount=%v err=%v, want independent %s", amount, err, wantReserve)
	}
	if _, exists, _ := sv.Get(keys.KeyFutureOrderState("resting_order")); !exists {
		t.Fatal("partially filled zero-reserve order is not resting")
	}

	applyFutureOrderForTest(t, sv, "next_taker_order", "next_taker", pair, future.Short, unit, price, leverage, 3)
	pos, err := (&svPositionStore{sv: sv}).GetPositionByOwnerAndPairSide("resting", pair, future.Long)
	if err != nil || pos == nil {
		t.Fatalf("resting maker position missing after next block fill: pos=%+v err=%v", pos, err)
	}
	wantMargin := new(big.Int).Add(
		future.CalcRequiredMargin(threeQty, price, leverage),
		future.CalcRequiredMargin(unit, price, leverage),
	)
	if pos.Quantity.Cmp(fourQty) != 0 || pos.Margin.Cmp(wantMargin) != 0 {
		t.Fatalf("resting maker final qty=%s margin=%s, want independently collateralized qty=%s margin=%s", pos.Quantity, pos.Margin, fourQty, wantMargin)
	}
	if _, exists, _ := sv.Get(keys.KeyFutureOrderReservedMargin("resting_order")); exists {
		t.Fatal("terminal resting order retained reserve state")
	}
}

func TestFutureRestingRemainderSurvivesFilledPositionClose(t *testing.T) {
	sv := newMemStateView()
	pair := "RESTING_AFTER_CLOSE_USDT"
	unit := new(big.Int).Set(future.PriceScale)
	totalQty := new(big.Int).Mul(big.NewInt(2), unit)
	price := big.NewInt(101)
	const leverage uint64 = 100

	sv.Set(keys.KeySpotLastPrice(pair), price.Bytes())
	sv.Set(keys.KeyFutureMarkPrice(pair), price.Bytes())
	for _, owner := range []string{"first_maker", "resting", "next_taker"} {
		giveBalance(t, sv, owner, "USDT", big.NewInt(1_000_000))
	}

	                                                                           
	                                                                            
	                                                                  
	applyFutureOrderForTest(t, sv, "first_maker_order", "first_maker", pair, future.Short, unit, price, leverage, 1)
	applyFutureOrderForTest(t, sv, "resting_order", "resting", pair, future.Long, totalQty, price, leverage, 2)

	store := &svPositionStore{sv: sv}
	filled, err := store.GetPositionByOwnerAndPairSide("resting", pair, future.Long)
	if err != nil || filled == nil {
		t.Fatalf("load initially filled position: pos=%+v err=%v", filled, err)
	}
	wantUnitMargin := future.CalcRequiredMargin(unit, price, leverage)
	if filled.Quantity.Cmp(unit) != 0 || filled.Margin.Cmp(wantUnitMargin) != 0 {
		t.Fatalf("initial fill qty/margin=%s/%s, want %s/%s", filled.Quantity, filled.Margin, unit, wantUnitMargin)
	}
	reserveData, exists, err := sv.Get(keys.KeyFutureOrderReservedMargin("resting_order"))
	if err != nil || !exists {
		t.Fatalf("load resting reserve: exists=%v err=%v", exists, err)
	}
	reserve, err := futureReservedMarginAmount(reserveData)
	if err != nil || reserve.Cmp(wantUnitMargin) != 0 {
		t.Fatalf("resting reserve=%v err=%v, want standalone %s", reserve, err, wantUnitMargin)
	}

	                                                                            
	                                                                         
	                                                                 
	seedFutureMakerForCloseTest(t, sv, pair, "close_first_fill_maker", "close_counterparty", future.Long, unit, price, leverage, 2)
	if _, err := NewFutureMatchEngine(sv, 3).CloseOrder(
		"resting", pair, "USDT", future.Short, unit, price, leverage, false, 0, filled.ID,
		&pb.BaseMessage{TxId: "close_first_fill", FromAddress: "resting", ExecutedHeight: 3},
	); err != nil {
		t.Fatalf("close initially filled position: %v", err)
	}
	applyFutureOrderForTest(t, sv, "next_taker_order", "next_taker", pair, future.Short, unit, price, leverage, 4)

	reopened, err := (&svPositionStore{sv: sv}).GetPositionByOwnerAndPairSide("resting", pair, future.Long)
	if err != nil || reopened == nil {
		t.Fatalf("load maker position after intervening close: pos=%+v err=%v", reopened, err)
	}
	if reopened.Quantity.Cmp(unit) != 0 || reopened.Margin.Cmp(wantUnitMargin) != 0 {
		t.Fatalf("reopened maker qty/margin=%s/%s, want independently collateralized %s/%s", reopened.Quantity, reopened.Margin, unit, wantUnitMargin)
	}
	if _, exists, _ := sv.Get(keys.KeyFutureOrderReservedMargin("resting_order")); exists {
		t.Fatal("terminal maker fill retained reserve state")
	}
}

func TestFutureFullyRestingMakerRemainderSurvivesRepeatedPositionLifecycles(t *testing.T) {
	sv := newMemStateView()
	pair := "FULLY_RESTING_REOPEN_USDT"
	unit := new(big.Int).Set(future.PriceScale)
	totalQty := new(big.Int).Mul(big.NewInt(3), unit)
	price := big.NewInt(100)
	const leverage uint64 = 100
	initialBalance := big.NewInt(1_000_000)

	sv.Set(keys.KeySpotLastPrice(pair), price.Bytes())
	sv.Set(keys.KeyFutureMarkPrice(pair), price.Bytes())
	for _, owner := range []string{"maker", "taker_1", "taker_2", "taker_3"} {
		giveBalance(t, sv, owner, "USDT", initialBalance)
	}

	                                                                           
	                                                                      
	applyFutureOrderForTest(t, sv, "maker_order", "maker", pair, future.Short, totalQty, price, leverage, 1)

	for i := 1; i <= 3; i++ {
		applyFutureOrderForTest(t, sv, fmt.Sprintf("taker_order_%d", i), fmt.Sprintf("taker_%d", i), pair, future.Long, unit, price, leverage, uint64(i*2))
		pos, err := (&svPositionStore{sv: sv}).GetPositionByOwnerAndPairSide("maker", pair, future.Short)
		if err != nil || pos == nil {
			t.Fatalf("maker position after lifecycle fill %d missing: pos=%+v err=%v", i, pos, err)
		}
		if pos.Quantity.Cmp(unit) != 0 || pos.Margin.Cmp(big.NewInt(1)) != 0 {
			t.Fatalf("maker lifecycle %d qty/margin=%s/%s, want %s/1", i, pos.Quantity, pos.Margin, unit)
		}

		data, exists, err := sv.Get(keys.KeyFutureOrderReservedMargin("maker_order"))
		if i < 3 {
			if err != nil || !exists {
				t.Fatalf("maker reserve after lifecycle %d missing: exists=%v err=%v", i, exists, err)
			}
			reserved, decodeErr := futureReservedMarginAmount(data)
			want := big.NewInt(int64(3 - i))
			if decodeErr != nil || reserved.Cmp(want) != 0 {
				t.Fatalf("maker reserve after lifecycle %d=%v err=%v, want %s", i, reserved, decodeErr, want)
			}
			seedFutureMakerForCloseTest(t, sv, pair, fmt.Sprintf("lifecycle_close_maker_%d", i), fmt.Sprintf("close_counterparty_%d", i), future.Short, unit, price, leverage, uint64(i*2))
			if _, err := NewFutureMatchEngine(sv, uint64(i*2+1)).CloseOrder(
				"maker", pair, "USDT", future.Long, unit, price, leverage, false, 0, pos.ID,
				&pb.BaseMessage{TxId: fmt.Sprintf("close_%d", i), FromAddress: "maker", ExecutedHeight: uint64(i*2 + 1)},
			); err != nil {
				t.Fatalf("close maker lifecycle %d: %v", i, err)
			}
			continue
		}
		if err != nil || exists {
			t.Fatalf("terminal lifecycle retained reserve: exists=%v err=%v data=%x", exists, err, data)
		}
	}

	bal := GetBalance(sv, "maker", "USDT")
	wantAvailable := new(big.Int).Sub(initialBalance, big.NewInt(1))
	if parseBigOrZero(bal.Balance).Cmp(wantAvailable) != 0 || parseBigOrZero(bal.OrderFrozenBalance).Sign() != 0 {
		t.Fatalf("maker buckets after repeated lifecycle available/frozen=%s/%s, want %s/0", bal.Balance, bal.OrderFrozenBalance, wantAvailable)
	}
}

func reverseBoundaryPosition(id, owner, pair string, side future.Side, qty, price *big.Int) *pb.FuturePositionState {
	margin := future.CalcRequiredMargin(qty, price, 100)
	return &pb.FuturePositionState{
		PositionId: id, Owner: owner, Pair: pair, Side: int32(side),
		EntryPrice: price.String(), Quantity: qty.String(), Margin: margin.String(),
		Status: int32(future.StatusOpen), OpenHeight: 1,
		EntryNotionalNumerator: new(big.Int).Mul(qty, price).String(),
		InitialMargin:          margin.String(),
		MaxQuantity:            qty.String(),
		MarginBasisAmount:      margin.String(),
		MarginBasisClosed:      "0",
	}
}

func applyFutureOrderForTest(t *testing.T, sv StateView, txID, owner, pair string, side future.Side, qty, price *big.Int, leverage, height uint64) {
	t.Helper()
	tx := &pb.AnyTx{Content: &pb.AnyTx_FutureOrderTx{FutureOrderTx: &pb.FutureOrderTx{
		Base: &pb.BaseMessage{TxId: txID, FromAddress: owner, ExecutedHeight: height},
		Pair: pair, Side: int32(side), Quantity: qty.String(), Price: price.String(), Leverage: leverage,
	}}}
	if _, receipt, err := (&FutureOrderTxHandler{}).DryRun(tx, sv); err != nil || receipt == nil || receipt.Status != "SUCCEED" {
		t.Fatalf("future order %s failed: receipt=%+v err=%v", txID, receipt, err)
	}
}

func readFutureBigIntKey(sv StateView, key string) *big.Int {
	data, exists, err := sv.Get(key)
	if err != nil || !exists || len(data) == 0 {
		return new(big.Int)
	}
	return new(big.Int).SetBytes(data)
}
