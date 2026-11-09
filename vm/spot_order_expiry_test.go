package vm

import (
	"dex/db"
	"dex/keys"
	"dex/pb"
	"dex/utils"
	"math/big"
	"testing"

	"google.golang.org/protobuf/proto"
)

func giveSpotAccount(t *testing.T, sv StateView, addr string) {
	t.Helper()
	data, err := proto.Marshal(&pb.Account{Address: addr})
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyAccount(addr), data)
}

func TestSpotOrderAutoExpiresNeverMatchedAfterTTL(t *testing.T) {
	sv := newMemStateView()
	pair := utils.GeneratePairKey("BTC", "USDT")
	const createHeight uint64 = 1
	qty := big.NewInt(1e8)
	price := big.NewInt(50_000 * 1e8)
	initialBalance := big.NewInt(1_000_000_000_000_000)
	frozenQuote, err := QuoteFromBasePrice(qty, price)
	if err != nil {
		t.Fatal(err)
	}
	giveSpotAccount(t, sv, "maker")
	giveBalance(t, sv, "maker", "USDT", initialBalance)
	sv.Set(keys.KeyToken("BTC"), []byte{1})
	sv.Set(keys.KeyToken("USDT"), []byte{1})

	tx := &pb.AnyTx{Content: &pb.AnyTx_OrderTx{OrderTx: &pb.OrderTx{
		Base: &pb.BaseMessage{TxId: "spot_expire", FromAddress: "maker", ExecutedHeight: createHeight},
		Op:   pb.OrderOp_ADD, BaseToken: "BTC", QuoteToken: "USDT",
		Side: pb.OrderSide_BUY, Amount: qty.String(), Price: price.String(),
	}}}
	if _, rc, err := (&OrderTxHandler{}).DryRun(tx, sv); err != nil || rc == nil || rc.Status != "SUCCEED" {
		t.Fatalf("open spot order failed rc=%+v err=%v", rc, err)
	}
	dueHeight := createHeight + DefaultSpotNoFillOrderTTLBlocks
	if _, exists, _ := sv.Get(keys.KeyOrderExpiry(dueHeight, "spot_expire")); !exists {
		t.Fatal("open never-matched spot order should have expiry index")
	}
	priceKey, _ := db.PriceToKey128(price.String())
	if _, exists, _ := sv.Get(keys.KeyOrderPriceIndex(pair, pb.OrderSide_BUY, false, priceKey, "spot_expire")); !exists {
		t.Fatal("open spot order should have price index")
	}
	bal := GetBalance(sv, "maker", "USDT")
	if bal.Balance != new(big.Int).Sub(initialBalance, frozenQuote).String() || bal.OrderFrozenBalance != frozenQuote.String() {
		t.Fatalf("spot order should freeze quote: balance=%s frozen=%s want balance=%s frozen=%s", bal.Balance, bal.OrderFrozenBalance, new(big.Int).Sub(initialBalance, frozenQuote), frozenQuote)
	}

	processSpotBlockHooks(sv, dueHeight-1)
	if _, exists, _ := sv.Get(keys.KeyOrderExpiry(dueHeight, "spot_expire")); !exists {
		t.Fatal("spot order should not expire before ttl height")
	}

	processSpotBlockHooks(sv, dueHeight)
	if data, exists, _ := sv.Get(keys.KeyOrderState("spot_expire")); exists || len(data) != 0 {
		t.Fatalf("expired spot order state should be deleted from StateDB: exists=%v data=%x", exists, data)
	}
	data, exists, _ := sv.Get(keys.KeySpotAccountOrder("maker", pair, pb.OrderStateStatus_ORDER_CANCELLED, createHeight, "spot_expire"))
	if !exists {
		t.Fatal("expired spot order should remain in cancelled account history")
	}
	var state pb.OrderState
	if err := proto.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if state.Status != pb.OrderStateStatus_ORDER_CANCELLED {
		t.Fatalf("expired spot order status=%s, want cancelled", state.Status)
	}
	if data, exists, _ := sv.Get(keys.KeyOrderExpiry(dueHeight, "spot_expire")); exists || len(data) != 0 {
		t.Fatalf("expired spot order expiry index should be deleted: exists=%v data=%x", exists, data)
	}
	if data, exists, _ := sv.Get(keys.KeyOrderPriceIndex(pair, pb.OrderSide_BUY, false, priceKey, "spot_expire")); exists || len(data) != 0 {
		t.Fatalf("expired spot order price index should be deleted: exists=%v data=%x", exists, data)
	}
	if _, exists, _ := sv.Get(keys.KeyAccountOrderItem("maker", "spot_expire")); exists {
		t.Fatal("expired spot order should be removed from legacy open account order item")
	}
	bal = GetBalance(sv, "maker", "USDT")
	if bal.Balance != initialBalance.String() || (bal.OrderFrozenBalance != "" && bal.OrderFrozenBalance != "0") {
		t.Fatalf("expired spot order should unfreeze quote, got balance=%s frozen=%s", bal.Balance, bal.OrderFrozenBalance)
	}
}

