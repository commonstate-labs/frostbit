package vm

import (
	"dex/db"
	"dex/future"
	"dex/keys"
	"dex/pb"
	"math/big"
	"testing"

	"google.golang.org/protobuf/proto"
)

                                                                                                   
                                                                           
  
                                                                      
                                             
                                                     
  
                                                      

func TestTryOrderbookLiquidate_FullFill(t *testing.T) {
	sv := newMemStateView()
	pair := "BTC_USDT"
	const height uint64 = 100

	                                                                    
	sv.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
	sv.Set(keys.KeySpotLastPrice(pair), big.NewInt(50000*1e8).Bytes())
	                                                                           
	                                                          
	markPrice := big.NewInt(49_100 * 1e8)
	sv.Set(keys.KeyFutureMarkPrice(pair), markPrice.Bytes())

	                                                                                                  
	aliceMargin := future.CalcRequiredMargin(big.NewInt(1e8), big.NewInt(50000*1e8), 50)
	alicePos := &pb.FuturePositionState{
		PositionId: "alice_pos", Owner: "alice", Pair: pair,
		Side:       int32(future.Long),
		EntryPrice: big.NewInt(50000 * 1e8).String(),
		Quantity:   big.NewInt(1e8).String(),
		Margin:     aliceMargin.String(),
		Status:     int32(future.StatusOpen),
		OpenHeight: 1,
	}
	writeFuturePosition(t, sv, alicePos)

	                                                                                                                   
	                                                                                                                      
	bobPrice := big.NewInt(49_200 * 1e8)
	bobOrderID := "bob_order"
	bobState := &pb.FutureOrderState{
		OrderId: bobOrderID, Owner: "bob", Pair: pair,
		Side:         1,       
		Price:        bobPrice.String(),
		Amount:       big.NewInt(1e8).String(),
		FilledBase:   "0",
		FilledQuote:  "0",
		Leverage:     10,
		Status:       pb.FutureOrderStatus_FUTURE_ORDER_OPEN,
		OwnerAddress: "bob",
		CreateHeight: 1,
	}
	writeFutureOrderState(t, sv, bobState)
	giveBalance(t, sv, "bob", "USDT", big.NewInt(5_000_000_000_000))
	reserveFutureOrderMarginForTest(t, sv, "bob", "USDT", bobOrderID, future.CalcRequiredMargin(big.NewInt(1e8), bobPrice, bobState.Leverage))
	                                                       
	priceKey67, _ := db.PriceToKey128(bobPrice.String())
	orderData, _ := proto.Marshal(bobState)
	sv.Set(keys.KeyFutureOrderPriceIndex(pair, pb.OrderSide_BUY, false, priceKey67, bobOrderID), orderData)

	                        
	eng := newFutureEngine(sv, height)
	results := tryOrderbookLiquidate(sv, pair, height, eng)
	if len(results) != 1 {
		t.Fatalf("expect 1 orderbook liquidation result, got %d", len(results))
	}
	if results[0].Owner != "alice" {
		t.Fatalf("expect alice liquidated, got %s", results[0].Owner)
	}

	                                            
	if _, exists, _ := sv.Get(keys.KeyFuturePosition("alice_pos")); exists {
		t.Fatalf("liquidated position should leave StateDB main key")
	}
	alicePosBack := readFuturePositionHistory(t, sv, "alice_pos")
	if future.PositionStatus(alicePosBack.Status) != future.StatusLiquidated {
		t.Fatalf("alice position should be Liquidated, got status=%d", alicePosBack.Status)
	}

	                                               
	bobPositions := scanOpenPositions(t, sv, pair)
	var bobPos *pb.FuturePositionState
	for _, p := range bobPositions {
		if p.Owner == "bob" {
			bobPos = p
			break
		}
	}
	if bobPos == nil {
		t.Fatalf("bob should have a new position")
	}
	if future.Side(bobPos.Side) != future.Long {
		t.Fatalf("bob position should be Long, got side=%d", bobPos.Side)
	}
	if bobPos.Quantity != big.NewInt(1e8).String() {
		t.Fatalf("bob position quantity wrong: got %s want %s", bobPos.Quantity, big.NewInt(1e8))
	}

	                                                      
	insData, _, _ := sv.Get(keys.KeyFutureInsuranceFund(pair))
	insBal := new(big.Int).SetBytes(insData)
	if insBal.Sign() <= 0 {
		t.Fatalf("insurance fund should grow by alice's margin loss, got %s", insBal)
	}

	orderData, exists, err := sv.Get(keys.KeyFutureAccountOrder("alice", pair, pb.FutureOrderStatus_FUTURE_ORDER_LIQUIDATED, height, "liq_ob_alice_pos"))
	if err != nil || !exists {
		t.Fatalf("liquidation order history missing: exists=%v err=%v", exists, err)
	}
	var liqOrder pb.FutureOrderState
	if err := proto.Unmarshal(orderData, &liqOrder); err != nil {
		t.Fatal(err)
	}
	if liqOrder.Side != int32(pb.OrderSide_SELL) {
		t.Fatalf("long liquidation history side=%d, want closing SELL=%d", liqOrder.Side, pb.OrderSide_SELL)
	}
}

func TestLiquidationClosePriceRoundsAgainstLiquidatedPosition(t *testing.T) {
	scale := new(big.Int).Set(future.PriceScale)
	low := new(big.Int).Mul(big.NewInt(101), scale)
	high := new(big.Int).Add(low, big.NewInt(1))
	fillQty := new(big.Int).Set(scale)
	sumQty := new(big.Int).Mul(fillQty, big.NewInt(2))
	sumQuote := new(big.Int).Add(
		new(big.Int).Mul(low, fillQty),
		new(big.Int).Mul(high, fillQty),
	)

	longClose := liquidationClosePrice(sumQuote, sumQty, future.Long)
	shortClose := liquidationClosePrice(sumQuote, sumQty, future.Short)
	if longClose.Cmp(low) != 0 {
		t.Fatalf("long close price = %s, want floor %s", longClose, low)
	}
	if shortClose.Cmp(high) != 0 {
		t.Fatalf("short close price = %s, want ceil %s", shortClose, high)
	}

	                                                                            
	                                                                          
	                                                                         
	if new(big.Int).Mul(longClose, sumQty).Cmp(sumQuote) > 0 {
		t.Fatal("long liquidation rounding overstated real fill proceeds")
	}
	if new(big.Int).Mul(shortClose, sumQty).Cmp(sumQuote) < 0 {
		t.Fatal("short liquidation rounding understated real fill cost")
	}
}

func TestTryOrderbookLiquidate_ShortTwoLevelsDoesNotLoseVWAPShortfall(t *testing.T) {
	sv := newMemStateView()
	pair := "BTC_USDT"
	const height uint64 = 100

	scale := new(big.Int).Set(future.PriceScale)
	entryPrice := new(big.Int).Mul(big.NewInt(100), scale)
	lowPrice := new(big.Int).Mul(big.NewInt(101), scale)
	highPrice := new(big.Int).Add(lowPrice, big.NewInt(1))
	fillQty := new(big.Int).Set(scale)
	totalQty := new(big.Int).Mul(fillQty, big.NewInt(2))
	margin := new(big.Int).Mul(big.NewInt(2), scale)

	sv.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
	sv.Set(keys.KeySpotLastPrice(pair), entryPrice.Bytes())
	sv.Set(keys.KeyFutureMarkPrice(pair), lowPrice.Bytes())
	writeFuturePosition(t, sv, &pb.FuturePositionState{
		PositionId: "alice_short_vwap", Owner: "alice", Pair: pair,
		Side:       int32(future.Short),
		EntryPrice: entryPrice.String(),
		Quantity:   totalQty.String(),
		Margin:     margin.String(),
		Status:     int32(future.StatusOpen),
		OpenHeight: 1,
	})

	addMaker := func(owner, orderID string, price *big.Int) {
		t.Helper()
		state := &pb.FutureOrderState{
			OrderId: orderID, Owner: owner, Pair: pair,
			Side:         int32(pb.OrderSide_SELL),
			Price:        price.String(),
			Amount:       fillQty.String(),
			FilledBase:   "0",
			FilledQuote:  "0",
			Leverage:     10,
			Status:       pb.FutureOrderStatus_FUTURE_ORDER_OPEN,
			OwnerAddress: owner,
			CreateHeight: 1,
		}
		writeFutureOrderState(t, sv, state)
		giveBalance(t, sv, owner, "USDT", new(big.Int).Mul(big.NewInt(1_000), scale))
		reserveFutureOrderMarginForTest(t, sv, owner, "USDT", orderID, future.CalcRequiredMargin(fillQty, price, state.Leverage))
		priceKey, err := db.PriceToKey128(price.String())
		if err != nil {
			t.Fatal(err)
		}
		data, err := proto.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
		sv.Set(keys.KeyFutureOrderPriceIndex(pair, pb.OrderSide_SELL, false, priceKey, orderID), data)
	}
	addMaker("bob", "bob_sell_low", lowPrice)
	addMaker("carol", "carol_sell_high", highPrice)

	eng := newFutureEngine(sv, height)
	cfg, err := eng.GetMarketConfig(pair)
	if err != nil {
		t.Fatal(err)
	}
	pos, err := (&svPositionStore{sv: sv}).GetPosition("alice_short_vwap")
	if err != nil || pos == nil {
		t.Fatalf("load short position: pos=%v err=%v", pos, err)
	}
	result := tryOrderbookLiquidatePosition(sv, pair, height, eng, cfg, lowPrice, pos)
	if result == nil {
		t.Fatal("short liquidation did not consume the two maker levels")
	}
	if result.ClosePrice.Cmp(highPrice) != 0 {
		t.Fatalf("short liquidation close price = %s, want adverse ceil %s", result.ClosePrice, highPrice)
	}
	if !result.NeedsADL || result.ADLShortfall == nil || result.ADLShortfall.Cmp(big.NewInt(2)) != 0 {
		t.Fatalf("short liquidation ADL shortfall = %v (needs=%v), want 2", result.ADLShortfall, result.NeedsADL)
	}

	                                                                         
	                                                                          
	                                                       
	oldFloorPnL := future.CalcUnrealizedPnL(future.Short, entryPrice, lowPrice, totalQty)
	if new(big.Int).Add(margin, oldFloorPnL).Sign() != 0 {
		t.Fatalf("regression setup invalid: old floor equity = %s, want 0", new(big.Int).Add(margin, oldFloorPnL))
	}
	sumQuote := new(big.Int).Add(
		new(big.Int).Mul(lowPrice, fillQty),
		new(big.Int).Mul(highPrice, fillQty),
	)
	actualPnL := new(big.Int).Sub(new(big.Int).Mul(entryPrice, totalQty), sumQuote)
	actualPnL.Quo(actualPnL, future.PriceScale)
	if actualEquity := new(big.Int).Add(margin, actualPnL); actualEquity.Cmp(big.NewInt(-1)) != 0 {
		t.Fatalf("regression setup invalid: actual fill equity = %s, want -1", actualEquity)
	}

	tradeData, err := sv.Scan(keys.KeyFutureTradeRecordPrefix(pair))
	if err != nil {
		t.Fatal(err)
	}
	seenPrices := make(map[string]bool)
	for _, data := range tradeData {
		var record pb.TradeRecord
		if err := proto.Unmarshal(data, &record); err == nil {
			seenPrices[record.Price] = true
		}
	}
	if !seenPrices[lowPrice.String()] || !seenPrices[highPrice.String()] {
		t.Fatalf("real fill records lost maker prices: got=%v", seenPrices)
	}
}

