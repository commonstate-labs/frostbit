package vm

import (
	"dex/db"
	"dex/future"
	"dex/keys"
	"dex/pb"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
)

type expiryCountingStateView struct {
	StateView
	expirySets map[string]int
	expiryDels map[string]int
}

type failBalanceAfterHistoryWriteView struct {
	StateView
	historyKey string
	balanceKey string
	armed      bool
}

func (s *failBalanceAfterHistoryWriteView) Set(key string, val []byte) {
	s.StateView.Set(key, val)
	if key == s.historyKey {
		s.armed = true
	}
}

func (s *failBalanceAfterHistoryWriteView) Get(key string) ([]byte, bool, error) {
	if s.armed && key == s.balanceKey {
		return nil, false, fmt.Errorf("injected batch-commit balance read failure")
	}
	return s.StateView.Get(key)
}

func newExpiryCountingStateView() *expiryCountingStateView {
	return &expiryCountingStateView{
		StateView:  newMemStateView(),
		expirySets: make(map[string]int),
		expiryDels: make(map[string]int),
	}
}

func seedFutureMakerForCloseTest(t *testing.T, sv StateView, pair, orderID, owner string, side future.Side, qty, price *big.Int, leverage uint64, height uint64) {
	t.Helper()
	giveBalance(t, sv, owner, "USDT", big.NewInt(1_000_000_000_000_000))
	state := &pb.FutureOrderState{
		OrderId: orderID, Owner: owner, OwnerAddress: owner, Pair: pair,
		Side: int32(side), Price: price.String(), Amount: qty.String(),
		FilledBase: "0", FilledQuote: "0", Leverage: leverage,
		Status: pb.FutureOrderStatus_FUTURE_ORDER_OPEN, CreateHeight: height,
	}
	if err := writeFutureOrderSnapshot(sv, state); err != nil {
		t.Fatalf("seed future maker: %v", err)
	}
	reserveFutureOrderMarginForTest(t, sv, owner, "USDT", orderID, future.CalcRequiredMargin(qty, price, leverage))
}

func (s *expiryCountingStateView) Set(key string, val []byte) {
	if strings.Contains(key, "future_order_expiry_") {
		s.expirySets[key]++
	}
	s.StateView.Set(key, val)
}

func (s *expiryCountingStateView) Del(key string) {
	if strings.Contains(key, "future_order_expiry_") {
		s.expiryDels[key]++
	}
	s.StateView.Del(key)
}

func TestFutureMatchEngine_OpenOrderPropagatesMakerFeeError(t *testing.T) {
	sv := newMemStateView()
	pair := "BTC_USDT"
	const height uint64 = 100
	qty := big.NewInt(1e8)
	price := big.NewInt(50_000 * 1e8)

	sv.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
	makerOrder := &pb.FutureOrderState{
		OrderId: "maker_sell", Owner: "bob", OwnerAddress: "bob", Pair: pair,
		Side: int32(future.Short), Price: price.String(), Amount: qty.String(),
		FilledBase: "0", FilledQuote: "0", Leverage: 10,
		Status: pb.FutureOrderStatus_FUTURE_ORDER_OPEN, CreateHeight: 1,
	}
	writeFutureOrderState(t, sv, makerOrder)
	reserveFutureOrderMarginForTest(t, sv, "bob", "USDT", makerOrder.OrderId, future.CalcRequiredMargin(qty, price, 10))
	priceKey, _ := db.PriceToKey128(price.String())
	orderData, _ := proto.Marshal(makerOrder)
	sv.Set(keys.KeyFutureOrderPriceIndex(pair, pb.OrderSide_SELL, false, priceKey, makerOrder.OrderId), orderData)
	giveBalance(t, sv, "alice", "USDT", big.NewInt(10_000_000_000))

	eng := NewFutureMatchEngine(sv, height)
	_, err := eng.OpenOrder("alice", pair, "USDT", future.Long, qty, price, 10, false, 0, future.CalcRequiredMargin(qty, price, 10), &pb.BaseMessage{TxId: "taker_buy", FromAddress: "alice", ExecutedHeight: height})
	if err == nil || !strings.Contains(err.Error(), "future trading fee failed role=maker") {
		t.Fatalf("expected maker fee error, got %v", err)
	}
	if data, exists, _ := sv.Get(keys.KeyFuturePosition("alice_BTC_USDT_1_100")); exists || len(data) != 0 {
		t.Fatal("open order fee failure should roll back taker position")
	}
	backData, exists, _ := sv.Get(keys.KeyFutureOrderState(makerOrder.OrderId))
	if !exists {
		t.Fatal("maker order should still exist after rollback")
	}
	var back pb.FutureOrderState
	_ = proto.Unmarshal(backData, &back)
	if back.FilledBase != "0" || back.Status != pb.FutureOrderStatus_FUTURE_ORDER_OPEN {
		t.Fatalf("maker order should be unchanged after rollback: %+v", &back)
	}
}

func TestFutureMatchEngine_CloseOrderPropagatesFeeError(t *testing.T) {
	sv := newMemStateView()
	pair := "BTC_USDT"
	const height uint64 = 100
	qty := big.NewInt(1e8)
	price := big.NewInt(50_000 * 1e8)

	sv.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
	sv.Set(keys.KeyFutureMarkPrice(pair), price.Bytes())
	pos := &pb.FuturePositionState{
		PositionId: "pos1", Owner: "alice", Pair: pair,
		Side: int32(future.Long), EntryPrice: price.String(), Quantity: qty.String(),
		Margin: "1",
		Status: int32(future.StatusOpen), OpenHeight: 1,
	}
	writeFuturePosition(t, sv, pos)
	seedFutureMakerForCloseTest(t, sv, pair, "close_fee_maker", "bob", future.Long, qty, price, 10, 1)

	eng := NewFutureMatchEngine(sv, height)
	_, err := eng.CloseOrder("alice", pair, "USDT", future.Short, qty, price, 10, false, 0, "pos1", &pb.BaseMessage{TxId: "close1", FromAddress: "alice", ExecutedHeight: height})
	if err == nil || !strings.Contains(err.Error(), "future trading fee failed role=taker") {
		t.Fatalf("expected close fee error, got %v", err)
	}
	back := readFuturePosition(t, sv, "pos1")
	if back.Status != int32(future.StatusOpen) || back.Quantity != qty.String() {
		t.Fatalf("close fee failure should roll back position, got %+v", back)
	}
}

