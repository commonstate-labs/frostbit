package vm

import (
	"dex/keys"
	"dex/pb"
	"math/big"
	"strings"
	"testing"
)

                                                                              
                                                                                
                                                            
func TestSpotTradeRecordGenerationDoesNotWriteLastPrice(t *testing.T) {
	sv := newMemStateView()
	pair := "BTC_USDT"
	order := &pb.OrderTx{
		Base:       &pb.BaseMessage{TxId: "taker", FromAddress: "bob", ExecutedHeight: 1},
		BaseToken:  "BTC",
		QuoteToken: "USDT",
		Side:       pb.OrderSide_BUY,
		Price:      "100000000",
		Amount:     "1",
	}
	states := map[string]*pb.OrderState{
		"taker": {OrderId: "taker", Owner: "bob", Side: pb.OrderSide_BUY, BaseToken: "BTC", QuoteToken: "USDT"},
		"maker": {OrderId: "maker", Owner: "alice", Side: pb.OrderSide_SELL, BaseToken: "BTC", QuoteToken: "USDT"},
	}
	trades := []SpotTradeUpdate{
		{OrderID: "taker", TradeAmt: big.NewInt(1), TradePrice: big.NewInt(100000000)},
		{OrderID: "maker", TradeAmt: big.NewInt(1), TradePrice: big.NewInt(100000000)},
	}
	if err := NewSpotEngine(sv).generateTradeRecordsFromStates(order, trades, states, sv, pair); err != nil {
		t.Fatalf("trade records: %v", err)
	}
	if _, exists, _ := sv.Get(keys.KeySpotLastPrice(pair)); exists {
		t.Fatal("trade record path must not bypass the block VWAP collector")
	}
}

func TestSpotSettlementRequiresEveryTradeState(t *testing.T) {
	sv := newMemStateView()
	order := &pb.OrderTx{
		Base:       &pb.BaseMessage{TxId: "taker", FromAddress: "bob", ExecutedHeight: 1},
		BaseToken:  "BTC",
		QuoteToken: "USDT",
		Side:       pb.OrderSide_BUY,
		Price:      "100000000",
		Amount:     "100000000",
	}
	states := map[string]*pb.OrderState{
		"taker": {
			OrderId: "taker", Owner: "bob", Side: pb.OrderSide_BUY,
			BaseToken: "BTC", QuoteToken: "USDT", Price: "100000000",
		},
	}
	trades := []SpotTradeUpdate{
		{OrderID: "taker", TradeAmt: big.NewInt(100000000), TradePrice: big.NewInt(100000000)},
		{OrderID: "maker_missing", TradeAmt: big.NewInt(100000000), TradePrice: big.NewInt(100000000)},
	}
	err := NewSpotEngine(sv).updateAccountBalancesFromStates(order, trades, states, sv)
	if err == nil || !strings.Contains(err.Error(), "missing order state") {
		t.Fatalf("expected missing state error, got %v", err)
	}
	if len(sv.Diff()) != 0 {
		t.Fatalf("missing maker state must fail before balance writes, got diff=%d", len(sv.Diff()))
	}
}