func TestTryOrderbookLiquidate_MissingMakerInfoRevertsAttempt(t *testing.T) {
	sv := newMemStateView()
	pair := "BTC_USDT"
	const height uint64 = 100

	sv.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
	sv.Set(keys.KeySpotLastPrice(pair), big.NewInt(50000*1e8).Bytes())
	markPrice := big.NewInt(49_100 * 1e8)
	sv.Set(keys.KeyFutureMarkPrice(pair), markPrice.Bytes())

	aliceMargin := future.CalcRequiredMargin(big.NewInt(1e8), big.NewInt(50000*1e8), 50)
	alicePos := &pb.FuturePositionState{
		PositionId: "alice_pos", Owner: "alice", Pair: pair,
		Side:       int32(future.Long),
		EntryPrice: big.NewInt(50000 * 1e8).String(),
		Quantity:   big.NewInt(1e8).String(),
		Margin:     aliceMargin.String(),
		Status:     int32(future.StatusOpen),
		OpenHeight: 1,
	}
	writeFuturePosition(t, sv, alicePos)

	bobPrice := big.NewInt(49_200 * 1e8)
	bobOrderID := "bob_order_missing_owner_address"
	bobState := &pb.FutureOrderState{
		OrderId: bobOrderID, Owner: "bob", Pair: pair,
		Side:         1,
		Price:        bobPrice.String(),
		Amount:       big.NewInt(1e8).String(),
		FilledBase:   "0",
		FilledQuote:  "0",
		Leverage:     10,
		Status:       pb.FutureOrderStatus_FUTURE_ORDER_OPEN,
		CreateHeight: 1,
	}
	writeFutureOrderState(t, sv, bobState)
	priceKey67, _ := db.PriceToKey128(bobPrice.String())
	orderData, _ := proto.Marshal(bobState)
	sv.Set(keys.KeyFutureOrderPriceIndex(pair, pb.OrderSide_BUY, false, priceKey67, bobOrderID), orderData)

	eng := newFutureEngine(sv, height)
	results := tryOrderbookLiquidate(sv, pair, height, eng)
	if len(results) != 0 {
		t.Fatalf("expect maker-info failure to skip orderbook liquidation, got %d", len(results))
	}
	alicePosBack := readFuturePosition(t, sv, "alice_pos")
	if future.PositionStatus(alicePosBack.Status) != future.StatusOpen {
		t.Fatalf("alice position should stay Open, got status=%d", alicePosBack.Status)
	}
	if _, exists, _ := sv.Get(keys.KeyFutureLastPrice(pair)); exists {
		t.Fatal("failed orderbook liquidation attempt must not update last price")
	}
}

                                                                                                                                                         
func TestCheckFutureLiquidations_OrderbookShortfallRecordedWhenNoADL(t *testing.T) {
	sv := newMemStateView()
	pair := "BTC_USDT"
	const height uint64 = 100

	sv.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
	sv.Set(keys.KeySpotLastPrice(pair), big.NewInt(50000*1e8).Bytes())
	markPrice := big.NewInt(48_000 * 1e8)
	sv.Set(keys.KeyFutureMarkPrice(pair), markPrice.Bytes())

	aliceQty := big.NewInt(1e8)
	aliceMargin := future.CalcRequiredMargin(aliceQty, big.NewInt(50000*1e8), 50)
	alicePos := &pb.FuturePositionState{
		PositionId: "alice_pos", Owner: "alice", Pair: pair,
		Side:       int32(future.Long),
		EntryPrice: big.NewInt(50000 * 1e8).String(),
		Quantity:   aliceQty.String(),
		Margin:     aliceMargin.String(),
		Status:     int32(future.StatusOpen),
		OpenHeight: 1,
	}
	writeFuturePosition(t, sv, alicePos)

	bobOrderID := "bob_order_bad_debt"
	bobState := &pb.FutureOrderState{
		OrderId: bobOrderID, Owner: "bob", Pair: pair,
		Side:         1,
		Price:        markPrice.String(),
		Amount:       aliceQty.String(),
		FilledBase:   "0",
		FilledQuote:  "0",
		Leverage:     10,
		Status:       pb.FutureOrderStatus_FUTURE_ORDER_OPEN,
		OwnerAddress: "bob",
		CreateHeight: 1,
	}
	writeFutureOrderState(t, sv, bobState)
	priceKey, _ := db.PriceToKey128(markPrice.String())
	orderData, _ := proto.Marshal(bobState)
	sv.Set(keys.KeyFutureOrderPriceIndex(pair, pb.OrderSide_BUY, false, priceKey, bobOrderID), orderData)
	giveBalance(t, sv, "bob", "USDT", big.NewInt(5_000_000_000_000))
	reserveFutureOrderMarginForTest(t, sv, "bob", "USDT", bobOrderID, future.CalcRequiredMargin(aliceQty, markPrice, bobState.Leverage))

	checkFutureLiquidations(sv, pair, height)

	shortfallData, exists, _ := sv.Get(keys.KeyFutureInsuranceShortfall(pair))
	if !exists || len(shortfallData) == 0 {
		t.Fatal("orderbook liquidation shortfall should be recorded when ADL has no candidates")
	}
	got := new(big.Int).SetBytes(shortfallData)
	want := big.NewInt(1000 * 1e8)
	if got.Cmp(want) != 0 {
		t.Fatalf("shortfall = %s, want %s", got, want)
	}
}

                                                                                                              
func TestTryOrderbookLiquidate_PartialFill(t *testing.T) {
	sv := newMemStateView()
	pair := "BTC_USDT"
	const height uint64 = 100

	sv.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
	sv.Set(keys.KeySpotLastPrice(pair), big.NewInt(50000*1e8).Bytes())
	markPrice := big.NewInt(49_100 * 1e8)
	sv.Set(keys.KeyFutureMarkPrice(pair), markPrice.Bytes())

	aliceQty := big.NewInt(1e8)           
	aliceMargin := future.CalcRequiredMargin(aliceQty, big.NewInt(50000*1e8), 50)
	alicePos := &pb.FuturePositionState{
		PositionId: "alice_pos", Owner: "alice", Pair: pair,
		Side:       int32(future.Long),
		EntryPrice: big.NewInt(50000 * 1e8).String(),
		Quantity:   aliceQty.String(),
		Margin:     aliceMargin.String(),
		Status:     int32(future.StatusOpen),
		OpenHeight: 1,
	}
	writeFuturePosition(t, sv, alicePos)

	                                                                        
	bobPrice := big.NewInt(49_200 * 1e8)
	bobQty := big.NewInt(60_000_000)           
	bobOrderID := "bob_order_partial"
	bobState := &pb.FutureOrderState{
		OrderId: bobOrderID, Owner: "bob", Pair: pair,
		Side:         1,       
		Price:        bobPrice.String(),
		Amount:       bobQty.String(),
		FilledBase:   "0",
		FilledQuote:  "0",
		Leverage:     10,
		Status:       pb.FutureOrderStatus_FUTURE_ORDER_OPEN,
		OwnerAddress: "bob",
		CreateHeight: 1,
	}
	writeFutureOrderState(t, sv, bobState)
	priceKey, _ := db.PriceToKey128(bobPrice.String())
	orderData, _ := proto.Marshal(bobState)
	sv.Set(keys.KeyFutureOrderPriceIndex(pair, pb.OrderSide_BUY, false, priceKey, bobOrderID), orderData)
	giveBalance(t, sv, "bob", "USDT", big.NewInt(5_000_000_000_000))
	reserveFutureOrderMarginForTest(t, sv, "bob", "USDT", bobOrderID, future.CalcRequiredMargin(bobQty, bobPrice, bobState.Leverage))

	eng := newFutureEngine(sv, height)
	results := tryOrderbookLiquidate(sv, pair, height, eng)
	if len(results) != 1 {
		t.Fatalf("expect 1 partial liquidation result, got %d", len(results))
	}
	                                                  
	if results[0].Quantity.Cmp(bobQty) != 0 {
		t.Fatalf("partial liquidation qty = %s, want %s", results[0].Quantity, bobQty)
	}

	                                                       
	alicePosBack := readFuturePosition(t, sv, "alice_pos")
	if future.PositionStatus(alicePosBack.Status) != future.StatusOpen {
		t.Fatalf("alice should remain Open after partial liq, got status=%d", alicePosBack.Status)
	}
	remainingQty := new(big.Int).Sub(aliceQty, bobQty)
	gotQty, _ := new(big.Int).SetString(alicePosBack.Quantity, 10)
	if gotQty.Cmp(remainingQty) != 0 {
		t.Fatalf("alice remaining qty = %s, want %s", gotQty, remainingQty)
	}
	                                                                     
	gotMargin, _ := new(big.Int).SetString(alicePosBack.Margin, 10)
	portionMargin := future.MulDivFloor(aliceMargin, bobQty, aliceQty)
	expectedMargin := new(big.Int).Sub(aliceMargin, portionMargin)
	if gotMargin.Cmp(expectedMargin) != 0 {
		t.Fatalf("alice remaining margin = %s, want %s", gotMargin, expectedMargin)
	}

	                                         
	bobPositions := scanOpenPositions(t, sv, pair)
	var bobPos *pb.FuturePositionState
	for _, p := range bobPositions {
		if p.Owner == "bob" {
			bobPos = p
			break
		}
	}
	if bobPos == nil {
		t.Fatalf("bob should have a new partial position")
	}
	if bobPos.Quantity != bobQty.String() {
		t.Fatalf("bob position qty = %s, want %s", bobPos.Quantity, bobQty)
	}

	                                                                             
	insData, _, _ := sv.Get(keys.KeyFutureInsuranceFund(pair))
	insBal := new(big.Int).SetBytes(insData)
	if insBal.Sign() <= 0 {
		t.Fatalf("insurance fund should grow from orderbook segment, got %s", insBal)
	}
}