func TestFutureMatchEngine_CloseOrderRejectsSameSide(t *testing.T) {
	sv := newMemStateView()
	pair := "BTC_USDT"
	const height uint64 = 100
	qty := big.NewInt(1e8)
	price := big.NewInt(50_000 * 1e8)

	sv.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
	sv.Set(keys.KeyFutureMarkPrice(pair), price.Bytes())
	pos := &pb.FuturePositionState{
		PositionId: "pos1", Owner: "alice", Pair: pair,
		Side: int32(future.Long), EntryPrice: price.String(), Quantity: qty.String(),
		Margin: "100000000", Status: int32(future.StatusOpen), OpenHeight: 1,
	}
	writeFuturePosition(t, sv, pos)

	eng := NewFutureMatchEngine(sv, height)
	_, err := eng.CloseOrder("alice", pair, "USDT", future.Long, qty, price, 10, false, 0, "pos1", &pb.BaseMessage{TxId: "close_same", FromAddress: "alice", ExecutedHeight: height})
	if err == nil || !strings.Contains(err.Error(), "opposite") {
		t.Fatalf("expected same-side close rejection, got %v", err)
	}
	back := readFuturePosition(t, sv, "pos1")
	if back.Quantity != qty.String() || back.Margin != "100000000" || back.Status != int32(future.StatusOpen) {
		t.Fatalf("same-side close must not mutate position: %+v", back)
	}
}

func TestFutureMatchEngine_CloseOrderWritesHistoryOnly(t *testing.T) {
	sv := newMemStateView()
	pair := "BTC_USDT"
	const height uint64 = 100
	qty := big.NewInt(1e8)
	price := big.NewInt(50_000 * 1e8)
	margin := future.CalcRequiredMargin(qty, price, 10)

	sv.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
	sv.Set(keys.KeyFutureMarkPrice(pair), price.Bytes())
	giveBalance(t, sv, "alice", "USDT", big.NewInt(1_000_000_000_000_000))
	pos := &pb.FuturePositionState{
		PositionId: "pos1", Owner: "alice", Pair: pair,
		Side: int32(future.Long), EntryPrice: price.String(), Quantity: qty.String(),
		Margin: margin.String(), Status: int32(future.StatusOpen), OpenHeight: 1,
	}
	writeFuturePosition(t, sv, pos)
	seedFutureMakerForCloseTest(t, sv, pair, "close_history_maker", "bob", future.Long, qty, price, 10, 1)

	eng := NewFutureMatchEngine(sv, height)
	_, err := eng.CloseOrder("alice", pair, "USDT", future.Short, qty, price, 10, false, 0, "pos1", &pb.BaseMessage{TxId: "close1", FromAddress: "alice", ExecutedHeight: height})
	if err != nil {
		t.Fatalf("close order failed: %v", err)
	}
	if data, exists, _ := sv.Get(keys.KeyFutureOrderState("close1")); exists || len(data) != 0 {
		t.Fatalf("closed order should be deleted from StateDB: exists=%v data=%x", exists, data)
	}
	if _, exists, _ := sv.Get(keys.KeyFutureAccountOrder("alice", pair, pb.FutureOrderStatus_FUTURE_ORDER_CLOSED, height, "close1")); !exists {
		t.Fatal("closed order history should remain in KV account index")
	}
	back := readFuturePositionHistory(t, sv, "pos1")
	if future.PositionStatus(back.Status) != future.StatusClosed {
		t.Fatalf("position should be closed, got status=%d", back.Status)
	}
}

func TestFutureOrderCancelWritesHistoryOnly(t *testing.T) {
	sv := newMemStateView()
	pair := "BTC_USDT"
	const leverage uint64 = 10
	qty := big.NewInt(1e8)
	price := big.NewInt(50_000 * 1e8)

	sv.Set(keys.KeySpotLastPrice(pair), price.Bytes())
	giveBalance(t, sv, "maker", "USDT", big.NewInt(1_000_000_000_000_000))

	openTx := &pb.AnyTx{Content: &pb.AnyTx_FutureOrderTx{FutureOrderTx: &pb.FutureOrderTx{
		Base: &pb.BaseMessage{TxId: "cancel_me", FromAddress: "maker", ExecutedHeight: 1},
		Pair: pair, Side: int32(future.Long), Quantity: qty.String(), Price: price.String(), Leverage: leverage,
	}}}
	if _, rc, err := (&FutureOrderTxHandler{}).DryRun(openTx, sv); err != nil || rc == nil || rc.Status != "SUCCEED" {
		t.Fatalf("open order failed rc=%+v err=%v", rc, err)
	}
	if _, exists, _ := sv.Get(keys.KeyFutureOrderState("cancel_me")); !exists {
		t.Fatal("open order should stay in StateDB before cancel")
	}

	cancelTx := &pb.AnyTx{Content: &pb.AnyTx_FutureOrderCancelTx{FutureOrderCancelTx: &pb.FutureOrderCancelTx{
		Base:    &pb.BaseMessage{TxId: "cancel_tx", FromAddress: "maker", ExecutedHeight: 2},
		OrderId: "cancel_me",
	}}}
	if _, rc, err := (&FutureOrderCancelTxHandler{}).DryRun(cancelTx, sv); err != nil || rc == nil || rc.Status != "SUCCEED" {
		t.Fatalf("cancel order failed rc=%+v err=%v", rc, err)
	}
	if data, exists, _ := sv.Get(keys.KeyFutureOrderState("cancel_me")); exists || len(data) != 0 {
		t.Fatalf("cancelled order should be deleted from StateDB: exists=%v data=%x", exists, data)
	}
	if _, exists, _ := sv.Get(keys.KeyFutureAccountOrder("maker", pair, pb.FutureOrderStatus_FUTURE_ORDER_OPEN, 1, "cancel_me")); exists {
		t.Fatal("cancelled order should be removed from open account index")
	}
	if _, exists, _ := sv.Get(keys.KeyFutureAccountOrder("maker", pair, pb.FutureOrderStatus_FUTURE_ORDER_CANCELLED, 1, "cancel_me")); !exists {
		t.Fatal("cancelled order history should remain in KV account index")
	}
	if data, exists, _ := sv.Get(keys.KeyFutureOrderReservedMargin("cancel_me")); exists || len(data) != 0 {
		t.Fatalf("cancelled order reserved margin should be deleted: exists=%v data=%x", exists, data)
	}
}

