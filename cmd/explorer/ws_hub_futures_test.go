package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"dex/cmd/explorer/indexdb"
	"dex/future"
	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

func TestFetchFuturesDataExposesUpstreamFailures(t *testing.T) {
	hub := &wsHub{srv: &server{allowedNodes: map[string]struct{}{}}}
	params, err := json.Marshal(futuresSubParams{Node: "blocked.example:443", Pair: "BTC_ARB.USDT"})
	if err != nil {
		t.Fatal(err)
	}

	raw := hub.fetchFuturesData(context.Background(), params)
	if len(raw) == 0 {
		t.Fatal("expected a futures payload with explicit upstream errors")
	}

	var payload map[string]map[string]string
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	for _, source := range []string{"market", "orderbook", "trades"} {
		message := payload[source]["error"]
		if !strings.Contains(message, source) || !strings.Contains(message, "upstream request failed") {
			t.Fatalf("%s error = %q, want an explicit source-specific upstream error", source, message)
		}
	}
}

func TestFetchFuturesDataRejectsInvalidUpstreamJSON(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/future/market", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not-json"))
	})
	mux.HandleFunc("/future/orderbook", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"bids":[],"asks":[]}`))
	})
	mux.HandleFunc("/future/trades", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	})
	node := httptest.NewTLSServer(mux)
	t.Cleanup(node.Close)
	address := strings.TrimPrefix(node.URL, "https://")
	hub := &wsHub{srv: &server{
		client:       node.Client(),
		allowedNodes: map[string]struct{}{address: {}},
	}}
	params, err := json.Marshal(futuresSubParams{Node: address, Pair: "BTC_ARB.USDT"})
	if err != nil {
		t.Fatal(err)
	}

	raw := hub.fetchFuturesData(context.Background(), params)
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var marketError map[string]string
	if err := json.Unmarshal(payload["market"], &marketError); err != nil {
		t.Fatalf("decode market error: %v", err)
	}
	if !strings.Contains(marketError["error"], "market upstream request failed") {
		t.Fatalf("market error = %q, want invalid JSON exposed as an upstream failure", marketError["error"])
	}
	if string(payload["orderbook"]) != `{"bids":[],"asks":[]}` {
		t.Fatalf("orderbook payload = %s, want valid payload preserved", payload["orderbook"])
	}
	if string(payload["trades"]) != `[]` {
		t.Fatalf("trades payload = %s, want valid payload preserved", payload["trades"])
	}
}

func TestFetchClosedPositionsUsesLocalReadModelCursorAndRequestID(t *testing.T) {
	idb, err := indexdb.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idb.Close() })
	for key, value := range map[string]string{
		indexdb.KeyReadModelSchemaVersion: strconv.FormatUint(uint64(pb.BlockReadModelSchemaVersion), 10),
		indexdb.KeyReadModelBaseHeight:    "1",
		indexdb.KeyReadModelTipHeight:     "12",
	} {
		if err := idb.Set(key, value); err != nil {
			t.Fatal(err)
		}
	}
	for _, state := range []*pb.FuturePositionState{
		{PositionId: "newer", Owner: "alice", Pair: "FB_ETH.USDT", Side: int32(future.Long), Status: int32(future.StatusClosed), CloseHeight: 12},
		{PositionId: "older", Owner: "alice", Pair: "FB_ETH.USDT", Side: int32(future.Long), Status: int32(future.StatusClosed), CloseHeight: 11},
	} {
		raw, err := proto.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
		if err := idb.Set(keys.KeyFuturePositionHistory(state.PositionId), string(raw)); err != nil {
			t.Fatal(err)
		}
		if err := idb.Set(keys.KeyFuturePositionHistoryOwnerIndex(state.Owner, state.Status, state.CloseHeight, state.PositionId), state.PositionId); err != nil {
			t.Fatal(err)
		}
	}

	hub := &wsHub{srv: &server{indexDB: idb, allowedNodes: map[string]struct{}{}}}
	firstParams := json.RawMessage(`{"address":"alice","status":"closed","limit":"1","request_id":"page-1","node":"unreachable.invalid"}`)
	first := hub.fetchTopicData("my_closed_positions", firstParams)
	var firstPage struct {
		RequestID  string                        `json:"request_id"`
		Positions  []explorerFuturePositionEntry `json:"positions"`
		HasMore    bool                          `json:"has_more"`
		NextCursor string                        `json:"next_cursor"`
	}
	if err := json.Unmarshal(first.data, &firstPage); err != nil {
		t.Fatalf("decode first WebSocket page: %v; payload=%s", err, first.data)
	}
	if firstPage.RequestID != "page-1" || len(firstPage.Positions) != 1 || firstPage.Positions[0].PositionID != "newer" || !firstPage.HasMore || firstPage.NextCursor == "" {
		t.Fatalf("unexpected first WebSocket page: %+v", firstPage)
	}

	secondParams, err := json.Marshal(map[string]string{
		"owner":      "alice",
		"status":     "closed",
		"cursor":     firstPage.NextCursor,
		"limit":      "1",
		"request_id": "page-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	second := hub.fetchTopicData("my_closed_positions", secondParams)
	var secondPage struct {
		RequestID string                        `json:"request_id"`
		Positions []explorerFuturePositionEntry `json:"positions"`
		HasMore   bool                          `json:"has_more"`
	}
	if err := json.Unmarshal(second.data, &secondPage); err != nil {
		t.Fatalf("decode second WebSocket page: %v; payload=%s", err, second.data)
	}
	if secondPage.RequestID != "page-2" || len(secondPage.Positions) != 1 || secondPage.Positions[0].PositionID != "older" || secondPage.HasMore {
		t.Fatalf("unexpected second WebSocket page: %+v", secondPage)
	}
	if !isAllowedWSTopic("my_closed_positions") {
		t.Fatal("closed-position WebSocket topic is not admitted")
	}

	bounded := hub.fetchTopicData("my_closed_positions", []byte(`{"address":"alice","limit":"500","request_id":"bounded"}`))
	var boundedPage struct {
		Limit int `json:"limit"`
	}
	if err := json.Unmarshal(bounded.data, &boundedPage); err != nil {
		t.Fatalf("decode bounded WebSocket page: %v; payload=%s", err, bounded.data)
	}
	if boundedPage.Limit != wsClosedPositionsPageLimit {
		t.Fatalf("WebSocket page limit = %d, want %d", boundedPage.Limit, wsClosedPositionsPageLimit)
	}
}

func TestFetchClosedPositionsPushesReadModelErrorsWithRequestID(t *testing.T) {
	idb, err := indexdb.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idb.Close() })

	hub := &wsHub{srv: &server{indexDB: idb}}
	payload := hub.fetchTopicData("my_closed_positions", []byte(`{"address":"alice","request_id":"initial"}`))
	var response struct {
		RequestID string `json:"request_id"`
		Code      string `json:"code"`
		Error     string `json:"error"`
	}
	if err := json.Unmarshal(payload.data, &response); err != nil {
		t.Fatalf("decode WebSocket error: %v; payload=%s", err, payload.data)
	}
	if response.RequestID != "initial" || response.Code != "READ_MODEL_UNAVAILABLE" || response.Error == "" {
		t.Fatalf("unexpected WebSocket error payload: %+v", response)
	}

	missingOwner := hub.fetchTopicData("my_closed_positions", []byte(`{"request_id":"missing-owner"}`))
	if err := json.Unmarshal(missingOwner.data, &response); err != nil {
		t.Fatalf("decode missing-owner error: %v; payload=%s", err, missingOwner.data)
	}
	if response.RequestID != "missing-owner" || response.Code != "INVALID_PARAMS" || response.Error == "" {
		t.Fatalf("unexpected missing-owner payload: %+v", response)
	}
}