func TestCheckFutureLiquidations_PartialOrderbookThenMarkFallback(t *testing.T) {
	sv := newMemStateView()
	pair := "BTC_USDT"
	const height uint64 = 100

	sv.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
	sv.Set(keys.KeySpotLastPrice(pair), big.NewInt(50000*1e8).Bytes())
	markPrice := big.NewInt(49_100 * 1e8)
	sv.Set(keys.KeyFutureMarkPrice(pair), markPrice.Bytes())

	aliceQty := big.NewInt(1e8)
	aliceMargin := future.CalcRequiredMargin(aliceQty, big.NewInt(50000*1e8), 50)
	alicePos := &pb.FuturePositionState{
		PositionId: "alice_pos", Owner: "alice", Pair: pair,
		Side:       int32(future.Long),
		EntryPrice: big.NewInt(50000 * 1e8).String(),
		Quantity:   aliceQty.String(),
		Margin:     aliceMargin.String(),
		Status:     int32(future.StatusOpen),
		OpenHeight: 1,
	}
	writeFuturePosition(t, sv, alicePos)

	bobPrice := big.NewInt(49_200 * 1e8)
	bobQty := big.NewInt(60_000_000)
	bobOrderID := "bob_order_partial_hook"
	bobState := &pb.FutureOrderState{
		OrderId: bobOrderID, Owner: "bob", Pair: pair,
		Side:         1,
		Price:        bobPrice.String(),
		Amount:       bobQty.String(),
		FilledBase:   "0",
		FilledQuote:  "0",
		Leverage:     10,
		Status:       pb.FutureOrderStatus_FUTURE_ORDER_OPEN,
		OwnerAddress: "bob",
		CreateHeight: 1,
	}
	writeFutureOrderState(t, sv, bobState)
	priceKey, _ := db.PriceToKey128(bobPrice.String())
	orderData, _ := proto.Marshal(bobState)
	sv.Set(keys.KeyFutureOrderPriceIndex(pair, pb.OrderSide_BUY, false, priceKey, bobOrderID), orderData)
	giveBalance(t, sv, "bob", "USDT", big.NewInt(5_000_000_000_000))
	reserveFutureOrderMarginForTest(t, sv, "bob", "USDT", bobOrderID, future.CalcRequiredMargin(bobQty, bobPrice, bobState.Leverage))

	checkFutureLiquidations(sv, pair, height)

	if _, exists, _ := sv.Get(keys.KeyFuturePosition("alice_pos")); exists {
		t.Fatalf("liquidated position should leave StateDB main key")
	}
	aliceBack := readFuturePositionHistory(t, sv, "alice_pos")
	if future.PositionStatus(aliceBack.Status) != future.StatusLiquidated {
		t.Fatalf("alice remaining position should be mark-price liquidated, got status=%d qty=%s", aliceBack.Status, aliceBack.Quantity)
	}
	if data, exists, _ := sv.Get(keys.KeyFutureOrderState("liq_ob_alice_pos")); exists || len(data) != 0 {
		t.Fatalf("orderbook liquidation order should be deleted from StateDB: exists=%v data=%x", exists, data)
	}
	if _, exists, _ := sv.Get(keys.KeyFutureAccountOrder("alice", pair, pb.FutureOrderStatus_FUTURE_ORDER_LIQUIDATED, height, "liq_ob_alice_pos")); !exists {
		t.Fatal("orderbook liquidation order history should remain in KV account index")
	}
	if data, exists, _ := sv.Get(keys.KeyFutureOrderState("liq_alice_pos")); exists || len(data) != 0 {
		t.Fatalf("mark-price liquidation order should be deleted from StateDB: exists=%v data=%x", exists, data)
	}
	if _, exists, _ := sv.Get(keys.KeyFutureAccountOrder("alice", pair, pb.FutureOrderStatus_FUTURE_ORDER_LIQUIDATED, height, "liq_alice_pos")); !exists {
		t.Fatal("mark-price liquidation order history should remain in KV account index")
	}
}

                                                                                                             
                                                                   
func TestTryOrderbookLiquidate_NoMatch(t *testing.T) {
	sv := newMemStateView()
	pair := "BTC_USDT"
	const height uint64 = 100

	sv.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
	sv.Set(keys.KeySpotLastPrice(pair), big.NewInt(50000*1e8).Bytes())
	markPrice := big.NewInt(48_500 * 1e8)
	sv.Set(keys.KeyFutureMarkPrice(pair), markPrice.Bytes())

	aliceMargin := future.CalcRequiredMargin(big.NewInt(1e8), big.NewInt(50000*1e8), 50)
	alicePos := &pb.FuturePositionState{
		PositionId: "alice_pos", Owner: "alice", Pair: pair,
		Side:       int32(future.Long),
		EntryPrice: big.NewInt(50000 * 1e8).String(),
		Quantity:   big.NewInt(1e8).String(),
		Margin:     aliceMargin.String(),
		Status:     int32(future.StatusOpen),
		OpenHeight: 1,
	}
	writeFuturePosition(t, sv, alicePos)

	                                            
	eng := newFutureEngine(sv, height)
	results := tryOrderbookLiquidate(sv, pair, height, eng)
	if len(results) != 0 {
		t.Fatalf("expect 0 results (no orderbook match), got %d", len(results))
	}
	                                                                   
	alicePosBack := readFuturePosition(t, sv, "alice_pos")
	if future.PositionStatus(alicePosBack.Status) != future.StatusOpen {
		t.Fatalf("alice position should stay Open, got status=%d", alicePosBack.Status)
	}
}

func TestApplyLiquidationSelfMarginTopupResetsBasisOnlyForPositiveAmount(t *testing.T) {
	pos := &future.Position{
		Margin:            big.NewInt(100),
		MarginBasisAmount: big.NewInt(80),
		MarginBasisClosed: big.NewInt(3),
	}
	if applyLiquidationSelfMarginTopup(pos, new(big.Int)) {
		t.Fatal("zero topup reported a position mutation")
	}
	if pos.Margin.Cmp(big.NewInt(100)) != 0 || pos.MarginBasisAmount.Cmp(big.NewInt(80)) != 0 || pos.MarginBasisClosed.Cmp(big.NewInt(3)) != 0 {
		t.Fatalf("zero topup mutated margin basis: margin=%s basis=%s/%s", pos.Margin, pos.MarginBasisAmount, pos.MarginBasisClosed)
	}

	if !applyLiquidationSelfMarginTopup(pos, big.NewInt(20)) {
		t.Fatal("positive topup did not report a position mutation")
	}
	if pos.Margin.Cmp(big.NewInt(120)) != 0 || pos.MarginBasisAmount.Cmp(pos.Margin) != 0 || pos.MarginBasisClosed.Sign() != 0 {
		t.Fatalf("positive topup did not reset margin basis: margin=%s basis=%s/%s", pos.Margin, pos.MarginBasisAmount, pos.MarginBasisClosed)
	}
}