func TestFutureMarginHandlersRejectNonOwner(t *testing.T) {
	sv := newMemStateView()
	pair := "BTC_USDT"
	price := big.NewInt(50_000 * 1e8)
	pos := &pb.FuturePositionState{
		PositionId: "bob_pos", Owner: "bob", Pair: pair,
		Side: int32(future.Long), EntryPrice: price.String(), Quantity: big.NewInt(1e8).String(),
		Margin: "1000000000", Status: int32(future.StatusOpen), OpenHeight: 1,
	}
	writeFuturePosition(t, sv, pos)
	giveBalance(t, sv, "bob", "USDT", big.NewInt(1_000_000_000))

	addTx := &pb.AnyTx{Content: &pb.AnyTx_FutureAddMarginTx{FutureAddMarginTx: &pb.FutureAddMarginTx{
		Base:       &pb.BaseMessage{TxId: "add", FromAddress: "alice", ExecutedHeight: 2},
		PositionId: "bob_pos", Amount: "100",
	}}}
	if _, rc, err := (&FutureAddMarginTxHandler{}).DryRun(addTx, sv); err == nil || rc == nil || !strings.Contains(err.Error(), "owner mismatch") {
		t.Fatalf("expected add-margin owner rejection rc=%+v err=%v", rc, err)
	}

	removeTx := &pb.AnyTx{Content: &pb.AnyTx_FutureRemoveMarginTx{FutureRemoveMarginTx: &pb.FutureRemoveMarginTx{
		Base:       &pb.BaseMessage{TxId: "remove", FromAddress: "alice", ExecutedHeight: 3},
		PositionId: "bob_pos", Amount: "100",
	}}}
	if _, rc, err := (&FutureRemoveMarginTxHandler{}).DryRun(removeTx, sv); err == nil || rc == nil || !strings.Contains(err.Error(), "owner mismatch") {
		t.Fatalf("expected remove-margin owner rejection rc=%+v err=%v", rc, err)
	}

	back := readFuturePosition(t, sv, "bob_pos")
	if back.Margin != pos.Margin {
		t.Fatalf("non-owner margin tx mutated position: got %s want %s", back.Margin, pos.Margin)
	}
}

func TestFutureOpenOrderUsesExecutionPriceForTakerMargin(t *testing.T) {
	sv := newMemStateView()
	pair := "BTC_USDT"
	const leverage uint64 = 10
	qty := big.NewInt(1e8)
	lowPrice := big.NewInt(10_000 * 1e8)
	highPrice := big.NewInt(50_000 * 1e8)
	wantMargin := future.CalcRequiredMargin(qty, highPrice, leverage)

	sv.Set(keys.KeySpotLastPrice(pair), lowPrice.Bytes())
	giveBalance(t, sv, "maker", "USDT", big.NewInt(1_000_000_000_000_000))
	giveBalance(t, sv, "taker", "USDT", big.NewInt(1_000_000_000_000_000))
	makerOrder := &pb.FutureOrderState{
		OrderId: "maker_buy", Owner: "maker", OwnerAddress: "maker", Pair: pair,
		Side: int32(future.Long), Price: highPrice.String(), Amount: qty.String(),
		FilledBase: "0", FilledQuote: "0", Leverage: leverage,
		Status: pb.FutureOrderStatus_FUTURE_ORDER_OPEN, CreateHeight: 1,
	}
	writeFutureOrderState(t, sv, makerOrder)
	reserveFutureOrderMarginForTest(t, sv, "maker", "USDT", makerOrder.OrderId, wantMargin)
	priceKey, _ := db.PriceToKey128(highPrice.String())
	orderData, _ := proto.Marshal(makerOrder)
	sv.Set(keys.KeyFutureOrderPriceIndex(pair, pb.OrderSide_BUY, false, priceKey, makerOrder.OrderId), orderData)

	tx := &pb.AnyTx{Content: &pb.AnyTx_FutureOrderTx{FutureOrderTx: &pb.FutureOrderTx{
		Base: &pb.BaseMessage{TxId: "taker_sell", FromAddress: "taker", ExecutedHeight: 2},
		Pair: pair, Side: int32(future.Short), Quantity: qty.String(), Price: lowPrice.String(), Leverage: leverage,
	}}}
	if _, rc, err := (&FutureOrderTxHandler{}).DryRun(tx, sv); err != nil || rc == nil || rc.Status != "SUCCEED" {
		t.Fatalf("taker short failed rc=%+v err=%v", rc, err)
	}
	pos, err := (&svPositionStore{sv: sv}).GetPositionByOwnerAndPairSide("taker", pair, future.Short)
	if err != nil || pos == nil {
		t.Fatalf("taker position missing err=%v", err)
	}
	if pos.Margin.Cmp(wantMargin) != 0 {
		t.Fatalf("taker margin should use execution price: got %s want %s", pos.Margin, wantMargin)
	}
}

