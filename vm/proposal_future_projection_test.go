package vm

import (
	"dex/db"
	"dex/future"
	"dex/keys"
	"dex/pb"
	"fmt"
	"math/big"
	"testing"
)

func putProposalFutureMaker(t *testing.T, sv StateView, pair, orderID, owner string, side future.Side, price, amount, filled *big.Int, leverage uint64) {
	t.Helper()
	state := &pb.FutureOrderState{
		OrderId: orderID, Owner: owner, OwnerAddress: owner, Pair: pair,
		Side: int32(side), Price: price.String(), Amount: amount.String(),
		FilledBase: filled.String(), FilledQuote: "0", Leverage: leverage,
		Status: pb.FutureOrderStatus_FUTURE_ORDER_OPEN, CreateHeight: 1,
	}
	if err := writeFutureOrderSnapshot(sv, state); err != nil {
		t.Fatalf("write future maker %s: %v", orderID, err)
	}
	remaining := new(big.Int).Sub(amount, filled)
	reserved := future.CalcRequiredMargin(remaining, price, leverage)
	if reserved.Sign() > 0 {
		reserveFutureOrderMarginForTest(t, sv, owner, "USDT", orderID, reserved)
	}
}

func TestProposalFutureCaptureRecordsPublicExecutionTruth(t *testing.T) {
	const (
		pair    = "BTC_USDT"
		orderID = "public-sell"
		owner   = "alice"
	)
	sv := newMemStateView()
	sv.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
	putProposalFutureMaker(t, sv, pair, orderID, owner, future.Short,
		big.NewInt(100_000_000), big.NewInt(10), big.NewInt(4), 10)
	putProposalFutureMaker(t, sv, pair, "public-buy", "bob", future.Long,
		big.NewInt(110_000_000), big.NewInt(7), big.NewInt(2), 20)

	tx := &pb.AnyTx{Content: &pb.AnyTx_FutureOrderTx{FutureOrderTx: &pb.FutureOrderTx{
		Base:     &pb.BaseMessage{TxId: orderID, FromAddress: owner},
		Pair:     pair,
		Side:     int32(future.Short),
		Quantity: "10",
		Price:    "100000000",
		Leverage: 10,
	}}}
	receipt := &Receipt{TxID: orderID, Status: "SUCCEED", Events: []Event{
		{Kind: EventOrderMatched, Payload: map[string]string{
			"maker_order_id": "older-buy-1", "quantity": "1", "price": "90000000",
		}},
		{Kind: EventOrderMatched, Payload: map[string]string{
			"maker_order_id": "older-buy-2", "quantity": "3", "price": "95000000",
		}},
	}}

	capture := &proposalFutureCapture{maxOrders: 8, takerAddress: "arbitrage-account"}
	if err := capture.capturePostTransaction(sv, nil, 2, tx, receipt); err != nil {
		t.Fatalf("capture future execution: %v", err)
	}
	if capture.result == nil || len(capture.result.Executions) != 1 {
		t.Fatalf("execution trace=%+v", capture.result)
	}
	execution := capture.result.Executions[0]
	if execution.PublicPosition != 3 || execution.OrderID != orderID || execution.Owner != owner ||
		execution.Pair != pair || execution.QuoteToken != "USDT" || execution.Side != pb.OrderSide_SELL ||
		execution.Price != "100000000" || execution.Quantity != "10" || execution.Remaining != "6" || execution.Leverage != 10 {
		t.Fatalf("execution=%+v", execution)
	}
	if len(execution.Matches) != 2 ||
		execution.Matches[0].MakerOrderID != "older-buy-1" || execution.Matches[0].Quantity != "1" || execution.Matches[0].Price != "90000000" ||
		execution.Matches[1].MakerOrderID != "older-buy-2" || execution.Matches[1].Quantity != "3" || execution.Matches[1].Price != "95000000" {
		t.Fatalf("matches=%+v", execution.Matches)
	}
	if len(execution.Levels) != 2 {
		t.Fatalf("post-execution levels=%+v, want both taker directions", execution.Levels)
	}
	if execution.Levels[0].TakerSide != pb.OrderSide_BUY || execution.Levels[0].Price != "100000000" ||
		len(execution.Levels[0].Makers) != 1 || execution.Levels[0].Makers[0].OrderID != orderID || execution.Levels[0].Makers[0].Remaining != "6" {
		t.Fatalf("post BUY level=%+v", execution.Levels[0])
	}
	if execution.Levels[1].TakerSide != pb.OrderSide_SELL || execution.Levels[1].Price != "110000000" ||
		len(execution.Levels[1].Makers) != 1 || execution.Levels[1].Makers[0].OrderID != "public-buy" || execution.Levels[1].Makers[0].Remaining != "5" {
		t.Fatalf("post SELL level=%+v", execution.Levels[1])
	}
}

