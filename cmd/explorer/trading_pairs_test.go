package main

import (
	"dex/pb"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
)

func TestMarketActivityFromBlockCountsAddOrdersBySemanticPair(t *testing.T) {
	block := &pb.Block{Body: []*pb.AnyTx{
		{Content: &pb.AnyTx_OrderTx{OrderTx: &pb.OrderTx{Base: &pb.BaseMessage{TxId: "spot-1"}, BaseToken: "br_base", QuoteToken: "br_quote", Op: pb.OrderOp_ADD}}},
		{Content: &pb.AnyTx_OrderTx{OrderTx: &pb.OrderTx{Base: &pb.BaseMessage{TxId: "spot-2"}, BaseToken: "br_base", QuoteToken: "br_quote", Op: pb.OrderOp_ADD}}},
		{Content: &pb.AnyTx_OrderTx{OrderTx: &pb.OrderTx{Base: &pb.BaseMessage{TxId: "cancel"}, Op: pb.OrderOp_REMOVE}}},
		{Content: &pb.AnyTx_FutureOrderTx{FutureOrderTx: &pb.FutureOrderTx{Base: &pb.BaseMessage{TxId: "future-1"}, Pair: "br_base_br_quote"}}},
	}}

	spot := marketActivityFromBlock(block, "spot")
	if got := spot["br_base_br_quote"]; got != 2 {
		t.Fatalf("spot activity=%d want=2", got)
	}
	future := marketActivityFromBlock(block, "future")
	if got := future["br_base_br_quote"]; got != 1 {
		t.Fatalf("future activity=%d want=1", got)
	}
}

func TestRankTradingPairsUsesLatestBlockActivityAndLimitsToOneHundred(t *testing.T) {
	pairs := make([]tradingPairInfo, 0, 105)
	for i := 0; i < 105; i++ {
		pair := fmt.Sprintf("pair-%03d", i)
		pairs = append(pairs, tradingPairInfo{Pair: pair, BaseToken: fmt.Sprintf("base-%03d", i), QuoteToken: "quote"})
	}
	activity := map[string]uint64{"pair-104": 3, "pair-103": 1}

	got := rankTradingPairs(pairs, activity, hotTradingPairLimit)
	if len(got) != hotTradingPairLimit {
		t.Fatalf("pair count=%d want=%d", len(got), hotTradingPairLimit)
	}
	if got[0].Pair != "pair-104" || got[0].Activity != 3 || got[1].Pair != "pair-103" || got[1].Activity != 1 {
		t.Fatalf("unexpected hot order: %+v", got[:2])
	}
}

func TestSelectRegisteredTradingPairsMapsExactAssetIDs(t *testing.T) {
	tokens := []tradingPairToken{
		{Address: "br_btc"},
		{Address: "br_arb_usdt"},
	}

	pairs := selectRegisteredTradingPairs(tokens, []string{"br_btc_br_arb_usdt"})
	if len(pairs) != 1 {
		t.Fatalf("pair count = %d, want 1: %+v", len(pairs), pairs)
	}
	pair := pairs[0]
	if pair.Pair != "br_btc_br_arb_usdt" || pair.BaseToken != "br_btc" || pair.QuoteToken != "br_arb_usdt" {
		t.Fatalf("address-key metadata mismatch: %+v", pair)
	}
}

func TestHandleTradingPairsUsesAuthoritativeFutureMarketDirection(t *testing.T) {
	tokens := tradingPairTestTokens()
	node := newTradingPairTestNode(t, tokens, http.StatusOK, `{"pairs":["br_btc_br_arb_usdt"]}`)
	s := &server{client: node.client, timeout: time.Second, defaultNodes: []string{node.address}}

	response := requestTradingPairs(t, s, node.address)
	if response.Error != "" {
		t.Fatalf("unexpected response error: %s", response.Error)
	}
	if len(response.Pairs) != 1 {
		t.Fatalf("pair count = %d, want one registered direction: %+v", len(response.Pairs), response.Pairs)
	}
	pair := response.Pairs[0]
	if pair.Pair != "br_btc_br_arb_usdt" || pair.BaseToken != "br_btc" || pair.QuoteToken != "br_arb_usdt" {
		t.Fatalf("registered pair metadata mismatch: %+v", pair)
	}
	if len(response.Tokens) != 2 || response.Tokens[0].NativeContract != "0xusdt" || response.Tokens[1].NativeChain != "btc" {
		t.Fatalf("token metadata was not preserved: %+v", response.Tokens)
	}
}