func TestFutureLimitOrderReservedMarginMovesFromFrozenToPosition(t *testing.T) {
	sv := newMemStateView()
	pair := "BTC_USDT"
	const leverage uint64 = 10
	qty := big.NewInt(1e8)
	price := big.NewInt(50_000 * 1e8)
	required := future.CalcRequiredMargin(qty, price, leverage)

	sv.Set(keys.KeySpotLastPrice(pair), price.Bytes())
	giveBalance(t, sv, "maker", "USDT", big.NewInt(1_000_000_000_000_000))
	giveBalance(t, sv, "taker", "USDT", big.NewInt(1_000_000_000_000_000))
	totalBefore := conservationTotalForTest(t, sv, "USDT", pair)

	handler := &FutureOrderTxHandler{}
	makerTx := &pb.AnyTx{Content: &pb.AnyTx_FutureOrderTx{FutureOrderTx: &pb.FutureOrderTx{
		Base: &pb.BaseMessage{TxId: "maker_sell", FromAddress: "maker", ExecutedHeight: 1},
		Pair: pair, Side: int32(future.Short), Quantity: qty.String(), Price: price.String(), Leverage: leverage,
	}}}
	if _, rc, err := handler.DryRun(makerTx, sv); err != nil || rc == nil || rc.Status != "SUCCEED" {
		t.Fatalf("maker order failed rc=%+v err=%v", rc, err)
	}
	makerBal := GetBalance(sv, "maker", "USDT")
	if makerBal.OrderFrozenBalance != required.String() {
		t.Fatalf("maker remaining margin should be order-frozen: got %s want %s", makerBal.OrderFrozenBalance, required)
	}
	if data, exists, _ := sv.Get(keys.KeyFutureOrderReservedMargin("maker_sell")); !exists {
		t.Fatal("reserved margin key missing")
	} else if amount, err := futureReservedMarginAmount(data); err != nil || amount.Cmp(required) != 0 {
		t.Fatalf("reserved margin key mismatch amount=%v err=%v data=%x want=%s", amount, err, data, required)
	}
	if _, exists, _ := sv.Get(keys.KeyFutureOrderState("maker_sell")); !exists {
		t.Fatal("open maker order should stay in StateDB")
	}
	if _, exists, _ := sv.Get(keys.KeyFutureOrderExpiry(1+future.DefaultNoFillOrderTTLBlocks, "maker_sell")); !exists {
		t.Fatal("open never-matched maker order should have expiry index")
	}
	if total := conservationTotalForTest(t, sv, "USDT", pair); total.Cmp(totalBefore) != 0 {
		t.Fatalf("placing maker order changed conservation total: before=%s after=%s", totalBefore, total)
	}

	takerTx := &pb.AnyTx{Content: &pb.AnyTx_FutureOrderTx{FutureOrderTx: &pb.FutureOrderTx{
		Base: &pb.BaseMessage{TxId: "taker_buy", FromAddress: "taker", ExecutedHeight: 2},
		Pair: pair, Side: int32(future.Long), Quantity: qty.String(), Price: price.String(), Leverage: leverage,
	}}}
	if _, rc, err := handler.DryRun(takerTx, sv); err != nil || rc == nil || rc.Status != "SUCCEED" {
		t.Fatalf("taker order failed rc=%+v err=%v", rc, err)
	}
	makerBal = GetBalance(sv, "maker", "USDT")
	if makerBal.OrderFrozenBalance != "0" && makerBal.OrderFrozenBalance != "" {
		t.Fatalf("maker frozen margin should be consumed after fill, got %s", makerBal.OrderFrozenBalance)
	}
	if data, exists, _ := sv.Get(keys.KeyFutureOrderReservedMargin("maker_sell")); exists || len(data) != 0 {
		t.Fatalf("reserved margin key should be deleted after fill: exists=%v data=%x", exists, data)
	}
	if data, exists, _ := sv.Get(keys.KeyFutureOrderExpiry(1+future.DefaultNoFillOrderTTLBlocks, "maker_sell")); exists || len(data) != 0 {
		t.Fatalf("expiry index should be deleted after fill: exists=%v data=%x", exists, data)
	}
	if data, exists, _ := sv.Get(keys.KeyFutureOrderState("maker_sell")); exists || len(data) != 0 {
		t.Fatalf("filled maker order should be deleted from StateDB: exists=%v data=%x", exists, data)
	}
	if _, exists, _ := sv.Get(keys.KeyFutureAccountOrder("maker", pair, pb.FutureOrderStatus_FUTURE_ORDER_OPEN, 1, "maker_sell")); exists {
		t.Fatal("filled maker order should be removed from open account index")
	}
	if _, exists, _ := sv.Get(keys.KeyFutureAccountOrder("maker", pair, pb.FutureOrderStatus_FUTURE_ORDER_FILLED, 1, "maker_sell")); !exists {
		t.Fatal("filled maker order history should remain in KV account index")
	}
	if data, exists, _ := sv.Get(keys.KeyFutureOrderState("taker_buy")); exists || len(data) != 0 {
		t.Fatalf("filled taker order should be deleted from StateDB: exists=%v data=%x", exists, data)
	}
	if _, exists, _ := sv.Get(keys.KeyFutureAccountOrder("taker", pair, pb.FutureOrderStatus_FUTURE_ORDER_FILLED, 2, "taker_buy")); !exists {
		t.Fatal("filled taker order history should remain in KV account index")
	}
	pos, err := (&svPositionStore{sv: sv}).GetPositionByOwnerAndPairSide("maker", pair, future.Short)
	if err != nil || pos == nil {
		t.Fatalf("maker position missing err=%v", err)
	}
	if pos.Margin.Cmp(required) != 0 {
		t.Fatalf("maker margin should come from frozen reserve: got %s want %s", pos.Margin, required)
	}
	if total := conservationTotalForTest(t, sv, "USDT", pair); total.Cmp(totalBefore) != 0 {
		t.Fatalf("filling maker order changed conservation total: before=%s after=%s", totalBefore, total)
	}
}

func TestFutureMatchEngine_SelfTradeCloseThenReincreaseKeepsFinalPosition(t *testing.T) {
	sv := newMemStateView()
	pair := "BTC_USDT"
	const (
		height   uint64 = 100
		leverage uint64 = 10
	)
	scale := new(big.Int).Set(future.PriceScale)
	price := new(big.Int).Mul(big.NewInt(100), scale)
	oneQty := new(big.Int).Set(scale)
	twoQty := new(big.Int).Mul(big.NewInt(2), scale)
	oneMargin := future.CalcRequiredMargin(oneQty, price, leverage)
	twoMargin := new(big.Int).Mul(big.NewInt(2), oneMargin)

	sv.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
	sv.Set(keys.KeySpotLastPrice(pair), price.Bytes())
	sv.Set(keys.KeyFutureMarkPrice(pair), price.Bytes())
	giveBalance(t, sv, "alice", "USDT", new(big.Int).Mul(big.NewInt(1_000), scale))
	writeFuturePosition(t, sv, &pb.FuturePositionState{
		PositionId: "alice_self_long", Owner: "alice", Pair: pair,
		Side: int32(future.Long), EntryPrice: price.String(),
		Quantity: twoQty.String(), Margin: twoMargin.String(),
		Status: int32(future.StatusOpen), OpenHeight: 1,
	})

	                                                                             
	                                                                     
	makerOrder := &pb.FutureOrderState{
		OrderId: "alice_self_buy", Owner: "alice", OwnerAddress: "alice", Pair: pair,
		Side: int32(future.Long), Price: price.String(),
		Amount: oneQty.String(), FilledBase: "0", FilledQuote: "0",
		Leverage: leverage, Status: pb.FutureOrderStatus_FUTURE_ORDER_OPEN,
		CreateHeight: 1,
	}
	writeFutureOrderState(t, sv, makerOrder)
	reserveFutureOrderMarginForTest(t, sv, "alice", "USDT", makerOrder.OrderId, oneMargin)
	priceKey, err := db.PriceToKey128(price.String())
	if err != nil {
		t.Fatal(err)
	}
	orderData, err := proto.Marshal(makerOrder)
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyFutureOrderPriceIndex(pair, pb.OrderSide_BUY, false, priceKey, makerOrder.OrderId), orderData)
	totalBefore := conservationTotalForTest(t, sv, "USDT", pair)

	eng := NewFutureMatchEngine(sv, height)
	res, err := eng.OpenOrder(
		"alice", pair, "USDT", future.Short, oneQty, price, leverage,
		false, 0, new(big.Int),
		&pb.BaseMessage{TxId: "alice_self_sell", FromAddress: "alice", ExecutedHeight: height},
	)
	if err != nil {
		t.Fatalf("self-trade open order: %v", err)
	}
	if res == nil || res.MatchedQty.Cmp(oneQty) != 0 || res.Status != pb.FutureOrderStatus_FUTURE_ORDER_FILLED {
		t.Fatalf("unexpected self-trade result: %+v", res)
	}

	pos, err := (&svPositionStore{sv: sv}).GetPosition("alice_self_long")
	if err != nil || pos == nil {
		t.Fatalf("load final self-trade position: pos=%+v err=%v", pos, err)
	}
	if pos.Status != future.StatusOpen || pos.Quantity.Cmp(twoQty) != 0 || pos.Margin.Cmp(twoMargin) != 0 {
		t.Fatalf("batch commit restored stale self-trade clone: status=%v qty=%s margin=%s, want OPEN/%s/%s",
			pos.Status, pos.Quantity, pos.Margin, twoQty, twoMargin)
	}
	if totalAfter := conservationTotalForTest(t, sv, "USDT", pair); totalAfter.Cmp(totalBefore) != 0 {
		t.Fatalf("self-trade changed conserved total: before=%s after=%s", totalBefore, totalAfter)
	}
}