func TestProposalFutureCaptureSnapshotsTakerBalanceAtEachExecutionBoundary(t *testing.T) {
	const (
		pair  = "BTC_USDT"
		taker = "arbitrage-account"
	)
	sv := newMemStateView()
	capture := &proposalFutureCapture{maxOrders: 8, takerAddress: taker}
	captureOne := func(index int, id string, quote, fb string) {
		t.Helper()
		SetBalance(sv, taker, "USDT", &pb.TokenBalance{Balance: quote})
		SetBalance(sv, taker, FeeToken, &pb.TokenBalance{Balance: fb})
		tx := &pb.AnyTx{Content: &pb.AnyTx_FutureOrderTx{FutureOrderTx: &pb.FutureOrderTx{
			Base: &pb.BaseMessage{TxId: id, FromAddress: "public-" + id},
			Pair: pair, Side: int32(future.Long), Quantity: "1", Price: "100000000", Leverage: 10,
		}}}
		if err := capture.capturePostTransaction(sv, nil, index, tx, &Receipt{TxID: id, Status: "SUCCEED"}); err != nil {
			t.Fatalf("capture boundary %d: %v", index+1, err)
		}
	}

	captureOne(0, "first-public-future", "100", "20")
	captureOne(1, "second-public-future", "900", "7")
	if capture.result == nil || len(capture.result.Executions) != 2 {
		t.Fatalf("execution boundaries=%+v", capture.result)
	}
	first, second := capture.result.Executions[0], capture.result.Executions[1]
	if first.TakerAvailable["USDT"] != "100" || first.TakerAvailable[FeeToken] != "20" {
		t.Fatalf("first boundary availability=%v", first.TakerAvailable)
	}
	if second.TakerAvailable["USDT"] != "900" || second.TakerAvailable[FeeToken] != "7" {
		t.Fatalf("second boundary availability=%v", second.TakerAvailable)
	}
}

