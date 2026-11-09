package handlers

import (
	"dex/interfaces"
	"dex/keys"
	"dex/protocol"
	"encoding/json"
	"reflect"
	"testing"

	"net/http/httptest"
)

func TestHandleSpotMarketsUnionsExplicitAndTradedMarkets(t *testing.T) {
	explicit, err := protocol.EncodeMarketIdentity("FB", "br_quote")
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handleSpotMarketsWithScan(recorder, func(prefix string, _ int, _ bool) ([]interfaces.KVPair, error) {
		switch prefix {
		case keys.KeySpotMarketConfigPrefix():
			return []interfaces.KVPair{
				{Key: keys.KeySpotMarketConfig("FB_br_quote"), Value: explicit},
			}, nil
		case keys.KeySpotLastPricePrefix():
			return []interfaces.KVPair{
				{Key: keys.KeySpotLastPrice("FB_br_quote")},
				{Key: keys.KeySpotLastPrice("BTC_USDT")},
				{Key: keys.KeySpotLastPrice("br_base_br_quote")},
			}, nil
		default:
			t.Fatalf("unexpected prefix %q", prefix)
			return nil, nil
		}
	})
	var got futureMarketsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := []string{"BTC_USDT", "FB_br_quote", "br_base_br_quote"}
	if !reflect.DeepEqual(got.Pairs, want) {
		t.Fatalf("pairs=%v want=%v", got.Pairs, want)
	}
	wantMarkets := []marketDirectoryEntry{
		{Pair: "BTC_USDT", BaseToken: "BTC", QuoteToken: "USDT"},
		{Pair: "FB_br_quote", BaseToken: "FB", QuoteToken: "br_quote"},
	}
	if !reflect.DeepEqual(got.Markets, wantMarkets) {
		t.Fatalf("markets=%+v want=%+v", got.Markets, wantMarkets)
	}
}

func TestHandleSpotMarketsLegacyFallbackDoesNotGuessAmbiguousPairs(t *testing.T) {
	recorder := httptest.NewRecorder()
	handleSpotMarketsWithScan(recorder, func(prefix string, _ int, _ bool) ([]interfaces.KVPair, error) {
		switch prefix {
		case keys.KeySpotMarketConfigPrefix():
			return []interfaces.KVPair{
				{Key: keys.KeySpotMarketConfig("ETH_USDT"), Value: []byte("ETH_USDT")},
				{Key: keys.KeySpotMarketConfig("br_base_br_quote"), Value: []byte("br_base_br_quote")},
				{Key: keys.KeySpotMarketConfig("CORRUPT_USDT"), Value: []byte("not-a-legacy-marker")},
			}, nil
		case keys.KeySpotLastPricePrefix():
			return nil, nil
		default:
			t.Fatalf("unexpected prefix %q", prefix)
			return nil, nil
		}
	})
	var got futureMarketsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	wantPairs := []string{"CORRUPT_USDT", "ETH_USDT", "br_base_br_quote"}
	if !reflect.DeepEqual(got.Pairs, wantPairs) {
		t.Fatalf("pairs=%v want=%v", got.Pairs, wantPairs)
	}
	wantMarkets := []marketDirectoryEntry{{Pair: "ETH_USDT", BaseToken: "ETH", QuoteToken: "USDT"}}
	if !reflect.DeepEqual(got.Markets, wantMarkets) {
		t.Fatalf("markets=%+v want=%+v", got.Markets, wantMarkets)
	}
}