func TestFutureMatchEngine_BatchCommitFailureRollsBackWholeOrder(t *testing.T) {
	base := newMemStateView()
	pair := "BTC_USDT"
	const (
		height   uint64 = 100
		leverage uint64 = 10
	)
	scale := new(big.Int).Set(future.PriceScale)
	qty := new(big.Int).Set(scale)
	entryPrice := new(big.Int).Mul(big.NewInt(100), scale)
	tradePrice := new(big.Int).Mul(big.NewInt(98), scale)
	makerMargin := new(big.Int).Mul(big.NewInt(10), scale)
	takerMargin := future.CalcRequiredMargin(qty, tradePrice, leverage)
	wantProfit := new(big.Int).Mul(big.NewInt(2), scale)

	base.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
	base.Set(keys.KeySpotLastPrice(pair), tradePrice.Bytes())
	base.Set(keys.KeyFutureMarkPrice(pair), tradePrice.Bytes())
	base.Set(keys.KeyFutureInsuranceFund(pair), wantProfit.Bytes())
	giveBalance(t, base, "alice", "USDT", new(big.Int).Mul(big.NewInt(1_000), scale))
	if err := NewLedger(base).Move(MoveRequest{
		Account: "alice", Token: "USDT", Amount: takerMargin,
		From: BucketAvailable, To: BucketMargin, Reason: "test_prefund_taker",
	}); err != nil {
		t.Fatal(err)
	}
	writeFuturePosition(t, base, &pb.FuturePositionState{
		PositionId: "bob_commit_short", Owner: "bob", Pair: pair,
		Side: int32(future.Short), EntryPrice: entryPrice.String(),
		Quantity: qty.String(), Margin: makerMargin.String(),
		Status: int32(future.StatusOpen), OpenHeight: 1,
	})
	makerOrder := &pb.FutureOrderState{
		OrderId: "bob_commit_buy", Owner: "bob", OwnerAddress: "bob", Pair: pair,
		Side: int32(future.Long), Price: tradePrice.String(),
		Amount: qty.String(), FilledBase: "0", FilledQuote: "0",
		Leverage: leverage, Status: pb.FutureOrderStatus_FUTURE_ORDER_OPEN,
		CreateHeight: 1,
	}
	writeFutureOrderState(t, base, makerOrder)
	reserveFutureOrderMarginForTest(t, base, "bob", "USDT", makerOrder.OrderId, takerMargin)
	priceKey, err := db.PriceToKey128(tradePrice.String())
	if err != nil {
		t.Fatal(err)
	}
	orderData, err := proto.Marshal(makerOrder)
	if err != nil {
		t.Fatal(err)
	}
	base.Set(keys.KeyFutureOrderPriceIndex(pair, pb.OrderSide_BUY, false, priceKey, makerOrder.OrderId), orderData)
	totalBefore := conservationTotalForTest(t, base, "USDT", pair)

	wrapped := &failBalanceAfterHistoryWriteView{
		StateView:  base,
		historyKey: keys.KeyFuturePositionHistory("bob_commit_short"),
		balanceKey: keys.KeyBalance("bob", "USDT"),
	}
	eng := NewFutureMatchEngine(wrapped, height)
	_, err = eng.OpenOrder(
		"alice", pair, "USDT", future.Short, qty, tradePrice, leverage,
		false, 0, takerMargin,
		&pb.BaseMessage{TxId: "alice_commit_sell", FromAddress: "alice", ExecutedHeight: height},
	)
	if err == nil || !strings.Contains(err.Error(), "credit batched realized profit") {
		t.Fatalf("expected injected batch commit failure, got %v", err)
	}

	bob, err := (&svPositionStore{sv: base}).GetPosition("bob_commit_short")
	if err != nil || bob == nil || bob.Status != future.StatusOpen || bob.Quantity.Cmp(qty) != 0 || bob.Margin.Cmp(makerMargin) != 0 {
		t.Fatalf("failed commit changed maker position: pos=%+v err=%v", bob, err)
	}
	if alice, err := (&svPositionStore{sv: base}).GetPositionByOwnerAndPairSide("alice", pair, future.Short); err != nil || alice != nil {
		t.Fatalf("failed commit retained taker position: pos=%+v err=%v", alice, err)
	}
	backData, exists, err := base.Get(keys.KeyFutureOrderState(makerOrder.OrderId))
	if err != nil || !exists {
		t.Fatalf("failed commit removed maker order: exists=%v err=%v", exists, err)
	}
	var backOrder pb.FutureOrderState
	if err := proto.Unmarshal(backData, &backOrder); err != nil || backOrder.FilledBase != "0" || backOrder.Status != pb.FutureOrderStatus_FUTURE_ORDER_OPEN {
		t.Fatalf("failed commit mutated maker order: order=%+v err=%v", &backOrder, err)
	}
	if fundData, exists, err := base.Get(keys.KeyFutureInsuranceFund(pair)); err != nil || !exists || new(big.Int).SetBytes(fundData).Cmp(wantProfit) != 0 {
		t.Fatalf("failed commit changed insurance fund: exists=%v balance=%s err=%v", exists, new(big.Int).SetBytes(fundData), err)
	}
	if totalAfter := conservationTotalForTest(t, base, "USDT", pair); totalAfter.Cmp(totalBefore) != 0 {
		t.Fatalf("failed commit changed conserved total: before=%s after=%s", totalBefore, totalAfter)
	}
}

