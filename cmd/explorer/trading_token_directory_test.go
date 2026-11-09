package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHandleTradingPairsPrefersStructuredIdentityWithUnderscores(t *testing.T) {
	tokens := []tradingPairToken{
		{Address: "a", Type: "native"},
		{Address: "b_c", Type: "native"},
		{Address: "c", Type: "system"},
		                                                                      
		                                                                   
	}
	marketBody := `{
		"pairs":["a_b_c"],
		"markets":[{"pair":"a_b_c","base_token":"a_b","quote_token":"c"}]
	}`
	node := newTradingPairTestNode(t, tokens, http.StatusOK, marketBody)
	s := &server{client: node.client, timeout: time.Second, defaultNodes: []string{node.address}}

	response := requestTradingPairs(t, s, node.address)
	if response.Error != "" || len(response.Pairs) != 1 {
		t.Fatalf("structured response error/pairs=%q/%+v", response.Error, response.Pairs)
	}
	pair := response.Pairs[0]
	if pair.Pair != "a_b_c" || pair.BaseToken != "a_b" || pair.QuoteToken != "c" {
		t.Fatalf("structured identity was guessed or changed: %+v", pair)
	}
	byAddress := make(map[string]tradingPairToken, len(response.Tokens))
	for _, token := range response.Tokens {
		byAddress[token.Address] = token
	}
	if len(byAddress) != 2 || byAddress["a_b"].Type != "native" || byAddress["c"].Type != "system" {
		t.Fatalf("referenced token metadata=%+v", response.Tokens)
	}
	if _, leaked := byAddress["a"]; leaked {
		t.Fatalf("unreferenced ambiguous token leaked into response: %+v", response.Tokens)
	}
}

func TestHandleTradingPairsMergesStructuredSubsetWithLegacyPairs(t *testing.T) {
	tokens := []tradingPairToken{
		{Address: "a_b", Type: "native"},
		{Address: "c", Type: "system"},
		{Address: "legacy_base", Type: "native"},
		{Address: "legacy_quote", Type: "native"},
	}
	marketBody := `{
		"pairs":["a_b_c","legacy_base_legacy_quote"],
		"markets":[{"pair":"a_b_c","base_token":"a_b","quote_token":"c"}]
	}`
	node := newTradingPairTestNode(t, tokens, http.StatusOK, marketBody)
	s := &server{client: node.client, timeout: time.Second, defaultNodes: []string{node.address}}

	response := requestTradingPairs(t, s, node.address)
	if response.Error != "" || len(response.Pairs) != 2 {
		t.Fatalf("merged response error/pairs=%q/%+v", response.Error, response.Pairs)
	}
	byPair := make(map[string]tradingPairInfo, len(response.Pairs))
	for _, pair := range response.Pairs {
		byPair[pair.Pair] = pair
	}
	if got := byPair["a_b_c"]; got.BaseToken != "a_b" || got.QuoteToken != "c" {
		t.Fatalf("structured pair changed: %+v", got)
	}
	if got := byPair["legacy_base_legacy_quote"]; got.BaseToken != "legacy_base" || got.QuoteToken != "legacy_quote" {
		t.Fatalf("legacy pair was hidden or misresolved: %+v", got)
	}
}

func TestHandleTradingPairsCachesTokenDirectoryAcrossRequests(t *testing.T) {
	var tokenCalls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/listtokens", func(w http.ResponseWriter, _ *http.Request) {
		tokenCalls.Add(1)
		_ = json.NewEncoder(w).Encode([]tradingPairToken{
			{Address: "base", Type: "native"},
			{Address: "quote", Type: "system"},
		})
	})
	mux.HandleFunc("/future/markets", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"markets":[{"pair":"base_quote","base_token":"base","quote_token":"quote"}],"pairs":["base_quote"]}`))
	})
	node := httptest.NewTLSServer(mux)
	defer node.Close()
	address := strings.TrimPrefix(node.URL, "https://")
	s := &server{client: node.Client(), timeout: time.Second, defaultNodes: []string{address}}

	for i := 0; i < 2; i++ {
		response := requestTradingPairs(t, s, address)
		if response.Error != "" || len(response.Pairs) != 1 {
			t.Fatalf("request %d response=%+v", i, response)
		}
	}
	if got := tokenCalls.Load(); got != 1 {
		t.Fatalf("token directory fetches=%d want=1", got)
	}
}

func TestHandleTradingPairsReturnsTokensOnlyForTopPairs(t *testing.T) {
	tokens := []tradingPairToken{{Address: "quote", Type: "system"}}
	markets := make([]tradingPairInfo, 0, hotTradingPairLimit+1)
	for i := 0; i <= hotTradingPairLimit; i++ {
		base := fmt.Sprintf("base-%03d", i)
		tokens = append(tokens, tradingPairToken{Address: base, Type: "native"})
		markets = append(markets, tradingPairInfo{
			Pair:       base + "_quote",
			BaseToken:  base,
			QuoteToken: "quote",
		})
	}
	marketPayload, err := json.Marshal(map[string]any{"markets": markets, "pairs": []string{}})
	if err != nil {
		t.Fatal(err)
	}
	node := newTradingPairTestNode(t, tokens, http.StatusOK, string(marketPayload))
	s := &server{client: node.client, timeout: time.Second, defaultNodes: []string{node.address}}

	response := requestTradingPairs(t, s, node.address)
	if response.Error != "" || len(response.Pairs) != hotTradingPairLimit {
		t.Fatalf("response error/pairs=%q/%d", response.Error, len(response.Pairs))
	}
	if len(response.Tokens) != hotTradingPairLimit+1 {
		t.Fatalf("token count=%d want=%d", len(response.Tokens), hotTradingPairLimit+1)
	}
	for _, token := range response.Tokens {
		if token.Address == "base-100" {
			t.Fatalf("token from pair outside top-%d leaked into response", hotTradingPairLimit)
		}
	}
}

func TestHandleTradingPairsLoadsPaginatedTokenDirectory(t *testing.T) {
	var tokenCalls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/listtokens", func(w http.ResponseWriter, r *http.Request) {
		tokenCalls.Add(1)
		if got := r.URL.Query().Get("limit"); got != "4096" {
			t.Errorf("page limit=%q want=4096", got)
		}
		switch r.URL.Query().Get("cursor") {
		case "":
			_ = json.NewEncoder(w).Encode(tradingTokenDirectoryPage{
				Tokens:     []tradingPairToken{{Address: "br_base", Type: "bridged", NativeChain: "btc"}},
				NextCursor: "page-2",
			})
		case "page-2":
			_ = json.NewEncoder(w).Encode(tradingTokenDirectoryPage{
				Tokens: []tradingPairToken{{Address: "br_quote", Type: "bridged", NativeChain: "arb"}},
			})
		default:
			http.Error(w, "unexpected cursor", http.StatusBadRequest)
		}
	})
	mux.HandleFunc("/future/markets", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"markets":[{"pair":"br_base_br_quote","base_token":"br_base","quote_token":"br_quote"}]}`))
	})
	node := httptest.NewTLSServer(mux)
	defer node.Close()
	address := strings.TrimPrefix(node.URL, "https://")
	s := &server{client: node.Client(), timeout: time.Second, defaultNodes: []string{address}}

	response := requestTradingPairs(t, s, address)
	if response.Error != "" || len(response.Tokens) != 2 || len(response.Pairs) != 1 {
		t.Fatalf("paginated response=%+v", response)
	}
	if got := tokenCalls.Load(); got != 2 {
		t.Fatalf("token pages fetched=%d want=2", got)
	}
}

