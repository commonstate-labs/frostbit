package vm

import (
	"dex/future"
	"dex/keys"
	"dex/pb"
	"math/big"
	"testing"
)

func matchedEvent(pair, taker, maker, quantity, price string) Event {
	return Event{Kind: EventOrderMatched, Payload: map[string]string{
		"pair": pair, "taker": taker, "maker": maker,
		"quantity": quantity, "price": price,
	}}
}

func spotOrderAnyTx() *pb.AnyTx {
	return &pb.AnyTx{Content: &pb.AnyTx_OrderTx{OrderTx: &pb.OrderTx{BaseToken: "BTC", QuoteToken: "USDT"}}}
}

func futureOrderAnyTx() *pb.AnyTx {
	return &pb.AnyTx{Content: &pb.AnyTx_FutureOrderTx{FutureOrderTx: &pb.FutureOrderTx{Pair: "BTC_USDT"}}}
}

func TestBlockPriceFeedCollectorOnlyMatchedOrders(t *testing.T) {
	sv := newMemStateView()
	collector := newBlockPriceFeedCollector()

	if err := collector.recordReceipt(sv, futureOrderAnyTx(), &Receipt{Status: "SUCCEED"}); err != nil {
		t.Fatal(err)
	}
	spotChanged, futureChanged := collector.changedPairs()
	if len(spotChanged) != 0 || len(futureChanged) != 0 {
		t.Fatalf("unmatched order changed price feed: spot=%v future=%v", spotChanged, futureChanged)
	}

	receipt := &Receipt{Status: "SUCCEED", Events: []Event{
		matchedEvent("BTC_USDT", "bob", "alice", "100000000", "100000000"),
	}}
	if err := collector.recordReceipt(sv, futureOrderAnyTx(), receipt); err != nil {
		t.Fatal(err)
	}
	spotChanged, futureChanged = collector.changedPairs()
	if !futureChanged["BTC_USDT"] || spotChanged["BTC_USDT"] {
		t.Fatalf("future fill changed wrong side: spot=%v future=%v", spotChanged, futureChanged)
	}
}

func TestBlockPriceFeedCollectorDustFillCounts(t *testing.T) {
	sv := newMemStateView()
	collector := newBlockPriceFeedCollector()
	receipt := &Receipt{Status: "SUCCEED", Events: []Event{
		                                                                   
		                                                                
		matchedEvent("BTC_USDT", "bob", "alice", "1", "100000000"),
	}}
	if err := collector.recordReceipt(sv, spotOrderAnyTx(), receipt); err != nil {
		t.Fatal(err)
	}
	got := readPosBig(sv, keys.KeySpotLastPrice("BTC_USDT"))
	if got == nil || got.Cmp(big.NewInt(100000000)) != 0 {
		t.Fatalf("dust fill VWAP=%v, want 100000000", got)
	}
	spotChanged, _ := collector.changedPairs()
	if !spotChanged["BTC_USDT"] {
		t.Fatal("dust fill must mark spot side changed")
	}
}

func TestBlockPriceFeedCollectorExcludesSelfTrade(t *testing.T) {
	sv := newMemStateView()
	pair := "BTC_USDT"
	previous := big.NewInt(77000000)
	sv.Set(keys.KeySpotLastPrice(pair), previous.Bytes())
	collector := newBlockPriceFeedCollector()
	receipt := &Receipt{Status: "SUCCEED", Events: []Event{
		matchedEvent(pair, "alice", "alice", "100000000", "900000000"),
	}}
	if err := collector.recordReceipt(sv, spotOrderAnyTx(), receipt); err != nil {
		t.Fatal(err)
	}
	got := readPosBig(sv, keys.KeySpotLastPrice(pair))
	if got == nil || got.Cmp(previous) != 0 {
		t.Fatalf("self-trade changed last price: got=%v want=%v", got, previous)
	}
	spotChanged, futureChanged := collector.changedPairs()
	if len(spotChanged) != 0 || len(futureChanged) != 0 {
		t.Fatalf("self-trade changed EMA inputs: spot=%v future=%v", spotChanged, futureChanged)
	}
}

func TestBlockPriceFeedCollectorIgnoresProtocolGeneratedFills(t *testing.T) {
	sv := newMemStateView()
	pair := "BTC_USDT"
	previous := big.NewInt(77000000)
	sv.Set(keys.KeyFutureLastPrice(pair), previous.Bytes())
	collector := newBlockPriceFeedCollector()

	                                                                         
	                                                                         
	                                                                      
	protocolReceipt := &Receipt{Status: "SUCCEED", Events: []Event{
		matchedEvent(pair, "liquidated-owner", "old-maker", "100000000", "900000000"),
	}}
	if err := collector.recordReceipt(sv, &pb.AnyTx{}, protocolReceipt); err != nil {
		t.Fatal(err)
	}
	got := readPosBig(sv, keys.KeyFutureLastPrice(pair))
	if got == nil || got.Cmp(previous) != 0 {
		t.Fatalf("protocol-generated fill changed price source: got=%v want=%v", got, previous)
	}
	spotChanged, futureChanged := collector.changedPairs()
	if len(spotChanged) != 0 || len(futureChanged) != 0 {
		t.Fatalf("protocol-generated fill changed EMA inputs: spot=%v future=%v", spotChanged, futureChanged)
	}
}