func TestFutureOrderAutoExpiresNeverMatchedAfterTTL(t *testing.T) {
	sv := newMemStateView()
	pair := "BTC_USDT"
	const leverage uint64 = 10
	const createHeight uint64 = 1
	qty := big.NewInt(1e8)
	price := big.NewInt(50_000 * 1e8)
	initialBalance := big.NewInt(1_000_000_000_000_000)

	sv.Set(keys.KeySpotLastPrice(pair), price.Bytes())
	giveBalance(t, sv, "maker", "USDT", initialBalance)

	tx := &pb.AnyTx{Content: &pb.AnyTx_FutureOrderTx{FutureOrderTx: &pb.FutureOrderTx{
		Base: &pb.BaseMessage{TxId: "expire_me", FromAddress: "maker", ExecutedHeight: createHeight},
		Pair: pair, Side: int32(future.Long), Quantity: qty.String(), Price: price.String(), Leverage: leverage,
	}}}
	if _, rc, err := (&FutureOrderTxHandler{}).DryRun(tx, sv); err != nil || rc == nil || rc.Status != "SUCCEED" {
		t.Fatalf("open order failed rc=%+v err=%v", rc, err)
	}
	dueHeight := createHeight + future.DefaultNoFillOrderTTLBlocks
	if _, exists, _ := sv.Get(keys.KeyFutureOrderExpiry(dueHeight, "expire_me")); !exists {
		t.Fatal("open order should have expiry index")
	}

	processFutureBlockHooks(sv, dueHeight-1, map[string]bool{}, map[string]bool{})
	if _, exists, _ := sv.Get(keys.KeyFutureOrderState("expire_me")); !exists {
		t.Fatal("order should not expire before ttl height")
	}

	processFutureBlockHooks(sv, dueHeight, map[string]bool{}, map[string]bool{})
	if data, exists, _ := sv.Get(keys.KeyFutureOrderState("expire_me")); exists || len(data) != 0 {
		t.Fatalf("expired order should be deleted from StateDB: exists=%v data=%x", exists, data)
	}
	if data, exists, _ := sv.Get(keys.KeyFutureOrderReservedMargin("expire_me")); exists || len(data) != 0 {
		t.Fatalf("expired order reserved margin should be deleted: exists=%v data=%x", exists, data)
	}
	if data, exists, _ := sv.Get(keys.KeyFutureOrderExpiry(dueHeight, "expire_me")); exists || len(data) != 0 {
		t.Fatalf("expired order expiry index should be deleted: exists=%v data=%x", exists, data)
	}
	if _, exists, _ := sv.Get(keys.KeyFutureAccountOrder("maker", pair, pb.FutureOrderStatus_FUTURE_ORDER_CANCELLED, createHeight, "expire_me")); !exists {
		t.Fatal("expired order should remain in cancelled account history")
	}
	bal := GetBalance(sv, "maker", "USDT")
	if bal.Balance != initialBalance.String() || (bal.OrderFrozenBalance != "" && bal.OrderFrozenBalance != "0") {
		t.Fatalf("expired order should unfreeze margin, got balance=%s frozen=%s", bal.Balance, bal.OrderFrozenBalance)
	}
	priceKey, _ := db.PriceToKey128(price.String())
	if data, exists, _ := sv.Get(keys.KeyFutureOrderPriceIndex(pair, pb.OrderSide_BUY, false, priceKey, "expire_me")); exists || len(data) != 0 {
		t.Fatalf("expired order price index should be deleted: exists=%v data=%x", exists, data)
	}
}