func TestSpotLimitBuyRefundUsesFrozenMinusTradeQuote(t *testing.T) {
	sv := newMemStateView()
	qty := big.NewInt(70_000_000)
	limitPrice := big.NewInt(3)
	tradePrice := big.NewInt(2)
	frozenQuote, err := QuoteFromBasePrice(qty, limitPrice)
	if err != nil {
		t.Fatalf("frozen quote: %v", err)
	}
	tradeQuote, err := QuoteFromBasePrice(qty, tradePrice)
	if err != nil {
		t.Fatalf("trade quote: %v", err)
	}

	SetBalance(sv, "bob", "USDT", &pb.TokenBalance{Balance: "0", OrderFrozenBalance: frozenQuote.String()})
	SetBalance(sv, "alice", "BTC", &pb.TokenBalance{Balance: "0", OrderFrozenBalance: qty.String()})
	order := &pb.OrderTx{
		Base:       &pb.BaseMessage{TxId: "taker", FromAddress: "bob", ExecutedHeight: 1},
		BaseToken:  "BTC",
		QuoteToken: "USDT",
		Side:       pb.OrderSide_BUY,
		Price:      limitPrice.String(),
		Amount:     qty.String(),
	}
	states := map[string]*pb.OrderState{
		"taker": {
			OrderId: "taker", Owner: "bob", Side: pb.OrderSide_BUY,
			BaseToken: "BTC", QuoteToken: "USDT", Price: limitPrice.String(),
		},
		"maker": {
			OrderId: "maker", Owner: "alice", Side: pb.OrderSide_SELL,
			BaseToken: "BTC", QuoteToken: "USDT", Price: tradePrice.String(),
		},
	}
	trades := []SpotTradeUpdate{
		{OrderID: "taker", TradeAmt: qty, TradePrice: tradePrice},
		{OrderID: "maker", TradeAmt: qty, TradePrice: tradePrice},
	}
	if err := NewSpotEngine(sv).updateAccountBalancesFromStates(order, trades, states, sv); err != nil {
		t.Fatalf("settle trade: %v", err)
	}

	bobUSDT := GetBalance(sv, "bob", "USDT")
	if want := new(big.Int).Sub(frozenQuote, tradeQuote).String(); bobUSDT.Balance != want || bobUSDT.OrderFrozenBalance != "0" {
		t.Fatalf("buyer quote mismatch: got balance=%s frozen=%s want balance=%s frozen=0", bobUSDT.Balance, bobUSDT.OrderFrozenBalance, want)
	}
	aliceUSDT := GetBalance(sv, "alice", "USDT")
	if aliceUSDT.Balance != tradeQuote.String() {
		t.Fatalf("seller quote mismatch: got %s want %s", aliceUSDT.Balance, tradeQuote)
	}
	bobBTC := GetBalance(sv, "bob", "BTC")
	if bobBTC.Balance != qty.String() {
		t.Fatalf("buyer base mismatch: got %s want %s", bobBTC.Balance, qty)
	}
	aliceBTC := GetBalance(sv, "alice", "BTC")
	if aliceBTC.OrderFrozenBalance != "0" {
		t.Fatalf("seller base frozen mismatch: got %s want 0", aliceBTC.OrderFrozenBalance)
	}
}

func TestSpotSettlementRejectsSameSideTradeStates(t *testing.T) {
	sv := newMemStateView()
	order := &pb.OrderTx{
		Base:       &pb.BaseMessage{TxId: "taker", FromAddress: "bob", ExecutedHeight: 1},
		BaseToken:  "ETH.USDT",
		QuoteToken: "FB",
		Side:       pb.OrderSide_BUY,
		Price:      "1120000000",
		Amount:     "25000000",
	}
	states := map[string]*pb.OrderState{
		"taker": {OrderId: "taker", Owner: "bob", Side: pb.OrderSide_BUY, BaseToken: "ETH.USDT", QuoteToken: "FB", Price: "1120000000"},
		"maker": {OrderId: "maker", Owner: "alice", Side: pb.OrderSide_BUY, BaseToken: "ETH.USDT", QuoteToken: "FB", Price: "1120000000"},
	}
	trades := []SpotTradeUpdate{
		{OrderID: "taker", TradeAmt: big.NewInt(25000000), TradePrice: big.NewInt(1120000000)},
		{OrderID: "maker", TradeAmt: big.NewInt(25000000), TradePrice: big.NewInt(1120000000)},
	}
	err := NewSpotEngine(sv).updateAccountBalancesFromStates(order, trades, states, sv)
	if err == nil || !strings.Contains(err.Error(), "same-side") {
		t.Fatalf("expected same-side error, got %v", err)
	}
	if len(sv.Diff()) != 0 {
		t.Fatalf("invalid trade pair must fail before balance writes, got diff=%d", len(sv.Diff()))
	}
}
