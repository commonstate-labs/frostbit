package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"dex/interfaces"
	"dex/keys"
	"dex/pb"
	"dex/protocol"

	"google.golang.org/protobuf/proto"
)

func futureOrderBookRowsForTest(t *testing.T, count int, side pb.OrderSide) []interfaces.KVPair {
	t.Helper()
	rows := make([]interfaces.KVPair, 0, count)
	for price := 1; price <= count; price++ {
		order := &pb.FutureOrderState{
			OrderId:    fmt.Sprintf("order-%04d", price),
			Owner:      "maker",
			Side:       int32(side),
			Price:      strconv.Itoa(price),
			Amount:     "1",
			FilledBase: "0",
			Leverage:   10,
			Status:     pb.FutureOrderStatus_FUTURE_ORDER_OPEN,
		}
		data, err := proto.Marshal(order)
		if err != nil {
			t.Fatalf("marshal future order %d: %v", price, err)
		}
		rows = append(rows, interfaces.KVPair{Key: fmt.Sprintf("row-%04d", price), Value: data})
	}
	return rows
}

func boundedOrderedRows(rows []interfaces.KVPair, limit int, reverse bool) []interfaces.KVPair {
	if limit > len(rows) {
		limit = len(rows)
	}
	out := make([]interfaces.KVPair, 0, limit)
	if reverse {
		for i := len(rows) - 1; i >= 0 && len(out) < limit; i-- {
			out = append(out, rows[i])
		}
		return out
	}
	return append(out, rows[:limit]...)
}