func TestCheckFutureLiquidations_OwnMakerTopsUpMargin(t *testing.T) {
	sv := newMemStateView()
	pair := "BTC_USDT"
	const height uint64 = 100

	sv.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
	sv.Set(keys.KeySpotLastPrice(pair), big.NewInt(50000*1e8).Bytes())
	markPrice := big.NewInt(49_100 * 1e8)
	sv.Set(keys.KeyFutureMarkPrice(pair), markPrice.Bytes())

	aliceQty := big.NewInt(1e8)
	aliceMargin := future.CalcRequiredMargin(aliceQty, big.NewInt(50000*1e8), 50)
	alicePos := &pb.FuturePositionState{
		PositionId: "alice_pos", Owner: "alice", Pair: pair,
		Side:       int32(future.Long),
		EntryPrice: big.NewInt(50000 * 1e8).String(),
		Quantity:   aliceQty.String(),
		Margin:     aliceMargin.String(),
		Status:     int32(future.StatusOpen),
		OpenHeight: 1,
	}
	writeFuturePosition(t, sv, alicePos)

	                                                                                             
	                                                                                                        
	aliceOrderID := "alice_own_buy"
	alicePrice := big.NewInt(49_200 * 1e8)
	aliceOrder := &pb.FutureOrderState{
		OrderId: aliceOrderID, Owner: "alice", Pair: pair,
		Side:         int32(pb.OrderSide_BUY),
		Price:        alicePrice.String(),
		Amount:       aliceQty.String(),
		FilledBase:   "0",
		FilledQuote:  "0",
		Leverage:     10,
		Status:       pb.FutureOrderStatus_FUTURE_ORDER_OPEN,
		OwnerAddress: "alice",
		CreateHeight: 1,
	}
	writeFutureOrderState(t, sv, aliceOrder)
	aliceOrderMargin := future.CalcRequiredMargin(aliceQty, alicePrice, aliceOrder.Leverage)
	reserveFutureOrderMarginForTest(t, sv, "alice", "USDT", aliceOrderID, aliceOrderMargin)
	priceKey, _ := db.PriceToKey128(alicePrice.String())
	orderData, _ := proto.Marshal(aliceOrder)
	indexKey := keys.KeyFutureOrderPriceIndex(pair, pb.OrderSide_BUY, false, priceKey, aliceOrderID)
	sv.Set(indexKey, orderData)

	before := futureQuoteConservationTotal(t, sv, pair, "USDT")
	checkFutureLiquidations(sv, pair, height)
	after := futureQuoteConservationTotal(t, sv, pair, "USDT")
	if after.Cmp(before) != 0 {
		t.Fatalf("conservation total changed after own-maker topup: before=%s after=%s", before, after)
	}
	if _, exists, _ := sv.Get(keys.KeyFutureOrderReservedMargin(aliceOrderID)); exists {
		t.Fatal("own order reserved margin should be consumed")
	}
	if _, exists, _ := sv.Get(keys.KeyFutureOrderState(aliceOrderID)); exists {
		t.Fatal("own order should be removed from open order state")
	}
	if _, exists, _ := sv.Get(indexKey); exists {
		t.Fatal("own order should be removed from price index")
	}
	if _, exists, _ := sv.Get(keys.KeyFutureAccountOrder("alice", pair, pb.FutureOrderStatus_FUTURE_ORDER_CANCELLED, 1, aliceOrderID)); !exists {
		t.Fatal("own order should remain in cancelled account history")
	}
	back := readFuturePosition(t, sv, "alice_pos")
	if future.PositionStatus(back.Status) != future.StatusOpen {
		t.Fatalf("alice position should remain Open after self topup, got status=%d", back.Status)
	}
	gotMargin := parseBigOrZero(back.Margin)
	wantMargin := new(big.Int).Add(aliceMargin, aliceOrderMargin)
	if gotMargin.Cmp(wantMargin) != 0 {
		t.Fatalf("alice margin = %s, want %s", gotMargin, wantMargin)
	}
	if gotBasis := parseBigOrZero(back.MarginBasisAmount); gotBasis.Cmp(gotMargin) != 0 {
		t.Fatalf("alice margin basis amount = %s, want topped-up margin %s", gotBasis, gotMargin)
	}
	if gotClosed := parseBigOrZero(back.MarginBasisClosed); gotClosed.Sign() != 0 {
		t.Fatalf("alice margin basis closed = %s, want 0 after topup", gotClosed)
	}
	if future.ShouldLiquidate(gotMargin, future.Long, big.NewInt(50000*1e8), markPrice, aliceQty, future.DefaultMaintenanceMarginRateBps) {
		t.Fatal("alice position should be healthy after self topup")
	}
}

func TestApplyLiquidationSelfMarginTopups_CurrentHeightMakerIsIgnoredAndRemainsOpen(t *testing.T) {
	sv := newMemStateView()
	pair := selfTradePair
	const height uint64 = 100
	markPrice := big.NewInt(49_100 * 1e8)
	sv.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
	sv.Set(keys.KeySpotLastPrice(pair), big.NewInt(50_000*1e8).Bytes())
	sv.Set(keys.KeyFutureMarkPrice(pair), markPrice.Bytes())

	qty := big.NewInt(1e8)
	entryPrice := big.NewInt(50_000 * 1e8)
	margin := future.CalcRequiredMargin(qty, entryPrice, 50)
	writeFuturePosition(t, sv, &pb.FuturePositionState{
		PositionId: "current_height_topup_pos", Owner: "alice", Pair: pair,
		Side: int32(future.Long), EntryPrice: entryPrice.String(), Quantity: qty.String(),
		Margin: margin.String(), Status: int32(future.StatusOpen), OpenHeight: 1,
	})

	orderID := "current_height_own_buy"
	orderPrice := big.NewInt(49_200 * 1e8)
	order := &pb.FutureOrderState{
		OrderId: orderID, Owner: "alice", Pair: pair,
		Side: int32(pb.OrderSide_BUY), Price: orderPrice.String(), Amount: qty.String(),
		FilledBase: "0", FilledQuote: "0", Leverage: 10,
		Status: pb.FutureOrderStatus_FUTURE_ORDER_OPEN, OwnerAddress: "alice", CreateHeight: height,
	}
	writeFutureOrderState(t, sv, order)
	reserve := future.CalcRequiredMargin(qty, orderPrice, order.Leverage)
	reserveFutureOrderMarginForTest(t, sv, "alice", "USDT", orderID, reserve)
	priceKey, _ := db.PriceToKey128(orderPrice.String())
	orderData, _ := proto.Marshal(order)
	sv.Set(keys.KeyFutureOrderPriceIndex(pair, pb.OrderSide_BUY, false, priceKey, orderID), orderData)

	eng := newFutureEngineWithActivity(sv, height)
	cfg, err := eng.GetMarketConfig(pair)
	if err != nil {
		t.Fatalf("get market config: %v", err)
	}
	pos, err := (&svPositionStore{sv: sv}).GetPosition("current_height_topup_pos")
	if err != nil || pos == nil {
		t.Fatalf("read position: pos=%+v err=%v", pos, err)
	}
	healthy, err := applyLiquidationSelfMarginTopupsLocked(sv, pair, height, eng, cfg, markPrice, pos)
	if err != nil {
		t.Fatalf("apply liquidation self topups: %v", err)
	}
	if healthy {
		t.Fatal("current-height maker must not rescue the position")
	}
	back := readFuturePosition(t, sv, "current_height_topup_pos")
	if back.Margin != margin.String() {
		t.Fatalf("current-height maker changed position margin: got=%s want=%s", back.Margin, margin)
	}
	assertFutureMakerOpen(t, sv, orderID, pb.OrderSide_BUY, orderPrice, "0")
	if _, exists, err := sv.Get(keys.KeyFutureOrderReservedMargin(orderID)); err != nil || !exists {
		t.Fatalf("current-height maker reserve must remain: exists=%v err=%v", exists, err)
	}
}

func TestApplyLiquidationSelfMarginTopups_OwnMakerDoesNotConsumeExternalSweepCapacity(t *testing.T) {
	sv, eng, cfg, pos, markPrice := newLiquidationTopupScanFixture(t, "own_capacity_pos")
	qty := new(big.Int).Set(pos.Quantity)
	selfQty := new(big.Int).Quo(new(big.Int).Set(qty), big.NewInt(4))

	                                                                            
	                                                                             
	                                                                          
	                                              
	placeLiquidationTopupScanMaker(t, sv, "own_capacity_1", pos.Owner, big.NewInt(49_000_000), selfQty, 100, 1, nil)
	placeLiquidationTopupScanMaker(t, sv, "own_capacity_2", pos.Owner, big.NewInt(48_900_000), selfQty, 100, 1, nil)
	external := placeLiquidationTopupScanMaker(t, sv, "own_capacity_external", "bob", big.NewInt(48_800_000), qty, 10, 1, nil)
	placeLiquidationTopupScanMaker(t, sv, "own_capacity_behind", pos.Owner, big.NewInt(48_700_000), qty, 100, 1, nil)
	if !futureMakerTradeExecutable(sv, selfTradePair, pos.Owner, external, qty, false) {
		t.Fatal("external fixture maker must be executable")
	}

	healthy, err := applyLiquidationSelfMarginTopupsLocked(sv, selfTradePair, 100, eng, cfg, markPrice, pos)
	if err != nil {
		t.Fatalf("apply liquidation self topups: %v", err)
	}
	if healthy {
		t.Fatalf("two self-maker topups should not yet rescue the position: margin=%s", pos.Margin)
	}
	assertLiquidationTopupMakerConsumed(t, sv, "own_capacity_1")
	assertLiquidationTopupMakerConsumed(t, sv, "own_capacity_2")
	assertFutureMakerOpen(t, sv, "own_capacity_external", pb.OrderSide_BUY, big.NewInt(48_800_000), "0")
	assertFutureMakerOpen(t, sv, "own_capacity_behind", pb.OrderSide_BUY, big.NewInt(48_700_000), "0")
}

