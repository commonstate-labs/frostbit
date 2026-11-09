package vm

import (
	"dex/future"
	"dex/keys"
	"dex/pb"
	"math/big"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
)

func putCloseTestPosition(t *testing.T, sv StateView, id, owner, pair string, side future.Side, qty, entry *big.Int, leverage uint64) {
	t.Helper()
	margin := future.CalcRequiredMargin(qty, entry, leverage)
	writeFuturePosition(t, sv, &pb.FuturePositionState{
		PositionId: id, Owner: owner, Pair: pair, Side: int32(side),
		EntryPrice: entry.String(), Quantity: qty.String(), Margin: margin.String(),
		Status: int32(future.StatusOpen), OpenHeight: 1,
		EntryNotionalNumerator: new(big.Int).Mul(qty, entry).String(),
		InitialMargin:          margin.String(),
		MaxQuantity:            qty.String(),
		MarginBasisAmount:      margin.String(),
		MarginBasisClosed:      "0",
		RealizedPnl:            "0",
		ClosedQty:              "0",
		ExitNotional:           "0",
		ClosePnlRemainder:      "0",
	})
}

func readFutureOrderHistoryForCloseTest(t *testing.T, sv StateView, owner, pair string, status pb.FutureOrderStatus, height uint64, orderID string) *pb.FutureOrderState {
	t.Helper()
	data, exists, err := sv.Get(keys.KeyFutureAccountOrder(owner, pair, status, height, orderID))
	if err != nil || !exists {
		t.Fatalf("read close order history: exists=%v err=%v", exists, err)
	}
	var state pb.FutureOrderState
	if err := proto.Unmarshal(data, &state); err != nil {
		t.Fatalf("unmarshal close order history: %v", err)
	}
	return &state
}

func TestFutureCloseOrderRejectsNonOwner(t *testing.T) {
	sv := newMemStateView()
	pair := "CLOSE_OWNER_USDT"
	qty := new(big.Int).Set(future.PriceScale)
	price := new(big.Int).Mul(big.NewInt(100), future.PriceScale)
	sv.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
	putCloseTestPosition(t, sv, "alice-long", "alice", pair, future.Long, qty, price, 10)

	_, err := NewFutureMatchEngine(sv, 2).CloseOrder(
		"mallory", pair, "USDT", future.Short, qty, price, 10, false, 0, "alice-long",
		&pb.BaseMessage{TxId: "steal-close", FromAddress: "mallory", ExecutedHeight: 2},
	)
	if err == nil || !containsAll(err.Error(), "owner mismatch", "alice", "mallory") {
		t.Fatalf("expected owner rejection, got %v", err)
	}
	if back := readFuturePosition(t, sv, "alice-long"); back.Quantity != qty.String() || back.Status != int32(future.StatusOpen) {
		t.Fatalf("non-owner close mutated position: %+v", back)
	}
}

func TestFutureCloseOrderNoLiquidityIsSuccessfulIOCWithoutMarkFallback(t *testing.T) {
	sv := newMemStateView()
	pair := "CLOSE_EMPTY_USDT"
	qty := new(big.Int).Set(future.PriceScale)
	price := new(big.Int).Mul(big.NewInt(100), future.PriceScale)
	sv.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
	sv.Set(keys.KeyFutureMarkPrice(pair), new(big.Int).Mul(big.NewInt(1_000), future.PriceScale).Bytes())
	putCloseTestPosition(t, sv, "alice-long", "alice", pair, future.Long, qty, price, 10)
	giveBalance(t, sv, "alice", "USDT", big.NewInt(1_000_000_000_000_000))

	res, err := NewFutureMatchEngine(sv, 2).CloseOrder(
		"alice", pair, "USDT", future.Short, qty, price, 10, false, 0, "alice-long",
		&pb.BaseMessage{TxId: "empty-close", FromAddress: "alice", ExecutedHeight: 2},
	)
	if err != nil {
		t.Fatalf("empty IOC close failed: %v", err)
	}
	if res.MatchedQty.Sign() != 0 || res.RemainQty.Cmp(qty) != 0 || res.FullyClosed {
		t.Fatalf("unexpected empty close result: %+v", res)
	}
	back := readFuturePosition(t, sv, "alice-long")
	if back.Quantity != qty.String() || back.Status != int32(future.StatusOpen) {
		t.Fatalf("empty close changed position: %+v", back)
	}
	if _, exists, _ := sv.Get(keys.KeyFutureOrderState("empty-close")); exists {
		t.Fatal("IOC close remainder became an open order")
	}
	if _, exists, _ := sv.Get(keys.KeyFutureOrderReservedMargin("empty-close")); exists {
		t.Fatal("IOC close created reserved margin")
	}
	history := readFutureOrderHistoryForCloseTest(t, sv, "alice", pair, pb.FutureOrderStatus_FUTURE_ORDER_CLOSED, 2, "empty-close")
	if history.FilledBase != "0" || history.IsClose != true || history.ClosePositionId != "alice-long" {
		t.Fatalf("bad empty close history: %+v", history)
	}
}