func TestTradingTokenDirectoryRejectsRepeatedCursor(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/listtokens", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(tradingTokenDirectoryPage{
			Tokens:     []tradingPairToken{{Address: "only", Type: "native"}},
			NextCursor: "same",
		})
	})
	node := httptest.NewTLSServer(mux)
	defer node.Close()
	address := strings.TrimPrefix(node.URL, "https://")
	s := &server{client: node.Client(), timeout: time.Second, defaultNodes: []string{address}}

	_, err := s.getTradingTokenDirectory(t.Context(), address)
	if err == nil || (!strings.Contains(err.Error(), "duplicate address") && !strings.Contains(err.Error(), "cursor")) {
		t.Fatalf("repeated cursor error=%v", err)
	}
}

func TestHandleTokensFiltersTypeAndAddresses(t *testing.T) {
	var tokenCalls atomic.Int32
	tokens := []tradingPairToken{
		{Address: "bridge-a", Type: "bridged", NativeChain: "btc"},
		{Address: "native-a", Type: "native"},
		{Address: "bridge-b", Type: "bridged", NativeChain: "arb"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/listtokens", func(w http.ResponseWriter, _ *http.Request) {
		tokenCalls.Add(1)
		_ = json.NewEncoder(w).Encode(tokens)
	})
	node := httptest.NewTLSServer(mux)
	defer node.Close()
	address := strings.TrimPrefix(node.URL, "https://")
	s := &server{client: node.Client(), timeout: time.Second, defaultNodes: []string{address}}

	first := requestTokens(t, s, "/api/tokens?node="+url.QueryEscape(address)+"&type=bridged&limit=1")
	if first.Status != http.StatusOK || len(first.Tokens) != 1 || first.Tokens[0].Address != "bridge-a" || first.NextCursor == "" {
		t.Fatalf("first bridged page=%+v", first)
	}
	second := requestTokens(t, s, "/api/tokens?node="+url.QueryEscape(address)+"&type=bridged&limit=1&cursor="+url.QueryEscape(first.NextCursor))
	if second.Status != http.StatusOK || len(second.Tokens) != 1 || second.Tokens[0].Address != "bridge-b" || second.NextCursor != "" {
		t.Fatalf("second bridged page=%+v", second)
	}
	exact := requestTokens(t, s, "/api/tokens?node="+url.QueryEscape(address)+"&addresses=native-a,missing,bridge-b")
	if exact.Status != http.StatusOK || len(exact.Tokens) != 2 || exact.Tokens[0].Address != "native-a" || exact.Tokens[1].Address != "bridge-b" {
		t.Fatalf("exact token response=%+v", exact)
	}
	if got := tokenCalls.Load(); got != 1 {
		t.Fatalf("shared token directory fetches=%d want=1", got)
	}

	missingFilter := requestTokens(t, s, "/api/tokens?node="+url.QueryEscape(address))
	if missingFilter.Status != http.StatusBadRequest || missingFilter.Error == "" {
		t.Fatalf("unfiltered response=%+v", missingFilter)
	}
	invalidType := requestTokens(t, s, "/api/tokens?node="+url.QueryEscape(address)+"&type=native")
	if invalidType.Status != http.StatusBadRequest || invalidType.Error == "" {
		t.Fatalf("invalid type response=%+v", invalidType)
	}
}

type tokenQueryTestResponse struct {
	Tokens     []tradingPairToken `json:"tokens"`
	NextCursor string             `json:"next_cursor"`
	Error      string             `json:"error"`
	Status     int                `json:"-"`
}

func requestTokens(t *testing.T, s *server, target string) tokenQueryTestResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	s.handleTokens(rec, req)
	var response tokenQueryTestResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode token response: %v; body=%s", err, rec.Body.String())
	}
	response.Status = rec.Code
	return response
}