func TestApplyLiquidationSelfMarginTopups_UnexecutableExternalMakersDoNotConsumeCapacity(t *testing.T) {
	sv, eng, cfg, pos, markPrice := newLiquidationTopupScanFixture(t, "invalid_external_pos")
	qty := new(big.Int).Set(pos.Quantity)

	zeroNotionalPrice := big.NewInt(49_000_000)
	placeLiquidationTopupScanMaker(t, sv, "zero_notional_external", "bob", zeroNotionalPrice, big.NewInt(1), 10, 1, big.NewInt(1))
	insufficientPrice := big.NewInt(48_900_000)
	placeLiquidationTopupScanMaker(t, sv, "insufficient_external", "carol", insufficientPrice, qty, 10, 1, big.NewInt(1))
	deepOwnPrice := big.NewInt(48_800_000)
	placeLiquidationTopupScanMaker(t, sv, "own_after_invalid", pos.Owner, deepOwnPrice, qty, 100, 1, nil)
	validExternalPrice := big.NewInt(48_700_000)
	placeLiquidationTopupScanMaker(t, sv, "valid_external", "dave", validExternalPrice, qty, 10, 1, nil)

	healthy, err := applyLiquidationSelfMarginTopupsLocked(sv, selfTradePair, 100, eng, cfg, markPrice, pos)
	if err != nil {
		t.Fatalf("apply liquidation self topups: %v", err)
	}
	if healthy {
		t.Fatal("one self-maker topup should not rescue the position")
	}
	assertLiquidationTopupMakerConsumed(t, sv, "own_after_invalid")
	assertFutureMakerOpen(t, sv, "zero_notional_external", pb.OrderSide_BUY, zeroNotionalPrice, "0")
	assertFutureMakerOpen(t, sv, "insufficient_external", pb.OrderSide_BUY, insufficientPrice, "0")
	assertFutureMakerOpen(t, sv, "valid_external", pb.OrderSide_BUY, validExternalPrice, "0")
}

func TestApplyLiquidationSelfMarginTopups_ReachesDeeperOwnMakersAndRescues(t *testing.T) {
	sv, eng, cfg, pos, markPrice := newLiquidationTopupScanFixture(t, "deep_own_rescue_pos")
	qty := new(big.Int).Set(pos.Quantity)

	                                                                           
	                                                                           
	                                                                      
	halfQty := new(big.Int).Quo(new(big.Int).Set(qty), big.NewInt(2))
	placeLiquidationTopupScanMaker(t, sv, "deep_external_half", "bob", big.NewInt(49_000_000), halfQty, 10, 1, nil)
	placeLiquidationTopupScanMaker(t, sv, "deep_own_1", pos.Owner, big.NewInt(48_900_000), qty, 100, 1, nil)
	placeLiquidationTopupScanMaker(t, sv, "deep_own_2", pos.Owner, big.NewInt(48_800_000), qty, 100, 1, nil)
	placeLiquidationTopupScanMaker(t, sv, "deep_own_3", pos.Owner, big.NewInt(48_700_000), qty, 100, 1, nil)

	healthy, err := applyLiquidationSelfMarginTopupsLocked(sv, selfTradePair, 100, eng, cfg, markPrice, pos)
	if err != nil {
		t.Fatalf("apply liquidation self topups: %v", err)
	}
	if !healthy {
		t.Fatal("deeper self-maker collateral should rescue the position")
	}
	for _, orderID := range []string{"deep_own_1", "deep_own_2", "deep_own_3"} {
		assertLiquidationTopupMakerConsumed(t, sv, orderID)
	}
	assertFutureMakerOpen(t, sv, "deep_external_half", pb.OrderSide_BUY, big.NewInt(49_000_000), "0")
	persisted, err := (&svPositionStore{sv: sv}).GetPosition("deep_own_rescue_pos")
	if err != nil || persisted == nil {
		t.Fatalf("read persisted rescued position: pos=%+v err=%v", persisted, err)
	}
	if future.ShouldLiquidatePosition(persisted, persisted.Margin, markPrice, cfg.MaintenanceMarginRateOrDefault()) {
		t.Fatal("persisted position remains liquidatable after deep self-maker topups")
	}
}

func newLiquidationTopupScanFixture(t *testing.T, positionID string) (StateView, *future.Engine, *future.MarketConfig, *future.Position, *big.Int) {
	t.Helper()
	sv := newMemStateView()
	const height uint64 = 100
	entryPrice := big.NewInt(50_000_000)
	markPrice := big.NewInt(48_000_000)
	qty := big.NewInt(1e8)
	sv.Set(keys.KeyFutureMarketConfig(selfTradePair), []byte(selfTradePair))
	sv.Set(keys.KeySpotLastPrice(selfTradePair), entryPrice.Bytes())
	sv.Set(keys.KeyFutureMarkPrice(selfTradePair), markPrice.Bytes())
	writeFuturePosition(t, sv, &pb.FuturePositionState{
		PositionId: positionID, Owner: "alice", Pair: selfTradePair,
		Side: int32(future.Long), EntryPrice: entryPrice.String(), Quantity: qty.String(),
		Margin: future.CalcRequiredMargin(qty, entryPrice, 50).String(),
		Status: int32(future.StatusOpen), OpenHeight: 1,
	})
	eng := newFutureEngineWithActivity(sv, height)
	cfg, err := eng.GetMarketConfig(selfTradePair)
	if err != nil {
		t.Fatalf("get market config: %v", err)
	}
	pos, err := (&svPositionStore{sv: sv}).GetPosition(positionID)
	if err != nil || pos == nil {
		t.Fatalf("read position: pos=%+v err=%v", pos, err)
	}
	if !future.ShouldLiquidatePosition(pos, pos.Margin, markPrice, cfg.MaintenanceMarginRateOrDefault()) {
		t.Fatal("fixture position must start liquidatable")
	}
	return sv, eng, cfg, pos, markPrice
}

func placeLiquidationTopupScanMaker(t *testing.T, sv StateView, orderID, owner string, price, amount *big.Int, leverage uint64, createHeight uint64, reserveOverride *big.Int) *makerOrderSnapshot {
	t.Helper()
	state := &pb.FutureOrderState{
		OrderId: orderID, Owner: owner, Pair: selfTradePair,
		Side: int32(future.Long), Price: price.String(), Amount: amount.String(),
		FilledBase: "0", FilledQuote: "0", Leverage: leverage,
		Status: pb.FutureOrderStatus_FUTURE_ORDER_OPEN, OwnerAddress: owner, CreateHeight: createHeight,
	}
	writeFutureOrderState(t, sv, state)
	reserve := reserveOverride
	if reserve == nil {
		reserve = future.CalcRequiredMargin(amount, price, leverage)
	}
	reserveFutureOrderMarginForTest(t, sv, owner, "USDT", orderID, reserve)
	priceKey, err := db.PriceToKey128(price.String())
	if err != nil {
		t.Fatalf("price key: %v", err)
	}
	data, err := proto.Marshal(state)
	if err != nil {
		t.Fatalf("marshal maker: %v", err)
	}
	sv.Set(keys.KeyFutureOrderPriceIndex(selfTradePair, pb.OrderSide_BUY, false, priceKey, orderID), data)
	return &makerOrderSnapshot{
		remain: new(big.Int).Set(amount), price: new(big.Int).Set(price), owner: owner,
		side: int32(future.Long), leverage: leverage, future: state,
	}
}