func TestFutureOrderPartialFillRefreshesExpiryIndex(t *testing.T) {
	sv := newMemStateView()
	pair := "BTC_USDT"
	const leverage uint64 = 10
	const createHeight uint64 = 1
	const firstFillHeight uint64 = 10
	const secondFillHeight uint64 = 20
	qty := big.NewInt(10 * 1e8)
	firstFillQty := big.NewInt(3 * 1e8)
	secondFillQty := big.NewInt(2 * 1e8)
	price := big.NewInt(50_000 * 1e8)
	initialBalance := big.NewInt(1_000_000_000_000_000)

	sv.Set(keys.KeySpotLastPrice(pair), price.Bytes())
	sv.Set(keys.KeyFutureMarkPrice(pair), price.Bytes())
	giveBalance(t, sv, "maker", "USDT", initialBalance)
	giveBalance(t, sv, "taker1", "USDT", initialBalance)
	giveBalance(t, sv, "taker2", "USDT", initialBalance)
	handler := &FutureOrderTxHandler{}

	makerTx := &pb.AnyTx{Content: &pb.AnyTx_FutureOrderTx{FutureOrderTx: &pb.FutureOrderTx{
		Base: &pb.BaseMessage{TxId: "maker_sell_refresh", FromAddress: "maker", ExecutedHeight: createHeight},
		Pair: pair, Side: int32(future.Short), Quantity: qty.String(), Price: price.String(), Leverage: leverage,
	}}}
	if _, rc, err := handler.DryRun(makerTx, sv); err != nil || rc == nil || rc.Status != "SUCCEED" {
		t.Fatalf("maker order failed rc=%+v err=%v", rc, err)
	}
	createDue := createHeight + future.DefaultNoFillOrderTTLBlocks
	if _, exists, _ := sv.Get(keys.KeyFutureOrderExpiry(createDue, "maker_sell_refresh")); !exists {
		t.Fatal("open maker order should have create-height expiry index")
	}

	taker1Tx := &pb.AnyTx{Content: &pb.AnyTx_FutureOrderTx{FutureOrderTx: &pb.FutureOrderTx{
		Base: &pb.BaseMessage{TxId: "taker_buy_1", FromAddress: "taker1", ExecutedHeight: firstFillHeight},
		Pair: pair, Side: int32(future.Long), Quantity: firstFillQty.String(), Price: price.String(), Leverage: leverage,
	}}}
	if _, rc, err := handler.DryRun(taker1Tx, sv); err != nil || rc == nil || rc.Status != "SUCCEED" {
		t.Fatalf("first taker order failed rc=%+v err=%v", rc, err)
	}
	firstDue := firstFillHeight + future.DefaultNoFillOrderTTLBlocks
	if data, exists, _ := sv.Get(keys.KeyFutureOrderExpiry(createDue, "maker_sell_refresh")); exists || len(data) != 0 {
		t.Fatalf("create-height expiry should be removed after first fill: exists=%v data=%x", exists, data)
	}
	if data, exists, _ := sv.Get(keys.KeyFutureOrderExpiry(firstDue, "maker_sell_refresh")); !exists || len(data) != 1 || data[0] != 1 {
		t.Fatalf("first partial fill should write refreshed expiry marker: exists=%v data=%x", exists, data)
	}

	taker2Tx := &pb.AnyTx{Content: &pb.AnyTx_FutureOrderTx{FutureOrderTx: &pb.FutureOrderTx{
		Base: &pb.BaseMessage{TxId: "taker_buy_2", FromAddress: "taker2", ExecutedHeight: secondFillHeight},
		Pair: pair, Side: int32(future.Long), Quantity: secondFillQty.String(), Price: price.String(), Leverage: leverage,
	}}}
	if _, rc, err := handler.DryRun(taker2Tx, sv); err != nil || rc == nil || rc.Status != "SUCCEED" {
		t.Fatalf("second taker order failed rc=%+v err=%v", rc, err)
	}
	secondDue := secondFillHeight + future.DefaultNoFillOrderTTLBlocks
	totalFilled := new(big.Int).Add(firstFillQty, secondFillQty)
	if data, exists, _ := sv.Get(keys.KeyFutureOrderExpiry(secondDue, "maker_sell_refresh")); !exists || len(data) != 1 || data[0] != 1 {
		t.Fatalf("second partial fill should write refreshed expiry marker: exists=%v data=%x", exists, data)
	}
	stateData, exists, _ := sv.Get(keys.KeyFutureOrderState("maker_sell_refresh"))
	if !exists {
		t.Fatal("partially filled maker order should remain open")
	}
	var refreshed pb.FutureOrderState
	if err := proto.Unmarshal(stateData, &refreshed); err != nil {
		t.Fatalf("unmarshal refreshed order: %v", err)
	}
	if refreshed.ExpiryHeight != secondDue || refreshed.FilledBase != totalFilled.String() {
		t.Fatalf("refreshed state mismatch expiry=%d filled=%s", refreshed.ExpiryHeight, refreshed.FilledBase)
	}

	if err := processFutureBlockHooks(sv, firstDue, map[string]bool{}, map[string]bool{}); err != nil {
		t.Fatalf("process stale first expiry: %v", err)
	}
	if _, exists, _ := sv.Get(keys.KeyFutureOrderState("maker_sell_refresh")); !exists {
		t.Fatal("stale first refreshed expiry should not cancel maker order")
	}
	if data, exists, _ := sv.Get(keys.KeyFutureOrderExpiry(firstDue, "maker_sell_refresh")); exists || len(data) != 0 {
		t.Fatalf("stale first refreshed expiry should be deleted: exists=%v data=%x", exists, data)
	}

	if err := processFutureBlockHooks(sv, secondDue, map[string]bool{}, map[string]bool{}); err != nil {
		t.Fatalf("process current second expiry: %v", err)
	}
	if data, exists, _ := sv.Get(keys.KeyFutureOrderState("maker_sell_refresh")); exists || len(data) != 0 {
		t.Fatalf("current refreshed expiry should cancel remaining maker order: exists=%v data=%x", exists, data)
	}
	if data, exists, _ := sv.Get(keys.KeyFutureOrderExpiry(secondDue, "maker_sell_refresh")); exists || len(data) != 0 {
		t.Fatalf("current refreshed expiry should be deleted: exists=%v data=%x", exists, data)
	}
	priceKey, _ := db.PriceToKey128(price.String())
	if data, exists, _ := sv.Get(keys.KeyFutureOrderPriceIndex(pair, pb.OrderSide_SELL, false, priceKey, "maker_sell_refresh")); exists || len(data) != 0 {
		t.Fatalf("cancelled remaining maker should remove price index: exists=%v data=%x", exists, data)
	}
	if _, exists, _ := sv.Get(keys.KeyFutureAccountOrder("maker", pair, pb.FutureOrderStatus_FUTURE_ORDER_CANCELLED, createHeight, "maker_sell_refresh")); !exists {
		t.Fatal("cancelled remaining maker should remain in cancelled account history")
	}
}

