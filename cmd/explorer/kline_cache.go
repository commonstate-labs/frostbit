package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	explorerKLineMarketSpot    = "spot"
	explorerKLineMarketFuture  = "future"
	explorerKLineDefaultCount  = 1000
	explorerKLineMaxCount      = 5000
	explorerKLineMinTradeLimit = 256
	explorerKLineMaxTradeLimit = 50000
	explorerKLineCacheDuration = 10 * time.Second
)

var explorerKLineAllowedBuckets = []uint64{1, 5, 20, 100}

type explorerKLineCandle struct {
	StartHeight     uint64 `json:"start_height"`
	EndHeight       uint64 `json:"end_height"`
	LastTradeHeight uint64 `json:"last_trade_height,omitempty"`
	Open            string `json:"open"`
	High            string `json:"high"`
	Low             string `json:"low"`
	Close           string `json:"close"`
	Volume          string `json:"volume"`
	Trades          uint64 `json:"trades"`
	Liquidations    uint64 `json:"liquidations,omitempty"`
}

type explorerKLineResponse struct {
	Market  string                           `json:"market"`
	Pair    string                           `json:"pair"`
	Buckets map[string][]explorerKLineCandle `json:"buckets"`
}

var explorerKLineCache = newBoundedTTLCache[explorerKLineResponse](256, 64<<20, 8)

func (s *server) handleKLines(w http.ResponseWriter, r *http.Request) {
	s.handleExplorerKLines(w, r, explorerKLineMarketSpot)
}

func (s *server) handleFutureKLines(w http.ResponseWriter, r *http.Request) {
	s.handleExplorerKLines(w, r, explorerKLineMarketFuture)
}

func (s *server) handleExplorerKLines(w http.ResponseWriter, r *http.Request, market string) {
	node := s.pickNode(r.URL.Query().Get("node"))
	pair := r.URL.Query().Get("pair")
	if node == "" || pair == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(APIError{Error: "missing node or pair parameter", Code: "INVALID_PARAMS"})
		return
	}
	count := parseExplorerKLineCount(r.URL.Query().Get("count"))
	buckets := parseExplorerKLineBuckets(r.URL.Query().Get("buckets"))

	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()
	resp, err := s.getExplorerKLines(ctx, node, market, pair, count, buckets)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(APIError{Error: "failed to build klines", Code: "NODE_ERROR", Details: err.Error()})
		return
	}
	writeJSON(w, resp)
}

func (s *server) getExplorerKLines(ctx context.Context, node, market, pair string, count int, buckets []uint64) (explorerKLineResponse, error) {
	if count <= 0 {
		count = explorerKLineDefaultCount
	}
	if len(buckets) == 0 {
		buckets = append([]uint64(nil), explorerKLineAllowedBuckets...)
	}
	                                                                         
	                                                                          
	                                                                  
	cacheCount := explorerKLineCacheCount(count)
	cacheKey := fmt.Sprintf("%s|%s|%s|%d|%s", market, node, pair, cacheCount, explorerKLineBucketKey(buckets))
	base, err := explorerKLineCache.GetOrLoad(ctx, cacheKey, explorerKLineCacheDuration, func(loadCtx context.Context) (explorerKLineResponse, int64, error) {
		                                                                      
		boundedCtx, cancel := context.WithTimeout(context.Background(), s.timeout)
		defer cancel()
		_ = loadCtx
		tradeLimit := explorerKLineTradeBudget(cacheCount)
		var (
			trades []TradeRecord
			err    error
		)
		if market == explorerKLineMarketFuture {
			trades, err = s.fetchFutureTradesFromNode(boundedCtx, node, pair, tradeLimit)
		} else {
			trades, err = s.fetchTradesFromNode(boundedCtx, node, pair, tradeLimit)
		}
		if err != nil {
			return explorerKLineResponse{}, 0, err
		}
		resp := buildExplorerKLines(market, pair, trades, cacheCount, buckets)
		encoded, err := json.Marshal(resp)
		if err != nil {
			return explorerKLineResponse{}, 0, err
		}
		return resp, int64(len(encoded)), nil
	})
	if err != nil {
		return explorerKLineResponse{}, err
	}
	return sliceExplorerKLines(base, count), nil
}

                                                                            
                                                                               
                                                                             
func explorerKLineCacheCount(count int) int {
	if count <= 0 {
		count = explorerKLineDefaultCount
	}
	if count >= explorerKLineMaxCount {
		return explorerKLineMaxCount
	}
	tier := 1
	for tier < count {
		tier <<= 1
	}
	if tier > explorerKLineMaxCount {
		return explorerKLineMaxCount
	}
	return tier
}

func explorerKLineTradeBudget(count int) int {
	if count <= 0 {
		count = explorerKLineDefaultCount
	}
	budget := count * 10
	if budget < explorerKLineMinTradeLimit {
		budget = explorerKLineMinTradeLimit
	}
	if budget > explorerKLineMaxTradeLimit {
		return explorerKLineMaxTradeLimit
	}
	return budget
}

func sliceExplorerKLines(base explorerKLineResponse, count int) explorerKLineResponse {
	if count <= 0 {
		count = explorerKLineDefaultCount
	}
	out := explorerKLineResponse{
		Market:  base.Market,
		Pair:    base.Pair,
		Buckets: make(map[string][]explorerKLineCandle, len(base.Buckets)),
	}
	for bucket, candles := range base.Buckets {
		if len(candles) > count {
			candles = candles[len(candles)-count:]
		}
		out.Buckets[bucket] = candles
	}
	return out
}