func TestProposalFutureCaptureUsesPostPrefixBookAndAvailableBalance(t *testing.T) {
	const (
		pair  = "ETH_USDT"
		taker = "arbitrage-account"
	)
	sv := newMemStateView()
	sv.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
	quantity := big.NewInt(10)

	                                                                         
	                                                                     
	putProposalFutureMaker(t, sv, pair, "a-external-sell", "local-miner", future.Short, big.NewInt(100_000_000), quantity, big.NewInt(1), 10)
	putProposalFutureMaker(t, sv, pair, "m-self-sell", taker, future.Short, big.NewInt(100_000_000), quantity, big.NewInt(0), 10)
	putProposalFutureMaker(t, sv, pair, "z-hidden-sell", "zoe", future.Short, big.NewInt(100_000_000), quantity, big.NewInt(0), 10)
	putProposalFutureMaker(t, sv, pair, "worse-sell", "will", future.Short, big.NewInt(101_000_000), quantity, big.NewInt(0), 10)

	                                                                           
	                          
	putProposalFutureMaker(t, sv, pair, "z-external-buy", "local-miner", future.Long, big.NewInt(110_000_000), quantity, big.NewInt(2), 10)
	putProposalFutureMaker(t, sv, pair, "m-self-buy", taker, future.Long, big.NewInt(110_000_000), quantity, big.NewInt(0), 10)
	putProposalFutureMaker(t, sv, pair, "a-hidden-buy", "amy", future.Long, big.NewInt(110_000_000), quantity, big.NewInt(0), 10)
	putProposalFutureMaker(t, sv, pair, "worse-buy", "wendy", future.Long, big.NewInt(109_000_000), quantity, big.NewInt(0), 10)
	SetBalance(sv, taker, "USDT", &pb.TokenBalance{Balance: "123", OrderFrozenBalance: "999"})
	SetBalance(sv, taker, FeeToken, &pb.TokenBalance{Balance: "456"})

	tx := &pb.AnyTx{Content: &pb.AnyTx_FutureOrderTx{FutureOrderTx: &pb.FutureOrderTx{
		Base: &pb.BaseMessage{TxId: "public-add", FromAddress: "public-owner"},
		Pair: pair, Side: int32(future.Long), Quantity: "1", Price: "100000000", Leverage: 10,
	}}}
	block := &pb.Block{Body: []*pb.AnyTx{tx}}
	capture := &proposalFutureCapture{takerAddress: taker, maxOrders: 8}
	if err := capture.capturePostTransactions(sv, block, []*Receipt{{TxID: "public-add", Status: "SUCCEED"}}); err != nil {
		t.Fatalf("capture post-prefix future projection: %v", err)
	}
	if capture.result == nil || len(capture.result.Levels) != 2 {
		t.Fatalf("levels=%+v, want BUY and SELL", capture.result)
	}
	buy := capture.result.Levels[0]
	if buy.Pair != pair || buy.QuoteToken != "USDT" || buy.TakerSide != pb.OrderSide_BUY || buy.Price != "100000000" ||
		len(buy.Makers) != 1 || buy.Makers[0].OrderID != "a-external-sell" || buy.Makers[0].Remaining != "9" {
		t.Fatalf("post-prefix BUY level=%+v", buy)
	}
	sell := capture.result.Levels[1]
	if sell.Pair != pair || sell.QuoteToken != "USDT" || sell.TakerSide != pb.OrderSide_SELL || sell.Price != "110000000" ||
		len(sell.Makers) != 1 || sell.Makers[0].OrderID != "z-external-buy" || sell.Makers[0].Remaining != "8" {
		t.Fatalf("post-prefix SELL level=%+v", sell)
	}
	if got := capture.result.TakerAvailable["USDT"]; got != "123" {
		t.Fatalf("available balance=%q, want 123 (frozen balance must be excluded)", got)
	}
	if got := capture.result.TakerAvailable[FeeToken]; got != "456" {
		t.Fatalf("runtime-fee balance=%q, want 456", got)
	}
}

func TestProjectFutureBestLevelPagesPastMalformedAndZeroNotionalMakers(t *testing.T) {
	const pair = "PAGE_USDT"
	sv := newMemStateView()
	sv.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
	for i := 0; i < proposalSpotProjectionScanBatch/2; i++ {
		putProposalFutureMaker(t, sv, pair, fmt.Sprintf("dust-%03d", i), "dust-owner", future.Short,
			big.NewInt(1), big.NewInt(1), big.NewInt(0), 10)
	}
	priceKey, err := db.PriceToKey128("1")
	if err != nil {
		t.Fatalf("dust price key: %v", err)
	}
	for i := proposalSpotProjectionScanBatch / 2; i < proposalSpotProjectionScanBatch; i++ {
		orderID := fmt.Sprintf("malformed-%03d", i)
		sv.Set(keys.KeyFutureOrderPriceIndex(pair, pb.OrderSide_SELL, false, priceKey, orderID), []byte{0xff})
	}
	putProposalFutureMaker(t, sv, pair, "valid", "alice", future.Short,
		big.NewInt(100_000_000), big.NewInt(7), big.NewInt(0), 10)

	level, err := projectFutureBestLevel(sv, pair, "USDT", pb.OrderSide_BUY, "private-taker", 4)
	if err != nil {
		t.Fatalf("project paged future level: %v", err)
	}
	if level == nil || level.Price != "100000000" || len(level.Makers) != 1 || level.Makers[0].OrderID != "valid" || level.Makers[0].Remaining != "7" {
		t.Fatalf("paged future level=%+v", level)
	}
}