func assertLiquidationTopupMakerConsumed(t *testing.T, sv StateView, orderID string) {
	t.Helper()
	if _, exists, err := sv.Get(keys.KeyFutureOrderState(orderID)); err != nil || exists {
		t.Fatalf("self maker %s must leave open state: exists=%v err=%v", orderID, exists, err)
	}
	if _, exists, err := sv.Get(keys.KeyFutureOrderReservedMargin(orderID)); err != nil || exists {
		t.Fatalf("self maker %s reserve must be consumed: exists=%v err=%v", orderID, exists, err)
	}
}

                                                                                            
                                                                                                        
                                                                                              
                              
  
                                                                                                      
                                                                              
func TestTryOrderbookLiquidate_BufferPositionLastCumUntouched(t *testing.T) {
	sv := newMemStateView()
	pair := "BTC_USDT"
	const height uint64 = 100

	sv.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
	sv.Set(keys.KeySpotLastPrice(pair), big.NewInt(50000*1e8).Bytes())
	                                                                      
	markPrice := big.NewInt(50_000 * 1e8)
	sv.Set(keys.KeyFutureMarkPrice(pair), markPrice.Bytes())

	                                                                         
	cumFunding := &pb.FutureCumulativeFunding{
		Pair:            pair,
		CumulativeLong:  "30",
		CumulativeShort: "30",
	}
	cfData, _ := proto.Marshal(cumFunding)
	sv.Set(keys.KeyFutureCumulativeFunding(pair), cfData)

	                                                     
	aliceQty := big.NewInt(1e8)
	aliceMargin := future.CalcRequiredMargin(aliceQty, big.NewInt(50000*1e8), 2)
	alicePos := &pb.FuturePositionState{
		PositionId: "alice_pos", Owner: "alice", Pair: pair,
		Side:                  int32(future.Long),
		EntryPrice:            big.NewInt(50000 * 1e8).String(),
		Quantity:              aliceQty.String(),
		Margin:                aliceMargin.String(),
		Status:                int32(future.StatusOpen),
		OpenHeight:            1,
		LastCumulativeFunding: "0",
	}
	writeFuturePosition(t, sv, alicePos)

	eng := newFutureEngine(sv, height)
	                                                                                                               
	if eng != nil {
		cfg, _ := eng.GetMarketConfig(pair)
		liqPrice := future.CalcLiquidationPrice(future.Long, big.NewInt(50000*1e8), aliceMargin, aliceQty, cfg.MaintenanceMarginRateOrDefault())
		idx := &svLiquidationIndex{sv: sv}
		_ = idx.Upsert("alice_pos", pair, future.Long, liqPrice)
	}

	_ = tryOrderbookLiquidate(sv, pair, height, eng)

	                                                                                         
	back := readFuturePosition(t, sv, "alice_pos")
	if back.LastCumulativeFunding != "0" {
		t.Fatalf("buffer position lastCum polluted: want 0, got %s", back.LastCumulativeFunding)
	}
}

                                     

func TestTryOrderbookLiquidate_LostMarginFundsProfitableMakerClose(t *testing.T) {
	sv := newMemStateView()
	pair := "BTC_USDT"
	const height uint64 = 100

	scale := new(big.Int).Set(future.PriceScale)
	qty := new(big.Int).Set(scale)
	entryPrice := new(big.Int).Mul(big.NewInt(100), scale)
	closePrice := new(big.Int).Mul(big.NewInt(98), scale)
	liquidatedMargin := new(big.Int).Set(scale)
	makerMargin := new(big.Int).Mul(big.NewInt(10), scale)
	wantMakerPnL := new(big.Int).Mul(big.NewInt(2), scale)

	sv.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
	sv.Set(keys.KeySpotLastPrice(pair), entryPrice.Bytes())
	sv.Set(keys.KeyFutureMarkPrice(pair), closePrice.Bytes())
	                                                                           
	                                                                           
	                                                                            
	                  
	sv.Set(keys.KeyFutureInsuranceFund(pair), new(big.Int).Set(scale).Bytes())
	writeFuturePosition(t, sv, &pb.FuturePositionState{
		PositionId: "alice_liquidated_long", Owner: "alice", Pair: pair,
		Side: int32(future.Long), EntryPrice: entryPrice.String(),
		Quantity: qty.String(), Margin: liquidatedMargin.String(),
		Status: int32(future.StatusOpen), OpenHeight: 1,
	})
	writeFuturePosition(t, sv, &pb.FuturePositionState{
		PositionId: "bob_profitable_short", Owner: "bob", Pair: pair,
		Side: int32(future.Short), EntryPrice: entryPrice.String(),
		Quantity: qty.String(), Margin: makerMargin.String(),
		Status: int32(future.StatusOpen), OpenHeight: 1,
	})

	orderID := "bob_close_short_maker"
	order := &pb.FutureOrderState{
		OrderId: orderID, Owner: "bob", OwnerAddress: "bob", Pair: pair,
		Side: int32(future.Long), Price: closePrice.String(),
		Amount: qty.String(), FilledBase: "0", FilledQuote: "0",
		Leverage: 10, Status: pb.FutureOrderStatus_FUTURE_ORDER_OPEN,
		CreateHeight: 1,
	}
	writeFutureOrderState(t, sv, order)
	giveBalance(t, sv, "bob", "USDT", new(big.Int))
	reserve := future.CalcRequiredMargin(qty, closePrice, order.Leverage)
	reserveFutureOrderMarginForTest(t, sv, "bob", "USDT", orderID, reserve)
	priceKey, err := db.PriceToKey128(closePrice.String())
	if err != nil {
		t.Fatal(err)
	}
	orderData, err := proto.Marshal(order)
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyFutureOrderPriceIndex(pair, pb.OrderSide_BUY, false, priceKey, orderID), orderData)

	eng := newFutureEngine(sv, height)
	cfg, err := eng.GetMarketConfig(pair)
	if err != nil {
		t.Fatal(err)
	}
	pos, err := (&svPositionStore{sv: sv}).GetPosition("alice_liquidated_long")
	if err != nil || pos == nil {
		t.Fatalf("load liquidated position: pos=%v err=%v", pos, err)
	}
	result := tryOrderbookLiquidatePosition(sv, pair, height, eng, cfg, closePrice, pos)
	if result == nil {
		t.Fatal("expected orderbook liquidation")
	}
	if result.NeedsADL || result.ADLShortfall == nil || result.ADLShortfall.Sign() != 0 {
		t.Fatalf("maker payout already absorbed liquidation gap, got needsADL=%v shortfall=%v", result.NeedsADL, result.ADLShortfall)
	}

	bob := readFuturePositionHistory(t, sv, "bob_profitable_short")
	if bob.RealizedPnl != wantMakerPnL.String() {
		t.Fatalf("maker realized PnL=%s, want %s funded by liquidated margin", bob.RealizedPnl, wantMakerPnL)
	}
	if data, exists, err := sv.Get(keys.KeyFutureInsuranceShortfall(pair)); err != nil {
		t.Fatal(err)
	} else if exists && new(big.Int).SetBytes(data).Sign() != 0 {
		t.Fatalf("profitable maker close recorded shortfall: %s", new(big.Int).SetBytes(data))
	}
	if data, exists, err := sv.Get(keys.KeyFutureInsuranceFund(pair)); err != nil {
		t.Fatal(err)
	} else if !exists || new(big.Int).SetBytes(data).Sign() != 0 {
		t.Fatalf("insurance after margin-funded maker payout: exists=%v balance=%s, want 0", exists, new(big.Int).SetBytes(data))
	}
}

func TestTryOrderbookLiquidate_SeizedMarginPayoutDoesNotEraseADLGap(t *testing.T) {
	sv := newMemStateView()
	pair := "BTC_USDT"
	const height uint64 = 100

	scale := new(big.Int).Set(future.PriceScale)
	qty := new(big.Int).Set(scale)
	targetEntry := new(big.Int).Mul(big.NewInt(100), scale)
	makerEntry := new(big.Int).Mul(big.NewInt(99), scale)
	closePrice := new(big.Int).Mul(big.NewInt(98), scale)
	margin := new(big.Int).Set(scale)

	sv.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
	sv.Set(keys.KeySpotLastPrice(pair), targetEntry.Bytes())
	sv.Set(keys.KeyFutureMarkPrice(pair), closePrice.Bytes())
	writeFuturePosition(t, sv, &pb.FuturePositionState{
		PositionId: "alice_gap_long", Owner: "alice", Pair: pair,
		Side: int32(future.Long), EntryPrice: targetEntry.String(),
		Quantity: qty.String(), Margin: margin.String(),
		Status: int32(future.StatusOpen), OpenHeight: 1,
	})
	writeFuturePosition(t, sv, &pb.FuturePositionState{
		PositionId: "bob_one_profit_short", Owner: "bob", Pair: pair,
		Side: int32(future.Short), EntryPrice: makerEntry.String(),
		Quantity: qty.String(), Margin: new(big.Int).Mul(big.NewInt(10), scale).String(),
		Status: int32(future.StatusOpen), OpenHeight: 1,
	})

	orderID := "bob_one_profit_close"
	order := &pb.FutureOrderState{
		OrderId: orderID, Owner: "bob", OwnerAddress: "bob", Pair: pair,
		Side: int32(future.Long), Price: closePrice.String(),
		Amount: qty.String(), FilledBase: "0", FilledQuote: "0",
		Leverage: 10, Status: pb.FutureOrderStatus_FUTURE_ORDER_OPEN,
		CreateHeight: 1,
	}
	writeFutureOrderState(t, sv, order)
	reserveFutureOrderMarginForTest(t, sv, "bob", "USDT", orderID, future.CalcRequiredMargin(qty, closePrice, order.Leverage))
	priceKey, err := db.PriceToKey128(closePrice.String())
	if err != nil {
		t.Fatal(err)
	}
	orderData, err := proto.Marshal(order)
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyFutureOrderPriceIndex(pair, pb.OrderSide_BUY, false, priceKey, orderID), orderData)

	eng := newFutureEngine(sv, height)
	cfg, err := eng.GetMarketConfig(pair)
	if err != nil {
		t.Fatal(err)
	}
	pos, err := (&svPositionStore{sv: sv}).GetPosition("alice_gap_long")
	if err != nil || pos == nil {
		t.Fatalf("load target position: pos=%v err=%v", pos, err)
	}
	result, err := tryOrderbookLiquidatePositionChecked(sv, pair, height, eng, cfg, closePrice, pos)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || !result.NeedsADL || result.ADLShortfall == nil || result.ADLShortfall.Cmp(scale) != 0 {
		t.Fatalf("seized-margin-funded maker claim erased target gap: result=%+v, want one-unit ADL", result)
	}
	bob := readFuturePositionHistory(t, sv, "bob_one_profit_short")
	if bob.RealizedPnl != scale.String() {
		t.Fatalf("maker realized PnL=%s, want %s paid from seized margin", bob.RealizedPnl, scale)
	}
}