func TestFutureCloseOrderPartialFillIsIOCAndReportsActualFill(t *testing.T) {
	sv := newMemStateView()
	pair := "CLOSE_PARTIAL_USDT"
	unit := new(big.Int).Set(future.PriceScale)
	qty := new(big.Int).Mul(big.NewInt(2), unit)
	price := new(big.Int).Mul(big.NewInt(100), future.PriceScale)
	sv.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
	putCloseTestPosition(t, sv, "alice-long", "alice", pair, future.Long, qty, price, 10)
	giveBalance(t, sv, "alice", "USDT", big.NewInt(1_000_000_000_000_000))
	seedFutureMakerForCloseTest(t, sv, pair, "one-buy", "bob", future.Long, unit, price, 10, 1)

	res, err := NewFutureMatchEngine(sv, 2).CloseOrder(
		"alice", pair, "USDT", future.Short, qty, price, 10, false, 0, "alice-long",
		&pb.BaseMessage{TxId: "partial-close", FromAddress: "alice", ExecutedHeight: 2},
	)
	if err != nil {
		t.Fatalf("partial close failed: %v", err)
	}
	if res.MatchedQty.Cmp(unit) != 0 || res.RemainQty.Cmp(unit) != 0 || res.Notional.Cmp(price) != 0 || res.FullyClosed {
		t.Fatalf("unexpected partial close result: %+v", res)
	}
	back := readFuturePosition(t, sv, "alice-long")
	if back.Quantity != unit.String() || back.Status != int32(future.StatusOpen) {
		t.Fatalf("partial close position: %+v", back)
	}
	if _, exists, _ := sv.Get(keys.KeyFutureOrderState("partial-close")); exists {
		t.Fatal("partial IOC remainder stayed on book")
	}
	if frozen := parseBigOrZero(GetBalance(sv, "alice", "USDT").OrderFrozenBalance); frozen.Sign() != 0 {
		t.Fatalf("partial IOC close froze margin: %s", frozen)
	}
	history := readFutureOrderHistoryForCloseTest(t, sv, "alice", pair, pb.FutureOrderStatus_FUTURE_ORDER_CLOSED, 2, "partial-close")
	if history.Amount != qty.String() || history.FilledBase != unit.String() || history.FilledQuote != price.String() {
		t.Fatalf("partial close history mismatch: %+v", history)
	}
	foundFill := false
	for _, event := range res.Events {
		if event.Kind == EventOrderMatched && event.Payload["maker_order_id"] == "one-buy" && event.Payload["quantity"] == unit.String() && event.Payload["price"] == price.String() {
			foundFill = true
		}
	}
	if !foundFill {
		t.Fatalf("missing exact ORDER_MATCHED event: %+v", res.Events)
	}
}

func TestFutureCloseOrderHonorsLimitAndMarketSlippage(t *testing.T) {
	unit := new(big.Int).Set(future.PriceScale)
	reference := new(big.Int).Mul(big.NewInt(100), future.PriceScale)
	makerPrice := new(big.Int).Mul(big.NewInt(99), future.PriceScale)
	tests := []struct {
		name       string
		isMarket   bool
		slippage   uint32
		wantFilled bool
	}{
		{name: "limit rejects worse bid", isMarket: false, wantFilled: false},
		{name: "market rejects beyond slippage", isMarket: true, slippage: 50, wantFilled: false},
		{name: "market accepts inside slippage", isMarket: true, slippage: 200, wantFilled: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sv := newMemStateView()
			pair := "CLOSE_PRICE_USDT"
			sv.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
			putCloseTestPosition(t, sv, "alice-long", "alice", pair, future.Long, unit, makerPrice, 10)
			giveBalance(t, sv, "alice", "USDT", big.NewInt(1_000_000_000_000_000))
			seedFutureMakerForCloseTest(t, sv, pair, "bid-99", "bob", future.Long, unit, makerPrice, 10, 1)

			res, err := NewFutureMatchEngine(sv, 2).CloseOrder(
				"alice", pair, "USDT", future.Short, unit, reference, 10, tt.isMarket, tt.slippage, "alice-long",
				&pb.BaseMessage{TxId: "priced-close", FromAddress: "alice", ExecutedHeight: 2},
			)
			if err != nil {
				t.Fatalf("priced close failed: %v", err)
			}
			if gotFilled := res.MatchedQty.Sign() > 0; gotFilled != tt.wantFilled {
				t.Fatalf("filled=%v, want %v result=%+v", gotFilled, tt.wantFilled, res)
			}
		})
	}
}