func TestFutureOrderSameBlockPartialFillsRefreshExpiryOnce(t *testing.T) {
	sv := newExpiryCountingStateView()
	pair := "BTC_USDT"
	const leverage uint64 = 10
	const createHeight uint64 = 1
	const fillHeight uint64 = 10
	qty := big.NewInt(10 * 1e8)
	firstFillQty := big.NewInt(3 * 1e8)
	secondFillQty := big.NewInt(2 * 1e8)
	price := big.NewInt(50_000 * 1e8)
	initialBalance := big.NewInt(1_000_000_000_000_000)

	sv.Set(keys.KeySpotLastPrice(pair), price.Bytes())
	sv.Set(keys.KeyFutureMarkPrice(pair), price.Bytes())
	giveBalance(t, sv, "maker", "USDT", initialBalance)
	giveBalance(t, sv, "taker1", "USDT", initialBalance)
	giveBalance(t, sv, "taker2", "USDT", initialBalance)
	handler := &FutureOrderTxHandler{}

	makerTx := &pb.AnyTx{Content: &pb.AnyTx_FutureOrderTx{FutureOrderTx: &pb.FutureOrderTx{
		Base: &pb.BaseMessage{TxId: "maker_sell_same_block", FromAddress: "maker", ExecutedHeight: createHeight},
		Pair: pair, Side: int32(future.Short), Quantity: qty.String(), Price: price.String(), Leverage: leverage,
	}}}
	if _, rc, err := handler.DryRun(makerTx, sv); err != nil || rc == nil || rc.Status != "SUCCEED" {
		t.Fatalf("maker order failed rc=%+v err=%v", rc, err)
	}

	for _, tc := range []struct {
		txID  string
		from  string
		qty   *big.Int
		label string
	}{
		{"taker_buy_same_block_1", "taker1", firstFillQty, "first"},
		{"taker_buy_same_block_2", "taker2", secondFillQty, "second"},
	} {
		takerTx := &pb.AnyTx{Content: &pb.AnyTx_FutureOrderTx{FutureOrderTx: &pb.FutureOrderTx{
			Base: &pb.BaseMessage{TxId: tc.txID, FromAddress: tc.from, ExecutedHeight: fillHeight},
			Pair: pair, Side: int32(future.Long), Quantity: tc.qty.String(), Price: price.String(), Leverage: leverage,
		}}}
		if _, rc, err := handler.DryRun(takerTx, sv); err != nil || rc == nil || rc.Status != "SUCCEED" {
			t.Fatalf("%s taker order failed rc=%+v err=%v", tc.label, rc, err)
		}
	}

	fillDue := fillHeight + future.DefaultNoFillOrderTTLBlocks
	fillExpiryKey := keys.KeyFutureOrderExpiry(fillDue, "maker_sell_same_block")
	if sv.expirySets[fillExpiryKey] != 1 || sv.expiryDels[fillExpiryKey] != 0 {
		t.Fatalf("same-block partial fills should refresh expiry once: sets=%d dels=%d", sv.expirySets[fillExpiryKey], sv.expiryDels[fillExpiryKey])
	}
	totalFilled := new(big.Int).Add(firstFillQty, secondFillQty)
	stateData, exists, _ := sv.Get(keys.KeyFutureOrderState("maker_sell_same_block"))
	if !exists {
		t.Fatal("partially filled maker order should remain open")
	}
	var state pb.FutureOrderState
	if err := proto.Unmarshal(stateData, &state); err != nil {
		t.Fatalf("unmarshal maker order: %v", err)
	}
	if state.ExpiryHeight != fillDue || state.FilledBase != totalFilled.String() {
		t.Fatalf("same-block state mismatch expiry=%d filled=%s", state.ExpiryHeight, state.FilledBase)
	}

	if err := processFutureBlockHooks(sv, fillDue, map[string]bool{}, map[string]bool{}); err != nil {
		t.Fatalf("process current same-block expiry: %v", err)
	}
	if data, exists, _ := sv.Get(keys.KeyFutureOrderState("maker_sell_same_block")); exists || len(data) != 0 {
		t.Fatalf("current expiry should cancel remaining maker order: exists=%v data=%x", exists, data)
	}
}

func TestFutureOrderExpirySkipsStaleIndexWhenStateExpiryMoved(t *testing.T) {
	sv := newMemStateView()
	pair := "BTC_USDT"
	const leverage uint64 = 10
	const createHeight uint64 = 1
	qty := big.NewInt(10 * 1e8)
	price := big.NewInt(50_000 * 1e8)
	staleDue := createHeight + future.DefaultNoFillOrderTTLBlocks
	currentDue := staleDue + 10

	sv.Set(keys.KeySpotLastPrice(pair), price.Bytes())
	giveBalance(t, sv, "maker", "USDT", big.NewInt(1_000_000_000_000_000))
	tx := &pb.AnyTx{Content: &pb.AnyTx_FutureOrderTx{FutureOrderTx: &pb.FutureOrderTx{
		Base: &pb.BaseMessage{TxId: "partial", FromAddress: "maker", ExecutedHeight: createHeight},
		Pair: pair, Side: int32(future.Long), Quantity: qty.String(), Price: price.String(), Leverage: leverage,
	}}}
	if _, rc, err := (&FutureOrderTxHandler{}).DryRun(tx, sv); err != nil || rc == nil || rc.Status != "SUCCEED" {
		t.Fatalf("open order failed rc=%+v err=%v", rc, err)
	}

	stateData, exists, _ := sv.Get(keys.KeyFutureOrderState("partial"))
	if !exists {
		t.Fatal("open order state missing")
	}
	var order pb.FutureOrderState
	if err := proto.Unmarshal(stateData, &order); err != nil {
		t.Fatalf("unmarshal open order: %v", err)
	}
	order.FilledBase = "1"
	order.FilledQuote = "1"
	order.ExpiryHeight = currentDue
	data, _ := proto.Marshal(&order)
	sv.Set(keys.KeyFutureOrderState(order.OrderId), data)
	priceKey, _ := db.PriceToKey128(price.String())
	sv.Set(keys.KeyFutureOrderPriceIndex(pair, pb.OrderSide_BUY, false, priceKey, order.OrderId), data)

	processFutureBlockHooks(sv, staleDue, map[string]bool{}, map[string]bool{})
	if _, exists, _ := sv.Get(keys.KeyFutureOrderState(order.OrderId)); !exists {
		t.Fatal("stale expiry index should not cancel open order")
	}
	if data, exists, _ := sv.Get(keys.KeyFutureOrderExpiry(staleDue, order.OrderId)); exists || len(data) != 0 {
		t.Fatalf("stale expiry index should be deleted for matched order: exists=%v data=%x", exists, data)
	}

	sv.Set(keys.KeyFutureOrderExpiry(currentDue, order.OrderId), []byte{1})
	processFutureBlockHooks(sv, currentDue, map[string]bool{}, map[string]bool{})
	if data, exists, _ := sv.Get(keys.KeyFutureOrderState(order.OrderId)); exists || len(data) != 0 {
		t.Fatalf("current expiry index should cancel open order: exists=%v data=%x", exists, data)
	}
}

func conservationTotalForTest(t *testing.T, sv StateView, token, pair string) *big.Int {
	t.Helper()
	total := new(big.Int)
	balances, err := sv.Scan("v1_balance_")
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range balances {
		if extractTokenFromBalanceKey(k) == token {
			total.Add(total, balanceRecordTotal(v))
		}
	}
	positions, err := sv.Scan(keys.KeyFuturePositionPrefix())
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range positions {
		posToken, delta := positionDelta(v, 1)
		if posToken == token {
			total.Add(total, delta)
		}
	}
	if data, exists, _ := sv.Get(keys.KeyFutureInsuranceFund(pair)); exists {
		total.Add(total, new(big.Int).SetBytes(data))
	}
	return total
}