func TestTryOrderbookLiquidate_BankruptMakerInflowDoesNotEraseTargetADL(t *testing.T) {
	sv := newMemStateView()
	pair := "BTC_USDT"
	const height uint64 = 100

	scale := new(big.Int).Set(future.PriceScale)
	oneQty := new(big.Int).Set(scale)
	twoQty := new(big.Int).Mul(big.NewInt(2), scale)
	closePrice := new(big.Int).Mul(big.NewInt(98), scale)
	targetEntry := new(big.Int).Mul(big.NewInt(100), scale)
	sv.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
	sv.Set(keys.KeySpotLastPrice(pair), targetEntry.Bytes())
	sv.Set(keys.KeyFutureMarkPrice(pair), closePrice.Bytes())
	writeFuturePosition(t, sv, &pb.FuturePositionState{
		PositionId: "alice_target_gap", Owner: "alice", Pair: pair,
		Side: int32(future.Long), EntryPrice: targetEntry.String(),
		Quantity: twoQty.String(), Margin: scale.String(),
		Status: int32(future.StatusOpen), OpenHeight: 1,
	})
	                                                                          
	                                                                        
	writeFuturePosition(t, sv, &pb.FuturePositionState{
		PositionId: "bob_bankrupt_short", Owner: "bob", Pair: pair,
		Side: int32(future.Short), EntryPrice: new(big.Int).Mul(big.NewInt(96), scale).String(),
		Quantity: oneQty.String(), Margin: scale.String(),
		Status: int32(future.StatusOpen), OpenHeight: 1,
	})
	writeFuturePosition(t, sv, &pb.FuturePositionState{
		PositionId: "carol_profit_short", Owner: "carol", Pair: pair,
		Side: int32(future.Short), EntryPrice: targetEntry.String(),
		Quantity: oneQty.String(), Margin: new(big.Int).Mul(big.NewInt(10), scale).String(),
		Status: int32(future.StatusOpen), OpenHeight: 1,
	})

	addMaker := func(owner, orderID string) {
		t.Helper()
		order := &pb.FutureOrderState{
			OrderId: orderID, Owner: owner, OwnerAddress: owner, Pair: pair,
			Side: int32(future.Long), Price: closePrice.String(),
			Amount: oneQty.String(), FilledBase: "0", FilledQuote: "0",
			Leverage: 10, Status: pb.FutureOrderStatus_FUTURE_ORDER_OPEN,
			CreateHeight: 1,
		}
		writeFutureOrderState(t, sv, order)
		reserveFutureOrderMarginForTest(t, sv, owner, "USDT", orderID, future.CalcRequiredMargin(oneQty, closePrice, order.Leverage))
		priceKey, err := db.PriceToKey128(closePrice.String())
		if err != nil {
			t.Fatal(err)
		}
		data, err := proto.Marshal(order)
		if err != nil {
			t.Fatal(err)
		}
		sv.Set(keys.KeyFutureOrderPriceIndex(pair, pb.OrderSide_BUY, false, priceKey, orderID), data)
	}
	addMaker("bob", "a_bob_bankrupt")
	addMaker("carol", "b_carol_profit")

	eng := newFutureEngine(sv, height)
	cfg, err := eng.GetMarketConfig(pair)
	if err != nil {
		t.Fatal(err)
	}
	pos, err := (&svPositionStore{sv: sv}).GetPosition("alice_target_gap")
	if err != nil || pos == nil {
		t.Fatalf("load target position: pos=%v err=%v", pos, err)
	}
	result, err := tryOrderbookLiquidatePositionChecked(sv, pair, height, eng, cfg, closePrice, pos)
	if err != nil {
		t.Fatal(err)
	}
	wantGap := new(big.Int).Mul(big.NewInt(3), scale)
	if result == nil || !result.NeedsADL || result.ADLShortfall == nil || result.ADLShortfall.Cmp(wantGap) != 0 {
		t.Fatalf("bankrupt maker inflow erased target gap: result=%+v, want ADL=%s", result, wantGap)
	}
	carol := readFuturePositionHistory(t, sv, "carol_profit_short")
	wantCarolPnL := new(big.Int).Mul(big.NewInt(2), scale)
	if carol.RealizedPnl != wantCarolPnL.String() {
		t.Fatalf("Carol realized PnL=%s, want %s funded by newly seized margins", carol.RealizedPnl, wantCarolPnL)
	}
}

func TestCheckFutureLiquidations_MakerShortpayIsNotADLAgain(t *testing.T) {
	sv := newMemStateView()
	pair := "BTC_USDT"
	const height uint64 = 100

	scale := new(big.Int).Set(future.PriceScale)
	qty := new(big.Int).Set(scale)
	entryPrice := new(big.Int).Mul(big.NewInt(100), scale)
	closePrice := new(big.Int).Mul(big.NewInt(98), scale)
	margin := new(big.Int).Set(scale)

	sv.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
	sv.Set(keys.KeySpotLastPrice(pair), entryPrice.Bytes())
	sv.Set(keys.KeyFutureMarkPrice(pair), closePrice.Bytes())
	writeFuturePosition(t, sv, &pb.FuturePositionState{
		PositionId: "alice_double_gap", Owner: "alice", Pair: pair,
		Side: int32(future.Long), EntryPrice: entryPrice.String(),
		Quantity: qty.String(), Margin: margin.String(),
		Status: int32(future.StatusOpen), OpenHeight: 1,
	})
	writeFuturePosition(t, sv, &pb.FuturePositionState{
		PositionId: "bob_shortpaid_maker", Owner: "bob", Pair: pair,
		Side: int32(future.Short), EntryPrice: entryPrice.String(),
		Quantity: qty.String(), Margin: new(big.Int).Mul(big.NewInt(10), scale).String(),
		Status: int32(future.StatusOpen), OpenHeight: 1,
	})
	writeFuturePosition(t, sv, &pb.FuturePositionState{
		PositionId: "carol_adl_candidate", Owner: "carol", Pair: pair,
		Side: int32(future.Short), EntryPrice: entryPrice.String(),
		Quantity: qty.String(), Margin: new(big.Int).Mul(big.NewInt(10), scale).String(),
		Status: int32(future.StatusOpen), OpenHeight: 1,
	})

	orderID := "bob_shortpaid_close"
	order := &pb.FutureOrderState{
		OrderId: orderID, Owner: "bob", OwnerAddress: "bob", Pair: pair,
		Side: int32(future.Long), Price: closePrice.String(),
		Amount: qty.String(), FilledBase: "0", FilledQuote: "0",
		Leverage: 10, Status: pb.FutureOrderStatus_FUTURE_ORDER_OPEN,
		CreateHeight: 1,
	}
	writeFutureOrderState(t, sv, order)
	reserveFutureOrderMarginForTest(t, sv, "bob", "USDT", orderID, future.CalcRequiredMargin(qty, closePrice, order.Leverage))
	priceKey, err := db.PriceToKey128(closePrice.String())
	if err != nil {
		t.Fatal(err)
	}
	orderData, err := proto.Marshal(order)
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyFutureOrderPriceIndex(pair, pb.OrderSide_BUY, false, priceKey, orderID), orderData)

	checkFutureLiquidations(sv, pair, height)

	shortfallData, exists, err := sv.Get(keys.KeyFutureInsuranceShortfall(pair))
	if err != nil {
		t.Fatal(err)
	}
	if !exists || new(big.Int).SetBytes(shortfallData).Cmp(scale) != 0 {
		t.Fatalf("one-unit gap should be recorded exactly once, exists=%v shortfall=%s", exists, new(big.Int).SetBytes(shortfallData))
	}
	carol := readFuturePosition(t, sv, "carol_adl_candidate")
	if carol.Quantity != qty.String() || future.PositionStatus(carol.Status) != future.StatusOpen {
		t.Fatalf("Carol was ADL-reduced for a gap already borne by maker: %+v", carol)
	}
	bob := readFuturePositionHistory(t, sv, "bob_shortpaid_maker")
	if bob.RealizedPnl != scale.String() {
		t.Fatalf("Bob actual paid PnL=%s, want %s with one-unit shortpay", bob.RealizedPnl, scale)
	}
}