func TestFutureCloseOrderCapsQuantityAndSkipsOwnMaker(t *testing.T) {
	t.Run("requested excess cannot reverse", func(t *testing.T) {
		sv := newMemStateView()
		pair := "CLOSE_CAP_USDT"
		unit := new(big.Int).Set(future.PriceScale)
		requested := new(big.Int).Mul(big.NewInt(2), unit)
		price := new(big.Int).Mul(big.NewInt(100), future.PriceScale)
		sv.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
		putCloseTestPosition(t, sv, "alice-long", "alice", pair, future.Long, unit, price, 10)
		giveBalance(t, sv, "alice", "USDT", big.NewInt(1_000_000_000_000_000))
		seedFutureMakerForCloseTest(t, sv, pair, "large-bid", "bob", future.Long, requested, price, 10, 1)

		res, err := NewFutureMatchEngine(sv, 2).CloseOrder(
			"alice", pair, "USDT", future.Short, requested, price, 10, false, 0, "alice-long",
			&pb.BaseMessage{TxId: "capped-close", FromAddress: "alice", ExecutedHeight: 2},
		)
		if err != nil {
			t.Fatalf("capped close failed: %v", err)
		}
		if res.MatchedQty.Cmp(unit) != 0 || res.RemainQty.Cmp(unit) != 0 || !res.FullyClosed {
			t.Fatalf("capped close result: %+v", res)
		}
		if short, err := (&svPositionStore{sv: sv}).GetPositionByOwnerAndPairSide("alice", pair, future.Short); err != nil || short != nil {
			t.Fatalf("reduce-only close opened reverse position: pos=%+v err=%v", short, err)
		}
		makerData, exists, _ := sv.Get(keys.KeyFutureOrderState("large-bid"))
		if !exists {
			t.Fatal("excess quantity consumed more maker depth than the position")
		}
		var maker pb.FutureOrderState
		_ = proto.Unmarshal(makerData, &maker)
		if maker.FilledBase != unit.String() {
			t.Fatalf("maker filled=%s, want capped %s", maker.FilledBase, unit)
		}
	})

	t.Run("own maker cannot neutralize reduce-only", func(t *testing.T) {
		sv := newMemStateView()
		pair := "CLOSE_SELF_USDT"
		unit := new(big.Int).Set(future.PriceScale)
		price := new(big.Int).Mul(big.NewInt(100), future.PriceScale)
		sv.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
		putCloseTestPosition(t, sv, "alice-long", "alice", pair, future.Long, unit, price, 10)
		seedFutureMakerForCloseTest(t, sv, pair, "alice-own-bid", "alice", future.Long, unit, price, 10, 1)

		res, err := NewFutureMatchEngine(sv, 2).CloseOrder(
			"alice", pair, "USDT", future.Short, unit, price, 10, false, 0, "alice-long",
			&pb.BaseMessage{TxId: "self-close", FromAddress: "alice", ExecutedHeight: 2},
		)
		if err != nil {
			t.Fatalf("self-maker close failed: %v", err)
		}
		if res.MatchedQty.Sign() != 0 {
			t.Fatalf("reduce-only close matched own maker: %+v", res)
		}
		if back := readFuturePosition(t, sv, "alice-long"); back.Quantity != unit.String() {
			t.Fatalf("own-maker close changed target: %+v", back)
		}
		if _, exists, _ := sv.Get(keys.KeyFutureOrderState("alice-own-bid")); !exists {
			t.Fatal("own maker was consumed or removed")
		}
	})
}