func TestBlockPriceFeedCollectorVWAPOrderIndependent(t *testing.T) {
	pair := "BTC_USDT"
	forward := []Event{
		matchedEvent(pair, "taker-a", "maker-a", "100000000", "100000000"),
		matchedEvent(pair, "taker-b", "maker-b", "300000000", "200000000"),
	}
	reverse := []Event{forward[1], forward[0]}

	collect := func(events []Event) *big.Int {
		t.Helper()
		sv := newMemStateView()
		collector := newBlockPriceFeedCollector()
		for _, event := range events {
			if err := collector.recordReceipt(sv, futureOrderAnyTx(), &Receipt{Status: "SUCCEED", Events: []Event{event}}); err != nil {
				t.Fatal(err)
			}
		}
		return readPosBig(sv, keys.KeyFutureLastPrice(pair))
	}

	want := big.NewInt(175000000)
	gotForward := collect(forward)
	gotReverse := collect(reverse)
	if gotForward == nil || gotForward.Cmp(want) != 0 || gotReverse == nil || gotReverse.Cmp(want) != 0 {
		t.Fatalf("order-dependent VWAP: forward=%v reverse=%v want=%v", gotForward, gotReverse, want)
	}
}

func TestBlockPriceFeedCollectorUsesActualPerFillQuoteRaw(t *testing.T) {
	sv := newMemStateView()
	collector := newBlockPriceFeedCollector()
	pair := "BTC_USDT"
	receipt := &Receipt{Status: "SUCCEED", Events: []Event{
		                                                            
		                                                      
		matchedEvent(pair, "a", "b", "70000000", "2"),
		matchedEvent(pair, "c", "d", "30000000", "5"),
	}}
	if err := collector.recordReceipt(sv, spotOrderAnyTx(), receipt); err != nil {
		t.Fatal(err)
	}
	got := readPosBig(sv, keys.KeySpotLastPrice(pair))
	if got == nil || got.Cmp(big.NewInt(2)) != 0 {
		t.Fatalf("actual-quote VWAP=%v, want 2", got)
	}
}

func TestBlockPriceFeedCollectorAggregateDoesNotOverflowUint256(t *testing.T) {
	sv := newMemStateView()
	collector := newBlockPriceFeedCollector()
	max := MaxUint256.String()
	receipt := &Receipt{Status: "SUCCEED", Events: []Event{
		matchedEvent("BTC_USDT", "a", "b", max, "100000000"),
		matchedEvent("BTC_USDT", "c", "d", max, "100000000"),
	}}
	if err := collector.recordReceipt(sv, futureOrderAnyTx(), receipt); err != nil {
		t.Fatalf("aggregate turnover above uint256 should use unbounded sums: %v", err)
	}
	got := readPosBig(sv, keys.KeyFutureLastPrice("BTC_USDT"))
	if got == nil || got.Cmp(big.NewInt(100000000)) != 0 {
		t.Fatalf("large aggregate VWAP=%v, want 100000000", got)
	}
}

func TestBlockVWAPFeedsEMAOnce(t *testing.T) {
	sv := newMemStateView()
	collector := newBlockPriceFeedCollector()
	pair := "BTC_USDT"
	oldEMA := big.NewInt(100000000)
	sv.Set(keys.KeyFutureEMA(pair), oldEMA.Bytes())
	sv.Set(keys.KeyFutureSpotEMA(pair), oldEMA.Bytes())
	sv.Set(keys.KeyFutureMarkPrice(pair), oldEMA.Bytes())
	receipt := &Receipt{Status: "SUCCEED", Events: []Event{
		matchedEvent(pair, "a", "b", "100000000", "150000000"),
		matchedEvent(pair, "c", "d", "100000000", "250000000"),
	}}
	if err := collector.recordReceipt(sv, futureOrderAnyTx(), receipt); err != nil {
		t.Fatal(err)
	}
	_, futureChanged := collector.changedPairs()
	if err := updateMarkPriceEMA(sv, pair, false, futureChanged[pair]); err != nil {
		t.Fatal(err)
	}
	want := future.CalcEMA(oldEMA, big.NewInt(200000000), future.DefaultEMAWindow)
	got := readPosBig(sv, keys.KeyFutureEMA(pair))
	if got == nil || got.Cmp(want) != 0 {
		t.Fatalf("EMA=%v, want one update from block VWAP=%v", got, want)
	}
}