func TestHandleTradingPairsUsesIndependentSpotDirectory(t *testing.T) {
	tokens := tradingPairTestTokens()
	mux := http.NewServeMux()
	mux.HandleFunc("/listtokens", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(tokens)
	})
	mux.HandleFunc("/spot/markets", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"pairs":["br_btc_br_arb_usdt"]}`))
	})
	mux.HandleFunc("/future/markets", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"pairs":[]}`))
	})
	node := httptest.NewTLSServer(mux)
	defer node.Close()
	address := strings.TrimPrefix(node.URL, "https://")
	s := &server{client: node.Client(), timeout: time.Second, defaultNodes: []string{address}}

	response := requestTradingPairsForMarket(t, s, address, "spot")
	if response.Error != "" || len(response.Pairs) != 1 || response.Pairs[0].Pair != "br_btc_br_arb_usdt" {
		t.Fatalf("spot directory response=%+v", response)
	}
	response = requestTradingPairsForMarket(t, s, address, "future")
	if response.Error != "" || len(response.Pairs) != 0 {
		t.Fatalf("future directory response=%+v", response)
	}
}

func TestHandleTradingPairsRanksByLatestFinalizedBlockActivity(t *testing.T) {
	tokens := []tradingPairToken{{Address: "asset-a"}, {Address: "asset-b"}}
	mux := http.NewServeMux()
	mux.HandleFunc("/listtokens", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(tokens)
	})
	mux.HandleFunc("/spot/markets", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"pairs":["asset-a_asset-b","asset-b_asset-a"]}`))
	})
	mux.HandleFunc("/heightquery", func(w http.ResponseWriter, _ *http.Request) {
		data, _ := proto.Marshal(&pb.HeightResponse{LastAcceptedHeight: 7})
		_, _ = w.Write(data)
	})
	mux.HandleFunc("/getblock", func(w http.ResponseWriter, _ *http.Request) {
		order := func(id, base, quote string) *pb.AnyTx {
			return &pb.AnyTx{Content: &pb.AnyTx_OrderTx{OrderTx: &pb.OrderTx{
				Base: &pb.BaseMessage{TxId: id}, BaseToken: base, QuoteToken: quote, Op: pb.OrderOp_ADD,
			}}}
		}
		data, _ := proto.Marshal(&pb.GetBlockResponse{Block: &pb.Block{
			Header: &pb.BlockHeader{Height: 7},
			Body: []*pb.AnyTx{
				order("hot-1", "asset-b", "asset-a"),
				order("hot-2", "asset-b", "asset-a"),
				order("cold-1", "asset-a", "asset-b"),
			},
		}})
		_, _ = w.Write(data)
	})
	node := httptest.NewTLSServer(mux)
	defer node.Close()
	address := strings.TrimPrefix(node.URL, "https://")
	s := &server{client: node.Client(), timeout: time.Second, defaultNodes: []string{address}}

	response := requestTradingPairsForMarket(t, s, address, "spot")
	if response.Error != "" || response.ActivityHeight != 7 {
		t.Fatalf("response error/height=%q/%d", response.Error, response.ActivityHeight)
	}
	if len(response.Pairs) != 2 || response.Pairs[0].Pair != "asset-b_asset-a" || response.Pairs[0].Activity != 2 {
		t.Fatalf("unexpected ranked pairs: %+v", response.Pairs)
	}
}

func TestHandleTradingPairsRejectsUnavailableMarketDirectory(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   int
	}{
		{name: "missing endpoint", status: http.StatusNotFound, body: `{"error":"not found"}`, want: http.StatusBadGateway},
		{name: "invalid json", status: http.StatusOK, body: `{not-json`, want: http.StatusBadGateway},
		{name: "explicit error", status: http.StatusOK, body: `{"error":"registry unavailable"}`, want: http.StatusBadGateway},
		{name: "upstream overflow", status: http.StatusRequestEntityTooLarge, body: `{"error":"too many markets"}`, want: http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tokens := tradingPairTestTokens()
			node := newTradingPairTestNode(t, tokens, tc.status, tc.body)
			s := &server{client: node.client, timeout: time.Second, defaultNodes: []string{node.address}}

			response := requestTradingPairs(t, s, node.address)
			if response.Status != tc.want || response.Error == "" {
				t.Fatalf("status/error = %d/%q, want %d/non-empty", response.Status, response.Error, tc.want)
			}
			if len(response.Pairs) != 0 {
				t.Fatalf("upstream failure returned inferred pairs: %+v", response.Pairs)
			}
		})
	}
}

func TestHandleTradingPairsHonorsAuthoritativeEmptyDirectory(t *testing.T) {
	tokens := tradingPairTestTokens()
	node := newTradingPairTestNode(t, tokens, http.StatusOK, `{"pairs":[]}`)
	s := &server{client: node.client, timeout: time.Second, defaultNodes: []string{node.address}}

	response := requestTradingPairs(t, s, node.address)
	if response.Error != "" {
		t.Fatalf("unexpected response error: %s", response.Error)
	}
	if response.Pairs == nil || len(response.Pairs) != 0 {
		t.Fatalf("authoritative empty directory = %+v, want an empty array without fallback pairs", response.Pairs)
	}
}

func TestHandleTradingPairsPropagatesTokenDirectoryOverflow(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/listtokens", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "token directory exceeds hard limit", http.StatusRequestEntityTooLarge)
	})
	node := httptest.NewTLSServer(mux)
	defer node.Close()
	address := strings.TrimPrefix(node.URL, "https://")
	s := &server{client: node.Client(), timeout: time.Second, defaultNodes: []string{address}}

	response := requestTradingPairs(t, s, address)
	if response.Status != http.StatusRequestEntityTooLarge || response.Error == "" {
		t.Fatalf("status/error=%d/%q", response.Status, response.Error)
	}
}

type tradingPairTestNode struct {
	address string
	client  *http.Client
}

func newTradingPairTestNode(t *testing.T, tokens []tradingPairToken, marketStatus int, marketBody string) tradingPairTestNode {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/listtokens", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(tokens); err != nil {
			t.Errorf("encode token response: %v", err)
		}
	})
	mux.HandleFunc("/future/markets", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(marketStatus)
		_, _ = w.Write([]byte(marketBody))
	})
	mux.HandleFunc("/spot/markets", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(marketStatus)
		_, _ = w.Write([]byte(marketBody))
	})
	node := httptest.NewTLSServer(mux)
	t.Cleanup(node.Close)
	return tradingPairTestNode{
		address: strings.TrimPrefix(node.URL, "https://"),
		client:  node.Client(),
	}
}

func requestTradingPairs(t *testing.T, s *server, node string) struct {
	Tokens         []tradingPairToken `json:"tokens"`
	Pairs          []tradingPairInfo  `json:"pairs"`
	Error          string             `json:"error"`
	ActivityHeight uint64             `json:"activity_height"`
	Status         int                `json:"-"`
} {
	return requestTradingPairsForMarket(t, s, node, "future")
}

func requestTradingPairsForMarket(t *testing.T, s *server, node, market string) struct {
	Tokens         []tradingPairToken `json:"tokens"`
	Pairs          []tradingPairInfo  `json:"pairs"`
	Error          string             `json:"error"`
	ActivityHeight uint64             `json:"activity_height"`
	Status         int                `json:"-"`
} {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/tradingpairs?node="+url.QueryEscape(node)+"&market="+url.QueryEscape(market), nil)
	rec := httptest.NewRecorder()
	s.handleTradingPairs(rec, req)
	var response struct {
		Tokens         []tradingPairToken `json:"tokens"`
		Pairs          []tradingPairInfo  `json:"pairs"`
		Error          string             `json:"error"`
		ActivityHeight uint64             `json:"activity_height"`
		Status         int                `json:"-"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
	}
	response.Status = rec.Code
	return response
}

func tradingPairTestTokens() []tradingPairToken {
	                                                                         
	                                                                        
	return []tradingPairToken{
		{
			Address:        "br_arb_usdt",
			Type:           "bridged",
			NativeChain:    "arb",
			NativeContract: "0xusdt",
		},
		{
			Address:     "br_btc",
			Type:        "bridged",
			NativeChain: "btc",
		},
	}
}