func buildExplorerKLines(market, pair string, trades []TradeRecord, count int, buckets []uint64) explorerKLineResponse {
	sort.SliceStable(trades, func(i, j int) bool {
		if trades[i].Height != trades[j].Height {
			return trades[i].Height < trades[j].Height
		}
		if trades[i].Time != trades[j].Time {
			return trades[i].Time < trades[j].Time
		}
		return trades[i].ID < trades[j].ID
	})

	resp := explorerKLineResponse{Market: market, Pair: pair, Buckets: make(map[string][]explorerKLineCandle, len(buckets))}
	for _, bucket := range buckets {
		byStart := make(map[uint64]*explorerKLineCandle)
		starts := make([]uint64, 0)
		for _, tr := range trades {
			if tr.Height == 0 || tr.Price == "" || tr.Amount == "" {
				continue
			}
			start := explorerKLineBucketStart(tr.Height, bucket)
			c, exists := byStart[start]
			if !exists {
				c = &explorerKLineCandle{
					StartHeight:     start,
					EndHeight:       start + bucket - 1,
					LastTradeHeight: tr.Height,
					Open:            tr.Price,
					High:            tr.Price,
					Low:             tr.Price,
					Close:           tr.Price,
					Volume:          "0",
				}
				byStart[start] = c
				starts = append(starts, start)
			}
			c.LastTradeHeight = tr.Height
			c.Close = tr.Price
			if compareDecimalStrings(tr.Price, c.High) > 0 {
				c.High = tr.Price
			}
			if compareDecimalStrings(tr.Price, c.Low) < 0 {
				c.Low = tr.Price
			}
			c.Volume = addIntegerStrings(c.Volume, tr.Amount)
			c.Trades++
			if market == explorerKLineMarketFuture && strings.HasPrefix(tr.ID, "liq_") {
				c.Liquidations++
			}
		}
		sort.Slice(starts, func(i, j int) bool { return starts[i] < starts[j] })
		if count > 0 && len(starts) > count {
			starts = starts[len(starts)-count:]
		}
		candles := make([]explorerKLineCandle, 0, len(starts))
		for _, start := range starts {
			candles = append(candles, *byStart[start])
		}
		resp.Buckets[strconv.FormatUint(bucket, 10)] = candles
	}
	return resp
}

func (s *server) fetchFutureTradesFromNode(ctx context.Context, node, pair string, limit int) ([]TradeRecord, error) {
	node = s.pickNode(node)
	if node == "" {
		return nil, fmt.Errorf("node is not allowed")
	}
	reqURL := fmt.Sprintf("https://%s/future/trades?pair=%s&limit=%d", node, url.QueryEscape(pair), limit)
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.nodeDo(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("node returned status %d", resp.StatusCode)
	}
	body, err := readResponseBody(resp)
	if err != nil {
		return nil, err
	}
	var trades []TradeRecord
	if err := json.Unmarshal(body, &trades); err != nil {
		return nil, err
	}
	if limit > 0 && len(trades) > limit {
		trades = trades[:limit]
	}
	return trades, nil
}

func parseExplorerKLineCount(raw string) int {
	count := explorerKLineDefaultCount
	if raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			count = n
		}
	}
	if count > explorerKLineMaxCount {
		return explorerKLineMaxCount
	}
	return count
}

func parseExplorerKLineBuckets(raw string) []uint64 {
	if strings.TrimSpace(raw) == "" {
		return append([]uint64(nil), explorerKLineAllowedBuckets...)
	}
	seen := make(map[uint64]struct{})
	out := make([]uint64, 0, len(explorerKLineAllowedBuckets))
	for _, part := range strings.Split(raw, ",") {
		n, err := strconv.ParseUint(strings.TrimSpace(part), 10, 64)
		if err != nil || !explorerKLineBucketAllowed(n) {
			continue
		}
		if _, exists := seen[n]; exists {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	if len(out) == 0 {
		return append([]uint64(nil), explorerKLineAllowedBuckets...)
	}
	return out
}

func explorerKLineBucketAllowed(bucket uint64) bool {
	for _, allowed := range explorerKLineAllowedBuckets {
		if bucket == allowed {
			return true
		}
	}
	return false
}

func explorerKLineBucketStart(height, bucket uint64) uint64 {
	if height == 0 || bucket == 0 {
		return 0
	}
	return ((height - 1) / bucket * bucket) + 1
}

func explorerKLineBucketKey(buckets []uint64) string {
	cp := append([]uint64(nil), buckets...)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	parts := make([]string, 0, len(cp))
	for _, bucket := range cp {
		parts = append(parts, strconv.FormatUint(bucket, 10))
	}
	return strings.Join(parts, ",")
}

func compareDecimalStrings(a, b string) int {
	ai, aok := new(big.Int).SetString(a, 10)
	bi, bok := new(big.Int).SetString(b, 10)
	if aok && bok {
		return ai.Cmp(bi)
	}
	return strings.Compare(a, b)
}

func addIntegerStrings(a, b string) string {
	ai, aok := new(big.Int).SetString(a, 10)
	if !aok {
		ai = new(big.Int)
	}
	bi, bok := new(big.Int).SetString(b, 10)
	if !bok {
		return ai.String()
	}
	return ai.Add(ai, bi).String()
}