func TestTryOrderbookLiquidate_MultiMakerLossFundsEarlierProfit(t *testing.T) {
	sv := newMemStateView()
	pair := "BTC_USDT"
	const height uint64 = 100

	scale := new(big.Int).Set(future.PriceScale)
	oneQty := new(big.Int).Set(scale)
	twoQty := new(big.Int).Mul(big.NewInt(2), scale)
	closePrice := new(big.Int).Mul(big.NewInt(98), scale)
	sv.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
	sv.Set(keys.KeySpotLastPrice(pair), new(big.Int).Mul(big.NewInt(100), scale).Bytes())
	sv.Set(keys.KeyFutureMarkPrice(pair), closePrice.Bytes())
	writeFuturePosition(t, sv, &pb.FuturePositionState{
		PositionId: "alice_multi_maker", Owner: "alice", Pair: pair,
		Side: int32(future.Long), EntryPrice: new(big.Int).Mul(big.NewInt(100), scale).String(),
		Quantity: twoQty.String(), Margin: new(big.Int).Mul(big.NewInt(4), scale).String(),
		Status: int32(future.StatusOpen), OpenHeight: 1,
	})
	writeFuturePosition(t, sv, &pb.FuturePositionState{
		PositionId: "bob_profit_first", Owner: "bob", Pair: pair,
		Side: int32(future.Short), EntryPrice: new(big.Int).Mul(big.NewInt(198), scale).String(),
		Quantity: oneQty.String(), Margin: new(big.Int).Mul(big.NewInt(2), scale).String(),
		Status: int32(future.StatusOpen), OpenHeight: 1,
	})
	writeFuturePosition(t, sv, &pb.FuturePositionState{
		PositionId: "carol_loss_second", Owner: "carol", Pair: pair,
		Side: int32(future.Short), EntryPrice: new(big.Int).Mul(big.NewInt(2), scale).String(),
		Quantity: oneQty.String(), Margin: new(big.Int).Mul(big.NewInt(97), scale).String(),
		Status: int32(future.StatusOpen), OpenHeight: 1,
	})

	addMaker := func(owner, orderID string) {
		t.Helper()
		order := &pb.FutureOrderState{
			OrderId: orderID, Owner: owner, OwnerAddress: owner, Pair: pair,
			Side: int32(future.Long), Price: closePrice.String(),
			Amount: oneQty.String(), FilledBase: "0", FilledQuote: "0",
			Leverage: 10, Status: pb.FutureOrderStatus_FUTURE_ORDER_OPEN,
			CreateHeight: 1,
		}
		writeFutureOrderState(t, sv, order)
		reserveFutureOrderMarginForTest(t, sv, owner, "USDT", orderID, future.CalcRequiredMargin(oneQty, closePrice, order.Leverage))
		priceKey, err := db.PriceToKey128(closePrice.String())
		if err != nil {
			t.Fatal(err)
		}
		data, err := proto.Marshal(order)
		if err != nil {
			t.Fatal(err)
		}
		sv.Set(keys.KeyFutureOrderPriceIndex(pair, pb.OrderSide_BUY, false, priceKey, orderID), data)
	}
	                                                                          
	addMaker("bob", "a_bob_profit_first")
	addMaker("carol", "b_carol_loss_second")

	eng := newFutureEngine(sv, height)
	cfg, err := eng.GetMarketConfig(pair)
	if err != nil {
		t.Fatal(err)
	}
	pos, err := (&svPositionStore{sv: sv}).GetPosition("alice_multi_maker")
	if err != nil || pos == nil {
		t.Fatalf("load liquidated position: pos=%v err=%v", pos, err)
	}
	result := tryOrderbookLiquidatePosition(sv, pair, height, eng, cfg, closePrice, pos)
	if result == nil {
		t.Fatal("expected two-maker orderbook liquidation")
	}
	bob := readFuturePositionHistory(t, sv, "bob_profit_first")
	carol := readFuturePositionHistory(t, sv, "carol_loss_second")
	if bob.RealizedPnl != new(big.Int).Mul(big.NewInt(100), scale).String() || carol.RealizedPnl != new(big.Int).Mul(big.NewInt(-96), scale).String() {
		t.Fatalf("maker realized PnL bob/carol=%s/%s, want +100/-96", bob.RealizedPnl, carol.RealizedPnl)
	}
	if data, exists, err := sv.Get(keys.KeyFutureInsuranceFund(pair)); err != nil {
		t.Fatal(err)
	} else if exists && new(big.Int).SetBytes(data).Sign() != 0 {
		t.Fatalf("maker loss stranded after earlier profit claim: %s", new(big.Int).SetBytes(data))
	}
	if data, exists, err := sv.Get(keys.KeyFutureInsuranceShortfall(pair)); err != nil {
		t.Fatal(err)
	} else if exists && new(big.Int).SetBytes(data).Sign() != 0 {
		t.Fatalf("fill order created false maker shortfall: %s", new(big.Int).SetBytes(data))
	}
}

func writeFuturePosition(t *testing.T, sv StateView, ps *pb.FuturePositionState) {
	t.Helper()
	data, err := proto.Marshal(ps)
	if err != nil {
		t.Fatalf("marshal position: %v", err)
	}
	sv.Set(keys.KeyFuturePosition(ps.PositionId), data)
	sv.Set(keys.KeyFutureOwnerPosition(ps.Owner, ps.PositionId), data)
	sv.Set(keys.KeyFuturePairPosition(ps.Pair, ps.PositionId), data)
	if future.PositionStatus(ps.Status) == future.StatusOpen {
		sv.Set(keys.KeyFutureOwnerPairSidePosition(ps.Owner, ps.Pair, ps.Side), []byte(ps.PositionId))
	} else {
		sv.Del(keys.KeyFutureOwnerPairSidePosition(ps.Owner, ps.Pair, ps.Side))
	}

	                                                                                   
	                                                              
	idx := &svLiquidationIndex{sv: sv}
	entry := big.NewInt(0)
	entry.SetString(ps.EntryPrice, 10)
	margin := new(big.Int)
	margin.SetString(ps.Margin, 10)
	qty := new(big.Int)
	qty.SetString(ps.Quantity, 10)
	                                                                                      
	liqPrice := future.CalcLiquidationPrice(future.Side(ps.Side), entry, margin, qty, future.DefaultMaintenanceMarginRateBps)
	if liqPrice.Sign() > 0 {
		_ = idx.Upsert(ps.PositionId, ps.Pair, future.Side(ps.Side), liqPrice)
	}
}

func writeFutureOrderState(t *testing.T, sv StateView, os *pb.FutureOrderState) {
	t.Helper()
	data, err := proto.Marshal(os)
	if err != nil {
		t.Fatalf("marshal order state: %v", err)
	}
	sv.Set(keys.KeyFutureOrderState(os.OrderId), data)
}

func readFuturePosition(t *testing.T, sv StateView, id string) *pb.FuturePositionState {
	t.Helper()
	data, _, _ := sv.Get(keys.KeyFuturePosition(id))
	var ps pb.FuturePositionState
	if err := proto.Unmarshal(data, &ps); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return &ps
}

func readFuturePositionHistory(t *testing.T, sv StateView, id string) *pb.FuturePositionState {
	t.Helper()
	data, _, _ := sv.Get(keys.KeyFuturePositionHistory(id))
	var ps pb.FuturePositionState
	if err := proto.Unmarshal(data, &ps); err != nil {
		t.Fatalf("unmarshal history: %v", err)
	}
	return &ps
}

func scanOpenPositions(t *testing.T, sv StateView, pair string) []*pb.FuturePositionState {
	t.Helper()
	kvs, err := sv.Scan(keys.KeyFuturePairPositionPrefix(pair))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	out := make([]*pb.FuturePositionState, 0, len(kvs))
	for _, v := range kvs {
		var p pb.FuturePositionState
		if err := proto.Unmarshal(v, &p); err == nil {
			out = append(out, &p)
		}
	}
	return out
}

func giveBalance(t *testing.T, sv StateView, addr, token string, amount *big.Int) {
	t.Helper()
	rec := &pb.TokenBalanceRecord{
		Address: addr, Token: token,
		Balance: &pb.TokenBalance{Balance: amount.String()},
	}
	data, _ := proto.Marshal(rec)
	sv.Set(keys.KeyBalance(addr, token), data)
}

func reserveFutureOrderMarginForTest(t *testing.T, sv StateView, owner, token, orderID string, amount *big.Int) {
	t.Helper()
	if amount == nil || amount.Sign() <= 0 {
		t.Fatalf("invalid reserved margin: %v", amount)
	}
	bal := GetBalance(sv, owner, token)
	frozen := parseBigOrZero(bal.OrderFrozenBalance)
	frozen.Add(frozen, amount)
	bal.OrderFrozenBalance = frozen.String()
	SetBalance(sv, owner, token, bal)
	sv.Set(keys.KeyFutureOrderReservedMargin(orderID), amount.Bytes())
}

func futureQuoteConservationTotal(t *testing.T, sv StateView, pair, quoteToken string) *big.Int {
	t.Helper()
	total := new(big.Int)
	balances, err := sv.Scan("v1_balance_")
	if err != nil {
		t.Fatalf("scan balances: %v", err)
	}
	for k, v := range balances {
		if extractTokenFromBalanceKey(k) == quoteToken {
			total.Add(total, balanceRecordTotal(v))
		}
	}
	positions, err := sv.Scan(keys.KeyFuturePairPositionPrefix(pair))
	if err != nil {
		t.Fatalf("scan positions: %v", err)
	}
	for _, v := range positions {
		var ps pb.FuturePositionState
		if err := proto.Unmarshal(v, &ps); err == nil && future.PositionStatus(ps.Status) == future.StatusOpen {
			total.Add(total, parseBigOrZero(ps.Margin))
		}
	}
	if data, exists, _ := sv.Get(keys.KeyFutureInsuranceFund(pair)); exists && len(data) > 0 {
		total.Add(total, new(big.Int).SetBytes(data))
	}
	return total
}