func TestFutureCloseOrderReportsActualPnLAndPreservesQuote(t *testing.T) {
	t.Run("loss enters insurance", func(t *testing.T) {
		sv := newMemStateView()
		pair := "CLOSE_LOSS_USDT"
		unit := new(big.Int).Set(future.PriceScale)
		entry := new(big.Int).Mul(big.NewInt(100), unit)
		exit := new(big.Int).Mul(big.NewInt(90), unit)
		sv.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
		putCloseTestPosition(t, sv, "alice-long", "alice", pair, future.Long, unit, entry, 10)
		giveBalance(t, sv, "alice", "USDT", big.NewInt(1_000_000_000_000_000))
		seedFutureMakerForCloseTest(t, sv, pair, "loss-bid", "bob", future.Long, unit, exit, 10, 1)
		before := futureQuoteConservationTotal(t, sv, pair, "USDT")

		res, err := NewFutureMatchEngine(sv, 2).CloseOrder(
			"alice", pair, "USDT", future.Short, unit, exit, 10, false, 0, "alice-long",
			&pb.BaseMessage{TxId: "loss-close", FromAddress: "alice", ExecutedHeight: 2},
		)
		if err != nil {
			t.Fatalf("loss close failed: %v", err)
		}
		wantLoss := new(big.Int).Neg(new(big.Int).Mul(big.NewInt(10), unit))
		if res.RealizedPnL.Cmp(wantLoss) != 0 || res.PnLShortfall.Sign() != 0 || !res.FullyClosed {
			t.Fatalf("loss result pnl/shortfall/closed=%s/%s/%t, want %s/0/true", res.RealizedPnL, res.PnLShortfall, res.FullyClosed, wantLoss)
		}
		after := futureQuoteConservationTotal(t, sv, pair, "USDT")
		if after.Cmp(before) != 0 {
			t.Fatalf("quote conservation changed: before=%s after=%s", before, after)
		}
	})

	t.Run("profit is limited by insurance and reports shortfall", func(t *testing.T) {
		sv := newMemStateView()
		pair := "CLOSE_SHORTFALL_USDT"
		unit := new(big.Int).Set(future.PriceScale)
		entry := new(big.Int).Mul(big.NewInt(90), unit)
		exit := new(big.Int).Mul(big.NewInt(100), unit)
		availableInsurance := new(big.Int).Mul(big.NewInt(4), unit)
		sv.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
		sv.Set(keys.KeyFutureInsuranceFund(pair), availableInsurance.Bytes())
		putCloseTestPosition(t, sv, "alice-long", "alice", pair, future.Long, unit, entry, 10)
		giveBalance(t, sv, "alice", "USDT", big.NewInt(1_000_000_000_000_000))
		seedFutureMakerForCloseTest(t, sv, pair, "profit-bid", "bob", future.Long, unit, exit, 10, 1)
		before := futureQuoteConservationTotal(t, sv, pair, "USDT")

		res, err := NewFutureMatchEngine(sv, 2).CloseOrder(
			"alice", pair, "USDT", future.Short, unit, exit, 10, false, 0, "alice-long",
			&pb.BaseMessage{TxId: "profit-close", FromAddress: "alice", ExecutedHeight: 2},
		)
		if err != nil {
			t.Fatalf("profit close failed: %v", err)
		}
		wantShortfall := new(big.Int).Mul(big.NewInt(6), unit)
		if res.RealizedPnL.Cmp(availableInsurance) != 0 || res.PnLShortfall.Cmp(wantShortfall) != 0 || !res.FullyClosed {
			t.Fatalf("profit result pnl/shortfall/closed=%s/%s/%t, want %s/%s/true", res.RealizedPnL, res.PnLShortfall, res.FullyClosed, availableInsurance, wantShortfall)
		}
		foundSummary := false
		for _, event := range res.Events {
			if event.Kind == EventPositionClosed &&
				event.Payload["realized_pnl"] == availableInsurance.String() &&
				event.Payload["pnl_shortfall"] == wantShortfall.String() &&
				event.Payload["fully_closed"] == "true" {
				foundSummary = true
			}
		}
		if !foundSummary {
			t.Fatalf("close event omitted actual PnL summary: %+v", res.Events)
		}
		after := futureQuoteConservationTotal(t, sv, pair, "USDT")
		if after.Cmp(before) != 0 {
			t.Fatalf("quote conservation changed: before=%s after=%s", before, after)
		}
	})
}

func containsAll(s string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(s, part) {
			return false
		}
	}
	return true
}