func TestHandleFutureOrderBookCapsEachSideAfterBestPriceSort(t *testing.T) {
	pair := "BTC_USDT"
	total := futureOrderBookSideLimit + 120
	buyRows := futureOrderBookRowsForTest(t, total, pb.OrderSide_BUY)
	sellRows := futureOrderBookRowsForTest(t, total, pb.OrderSide_SELL)

	request := httptest.NewRequest(http.MethodGet, "/future/orderbook?pair="+pair, nil)
	recorder := httptest.NewRecorder()
	handleFutureOrderBookWithScan(recorder, request, func(prefix string, limit int, reverse bool) ([]interfaces.KVPair, error) {
		if limit != futureOrderBookSideLimit {
			t.Fatalf("scan limit=%d, want %d", limit, futureOrderBookSideLimit)
		}
		switch prefix {
		case keys.KeyFutureOrderPriceIndexPrefix(pair, pb.OrderSide_BUY, false):
			if !reverse {
				t.Fatal("buy side must use reverse scan for best price first")
			}
			return boundedOrderedRows(buyRows, limit, reverse), nil
		case keys.KeyFutureOrderPriceIndexPrefix(pair, pb.OrderSide_SELL, false):
			if reverse {
				t.Fatal("sell side must use forward scan for best price first")
			}
			return boundedOrderedRows(sellRows, limit, reverse), nil
		default:
			return nil, fmt.Errorf("unexpected prefix %q", prefix)
		}
	})

	var got struct {
		Pair string                 `json:"pair"`
		Bids []futureOrderBookEntry `json:"bids"`
		Asks []futureOrderBookEntry `json:"asks"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode orderbook response %q: %v", recorder.Body.String(), err)
	}
	if got.Pair != pair {
		t.Fatalf("pair=%q, want %q", got.Pair, pair)
	}
	if len(got.Bids) != futureOrderBookSideLimit || len(got.Asks) != futureOrderBookSideLimit {
		t.Fatalf("depth bids/asks=%d/%d, want %d/%d", len(got.Bids), len(got.Asks), futureOrderBookSideLimit, futureOrderBookSideLimit)
	}
	for i := 0; i < futureOrderBookSideLimit; i++ {
		wantBid := strconv.Itoa(total - i)
		wantAsk := strconv.Itoa(i + 1)
		if got.Bids[i].Price != wantBid {
			t.Fatalf("bid[%d]=%s, want best-price order %s", i, got.Bids[i].Price, wantBid)
		}
		if got.Asks[i].Price != wantAsk {
			t.Fatalf("ask[%d]=%s, want best-price order %s", i, got.Asks[i].Price, wantAsk)
		}
	}
}

func TestHandleFutureOrderBookReturnsEitherSideScanError(t *testing.T) {
	for _, tc := range []struct {
		name     string
		failCall int
		wantSide string
	}{
		{name: "bids", failCall: 1, wantSide: "bids"},
		{name: "asks", failCall: 2, wantSide: "asks"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/future/orderbook?pair=BTC_USDT", nil)
			handleFutureOrderBookWithScan(recorder, request, func(string, int, bool) ([]interfaces.KVPair, error) {
				calls++
				if calls == tc.failCall {
					return nil, errors.New("injected scan failure")
				}
				return []interfaces.KVPair{}, nil
			})

			var got map[string]json.RawMessage
			if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode error response %q: %v", recorder.Body.String(), err)
			}
			var message string
			if err := json.Unmarshal(got["error"], &message); err != nil {
				t.Fatalf("missing explicit error in %q: %v", recorder.Body.String(), err)
			}
			if !strings.Contains(message, tc.wantSide) || !strings.Contains(message, "injected scan failure") {
				t.Fatalf("error=%q, want side %q and scan cause", message, tc.wantSide)
			}
			if _, exists := got["bids"]; exists {
				t.Fatalf("scan failure was disguised as an orderbook: %s", recorder.Body.String())
			}
		})
	}
}

func TestHandleFutureMarketsReturnsSortedUniquePairs(t *testing.T) {
	ethIdentity, err := protocol.EncodeMarketIdentity("ETH", "USDT")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name        string
		rows        []interfaces.KVPair
		wantPairs   []string
		wantMarkets []marketDirectoryEntry
	}{
		{
			name: "sorted and deduplicated",
			rows: []interfaces.KVPair{
				{Key: keys.KeyFutureMarketConfig("ETH_USDT"), Value: ethIdentity},
				{Key: keys.KeyFutureMarketConfig("BTC_USDT"), Value: []byte("BTC_USDT")},
				{Key: keys.KeyFutureMarketConfig("br_base_br_quote"), Value: []byte("br_base_br_quote")},
				{Key: keys.KeyFutureMarketConfig("ETH_USDT")},
				{Key: keys.KeyFutureMarketConfigPrefix()},
			},
			wantPairs: []string{"BTC_USDT", "ETH_USDT", "br_base_br_quote"},
			wantMarkets: []marketDirectoryEntry{
				{Pair: "BTC_USDT", BaseToken: "BTC", QuoteToken: "USDT"},
				{Pair: "ETH_USDT", BaseToken: "ETH", QuoteToken: "USDT"},
			},
		},
		{name: "empty is array", rows: []interfaces.KVPair{}, wantPairs: []string{}, wantMarkets: []marketDirectoryEntry{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handleFutureMarketsWithScan(recorder, func(prefix string, limit int, reverse bool) ([]interfaces.KVPair, error) {
				if prefix != keys.KeyFutureMarketConfigPrefix() {
					t.Fatalf("scan prefix=%q, want %q", prefix, keys.KeyFutureMarketConfigPrefix())
				}
				if limit != futureMarketDirectoryLimit+1 || reverse {
					t.Fatalf("scan bounds limit=%d reverse=%v", limit, reverse)
				}
				return tc.rows, nil
			})

			var got futureMarketsResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode markets response %q: %v", recorder.Body.String(), err)
			}
			if !reflect.DeepEqual(got.Pairs, tc.wantPairs) {
				t.Fatalf("pairs=%v, want %v", got.Pairs, tc.wantPairs)
			}
			if !reflect.DeepEqual(got.Markets, tc.wantMarkets) {
				t.Fatalf("markets=%+v, want %+v", got.Markets, tc.wantMarkets)
			}
		})
	}
}

func TestHandleFutureMarketsReturnsScanError(t *testing.T) {
	recorder := httptest.NewRecorder()
	handleFutureMarketsWithScan(recorder, func(string, int, bool) ([]interfaces.KVPair, error) {
		return nil, errors.New("registry unavailable")
	})

	var got map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode markets error %q: %v", recorder.Body.String(), err)
	}
	if !strings.Contains(got["error"], "scan future markets") || !strings.Contains(got["error"], "registry unavailable") {
		t.Fatalf("error=%q, want explicit registry scan failure", got["error"])
	}
}

func TestHandleFutureMarketsRejectsOverflow(t *testing.T) {
	recorder := httptest.NewRecorder()
	handleFutureMarketsWithScan(recorder, func(string, int, bool) ([]interfaces.KVPair, error) {
		return make([]interfaces.KVPair, futureMarketDirectoryLimit+1), nil
	})
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d body=%s, want %d", recorder.Code, recorder.Body.String(), http.StatusRequestEntityTooLarge)
	}
}