func TestSpotOrderExpirySkipsAlreadyMatchedOpenOrder(t *testing.T) {
	sv := newMemStateView()
	pair := utils.GeneratePairKey("BTC", "USDT")
	const createHeight uint64 = 1
	price := big.NewInt(50_000 * 1e8)
	order := &pb.OrderState{
		OrderId: "spot_partial", Owner: "maker", BaseToken: "BTC", QuoteToken: "USDT",
		Side: pb.OrderSide_SELL, Price: price.String(), Amount: "10", FilledBase: "1",
		FilledQuote: "1", Status: pb.OrderStateStatus_ORDER_OPEN, CreateHeight: createHeight,
	}
	data, _ := proto.Marshal(order)
	sv.Set(keys.KeyOrderState(order.OrderId), data)
	priceKey, _ := db.PriceToKey128(price.String())
	sv.Set(keys.KeyOrderPriceIndex(pair, pb.OrderSide_SELL, false, priceKey, order.OrderId), []byte{1})
	dueHeight := createHeight + DefaultSpotNoFillOrderTTLBlocks
	sv.Set(keys.KeyOrderExpiry(dueHeight, order.OrderId), []byte(order.OrderId))

	processSpotBlockHooks(sv, dueHeight)
	if _, exists, _ := sv.Get(keys.KeyOrderState(order.OrderId)); !exists {
		t.Fatal("partially matched spot order should not auto-expire")
	}
	if data, exists, _ := sv.Get(keys.KeyOrderExpiry(dueHeight, order.OrderId)); exists || len(data) != 0 {
		t.Fatalf("stale spot expiry index should be deleted for matched order: exists=%v data=%x", exists, data)
	}
	if _, exists, _ := sv.Get(keys.KeyOrderPriceIndex(pair, pb.OrderSide_SELL, false, priceKey, order.OrderId)); !exists {
		t.Fatal("partially matched spot order should keep price index")
	}
}

func TestSpotOrderSnapshotDeletesFilledStateAndKeepsHistory(t *testing.T) {
	sv := newMemStateView()
	pair := utils.GeneratePairKey("BTC", "USDT")
	const createHeight uint64 = 3
	price := big.NewInt(50_000 * 1e8)
	order := &pb.OrderState{
		OrderId: "spot_filled", Owner: "maker", BaseToken: "BTC", QuoteToken: "USDT",
		Side: pb.OrderSide_SELL, Price: price.String(), Amount: "10", FilledBase: "0",
		Status: pb.OrderStateStatus_ORDER_OPEN, CreateHeight: createHeight,
	}
	if err := writeSpotOrderSnapshot(sv, order); err != nil {
		t.Fatal(err)
	}
	priceKey, _ := db.PriceToKey128(price.String())
	if _, exists, _ := sv.Get(keys.KeyOrderState(order.OrderId)); !exists {
		t.Fatal("open spot order should keep hot order state")
	}
	if _, exists, _ := sv.Get(keys.KeyOrderPriceIndex(pair, pb.OrderSide_SELL, false, priceKey, order.OrderId)); !exists {
		t.Fatal("open spot order should keep price index")
	}

	filled := proto.Clone(order).(*pb.OrderState)
	filled.FilledBase = "10"
	filled.FilledQuote = "50000000000000"
	filled.IsFilled = true
	filled.Status = pb.OrderStateStatus_ORDER_FILLED
	if err := writeSpotOrderSnapshot(sv, filled, order); err != nil {
		t.Fatal(err)
	}
	if data, exists, _ := sv.Get(keys.KeyOrderState(order.OrderId)); exists || len(data) != 0 {
		t.Fatalf("filled spot order state should be deleted from StateDB: exists=%v data=%x", exists, data)
	}
	if data, exists, _ := sv.Get(keys.KeyOrderPriceIndex(pair, pb.OrderSide_SELL, false, priceKey, order.OrderId)); exists || len(data) != 0 {
		t.Fatalf("filled spot order price index should be deleted: exists=%v data=%x", exists, data)
	}
	if _, exists, _ := sv.Get(keys.KeySpotAccountOrder("maker", pair, pb.OrderStateStatus_ORDER_OPEN, createHeight, order.OrderId)); exists {
		t.Fatal("filled spot order should leave open account history index")
	}
	if _, exists, _ := sv.Get(keys.KeySpotAccountOrder("maker", pair, pb.OrderStateStatus_ORDER_FILLED, createHeight, order.OrderId)); !exists {
		t.Fatal("filled spot order should remain in filled account history")
	}
}
