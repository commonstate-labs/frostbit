package main

import (
	"bytes"
	"context"
	"dex/cmd/explorer/indexdb"
	"dex/pb"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
)

func TestHandleRecentTxsFallsBackWhenIndexIsEmpty(t *testing.T) {
	idb, err := indexdb.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idb.Close() })

	var blockCalls atomic.Int32
	node, client := newRecentTxTestNode(t, 3, func(height uint64) *pb.Block {
		switch height {
		case 3:
			return &pb.Block{Header: &pb.BlockHeader{Height: height}, Body: []*pb.AnyTx{
				recentTxTestTransfer("transfer-3"),
				recentTxTestOrder("order-3"),
			}}
		case 2:
			return &pb.Block{Header: &pb.BlockHeader{Height: height}, Body: []*pb.AnyTx{recentTxTestWitnessVote("vote-2")}}
		default:
			return nil
		}
	}, &blockCalls)
	s := recentTxTestServer(node, client, idb)

	req := httptest.NewRequest(http.MethodPost, "/api/recenttxs", strings.NewReader(`{"node":"`+node+`","tx_type":"all","count":3}`))
	rec := httptest.NewRecorder()
	s.handleRecentTxs(rec, req)

	var got recentTxsResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Error != "" {
		t.Fatalf("unexpected error: %s", got.Error)
	}
	wantIDs := []string{"order-3", "transfer-3", "vote-2"}
	if len(got.Txs) != len(wantIDs) {
		t.Fatalf("got %d txs, want %d: %s", len(got.Txs), len(wantIDs), rec.Body.String())
	}
	for i, want := range wantIDs {
		if got.Txs[i].TxID != want {
			t.Errorf("tx[%d] id = %q, want %q", i, got.Txs[i].TxID, want)
		}
	}
	if got := blockCalls.Load(); got != 2 {
		t.Fatalf("block fetches = %d, want 2", got)
	}
}

func TestHandleRecentTxsFallsBackWhenIndexIsUnavailable(t *testing.T) {
	node, client := newRecentTxTestNode(t, 1, func(height uint64) *pb.Block {
		return &pb.Block{Header: &pb.BlockHeader{Height: height}, Body: []*pb.AnyTx{recentTxTestTransfer("from-node")}}
	}, nil)
	s := recentTxTestServer(node, client, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/recenttxs", strings.NewReader(`{"node":"`+node+`","tx_type":"all","count":1}`))
	rec := httptest.NewRecorder()
	s.handleRecentTxs(rec, req)

	var got recentTxsResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Error != "" || len(got.Txs) != 1 || got.Txs[0].TxID != "from-node" {
		t.Fatalf("unexpected response: %s", rec.Body.String())
	}
}

func TestHandleRecentTxsUsesNodeWhenIndexIsStale(t *testing.T) {
	idb, err := indexdb.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idb.Close() })
	if err := idb.IndexBlock(&pb.Block{Header: &pb.BlockHeader{Height: 1}, Body: []*pb.AnyTx{recentTxTestTransfer("indexed-old")}}); err != nil {
		t.Fatal(err)
	}

	var blockCalls atomic.Int32
	node, client := newRecentTxTestNode(t, 3, func(height uint64) *pb.Block {
		return &pb.Block{Header: &pb.BlockHeader{Height: height}, Body: []*pb.AnyTx{recentTxTestTransfer("node-new")}}
	}, &blockCalls)
	s := recentTxTestServer(node, client, idb)
	req := httptest.NewRequest(http.MethodPost, "/api/recenttxs", strings.NewReader(`{"node":"`+node+`","tx_type":"all","count":1}`))
	rec := httptest.NewRecorder()
	s.handleRecentTxs(rec, req)

	var got recentTxsResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Error != "" || len(got.Txs) != 1 || got.Txs[0].TxID != "node-new" {
		t.Fatalf("unexpected response: %s", rec.Body.String())
	}
	if got := blockCalls.Load(); got != 1 {
		t.Fatalf("block fetches = %d, want 1", got)
	}
}

func TestHandleRecentTxsFallbackFiltersType(t *testing.T) {
	idb, err := indexdb.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idb.Close() })

	var blockCalls atomic.Int32
	node, client := newRecentTxTestNode(t, 3, func(height uint64) *pb.Block {
		if height == 3 {
			return &pb.Block{Header: &pb.BlockHeader{Height: height}, Body: []*pb.AnyTx{recentTxTestTransfer("transfer-3")}}
		}
		if height == 2 {
			return &pb.Block{Header: &pb.BlockHeader{Height: height}, Body: []*pb.AnyTx{recentTxTestWitnessVote("vote-2")}}
		}
		return nil
	}, &blockCalls)
	s := recentTxTestServer(node, client, idb)

	req := httptest.NewRequest(http.MethodPost, "/api/recenttxs", strings.NewReader(`{"node":"`+node+`","tx_type":"WitnessVote","count":1}`))
	rec := httptest.NewRecorder()
	s.handleRecentTxs(rec, req)

	var got recentTxsResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Error != "" || len(got.Txs) != 1 || got.Txs[0].TxID != "vote-2" {
		t.Fatalf("unexpected response: %s", rec.Body.String())
	}
	if got := blockCalls.Load(); got != 2 {
		t.Fatalf("block fetches = %d, want 2", got)
	}
}

func TestRecentTxRecordsFromNodeCapsRareTypeScan(t *testing.T) {
	var blockCalls atomic.Int32
	node, client := newRecentTxTestNode(t, 100, func(height uint64) *pb.Block {
		return &pb.Block{Header: &pb.BlockHeader{Height: height}, Body: []*pb.AnyTx{recentTxTestTransfer("transfer")}}
	}, &blockCalls)
	s := recentTxTestServer(node, client, nil)

	recs, err := s.recentTxRecordsFromNode(context.Background(), node, "WitnessVote", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 0 {
		t.Fatalf("got %d records, want none", len(recs))
	}
	wantCalls := int32(recentTxFallbackBlockLimit(1))
	if got := blockCalls.Load(); got != wantCalls {
		t.Fatalf("block fetches = %d, want capped %d", got, wantCalls)
	}
}

func TestHandleRecentTxsDoesNotSkipFailedTip(t *testing.T) {
	idb, err := indexdb.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idb.Close() })

	var blockCalls atomic.Int32
	node, client := newRecentTxTestNode(t, 3, func(height uint64) *pb.Block {
		if height == 2 {
			return &pb.Block{Header: &pb.BlockHeader{Height: height}, Body: []*pb.AnyTx{recentTxTestTransfer("older")}}
		}
		return nil
	}, &blockCalls)
	s := recentTxTestServer(node, client, idb)
	req := httptest.NewRequest(http.MethodPost, "/api/recenttxs", strings.NewReader(`{"node":"`+node+`","tx_type":"all","count":1}`))
	rec := httptest.NewRecorder()
	s.handleRecentTxs(rec, req)

	var got recentTxsResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Error, "fetch block 3") || len(got.Txs) != 0 {
		t.Fatalf("unexpected response: %s", rec.Body.String())
	}
	if got := blockCalls.Load(); got != 1 {
		t.Fatalf("block fetches = %d, want 1", got)
	}
}

func TestHandleRecentTxsTimeoutDiscardsPartialNodeResults(t *testing.T) {
	idb, err := indexdb.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idb.Close() })

	node, client := newRecentTxTestNodeWithContext(t, 3, func(ctx context.Context, height uint64) *pb.Block {
		if height == 3 {
			return &pb.Block{Header: &pb.BlockHeader{Height: height}, Body: []*pb.AnyTx{recentTxTestTransfer("partial")}}
		}
		<-ctx.Done()
		return nil
	}, nil)
	s := recentTxTestServer(node, client, idb)
	s.timeout = 100 * time.Millisecond
	req := httptest.NewRequest(http.MethodPost, "/api/recenttxs", strings.NewReader(`{"node":"`+node+`","tx_type":"all","count":2}`))
	rec := httptest.NewRecorder()
	s.handleRecentTxs(rec, req)

	var got recentTxsResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Error, "context deadline exceeded") || len(got.Txs) != 0 {
		t.Fatalf("unexpected response: %s", rec.Body.String())
	}
}

func TestHandleRecentTxsRejectsInvalidBlockHeader(t *testing.T) {
	tests := []struct {
		name  string
		block *pb.Block
		want  string
	}{
		{name: "missing header", block: &pb.Block{}, want: "empty header"},
		{name: "wrong height", block: &pb.Block{Header: &pb.BlockHeader{Height: 2}}, want: "source returned height 2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idb, err := indexdb.New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = idb.Close() })
			node, client := newRecentTxTestNode(t, 3, func(uint64) *pb.Block { return tt.block }, nil)
			s := recentTxTestServer(node, client, idb)
			req := httptest.NewRequest(http.MethodPost, "/api/recenttxs", strings.NewReader(`{"node":"`+node+`","tx_type":"all","count":1}`))
			rec := httptest.NewRecorder()
			s.handleRecentTxs(rec, req)

			var got recentTxsResponse
			if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(got.Error, tt.want) || len(got.Txs) != 0 {
				t.Fatalf("unexpected response: %s", rec.Body.String())
			}
		})
	}
}

func TestHandleRecentTxsRejectsUnlistedFallbackNode(t *testing.T) {
	idb, err := indexdb.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idb.Close() })

	var calls atomic.Int32
	s := &server{
		client: &http.Client{Transport: recentTxRoundTripFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return nil, errors.New("unexpected network request")
		})},
		timeout:      time.Second,
		defaultNodes: []string{"trusted:6000"},
		allowedNodes: map[string]struct{}{"trusted:6000": {}},
		indexDB:      idb,
	}
	req := httptest.NewRequest(http.MethodPost, "/api/recenttxs", strings.NewReader(`{"node":"attacker:6000","tx_type":"all","count":1}`))
	rec := httptest.NewRecorder()
	s.handleRecentTxs(rec, req)

	var got recentTxsResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Error, "not allowed") {
		t.Fatalf("error = %q, want node rejection", got.Error)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("network calls = %d, want 0", got)
	}
}

func TestHandleRecentTxsUsesIndexWithoutNodeFallback(t *testing.T) {
	idb, err := indexdb.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idb.Close() })
	if err := idb.IndexBlock(&pb.Block{Header: &pb.BlockHeader{Height: 3}, Body: []*pb.AnyTx{recentTxTestTransfer("indexed")}}); err != nil {
		t.Fatal(err)
	}

	var blockCalls atomic.Int32
	node, client := newRecentTxTestNode(t, 3, func(height uint64) *pb.Block {
		return &pb.Block{Header: &pb.BlockHeader{Height: height}, Body: []*pb.AnyTx{recentTxTestTransfer("from-node")}}
	}, &blockCalls)
	s := recentTxTestServer(node, client, idb)
	req := httptest.NewRequest(http.MethodPost, "/api/recenttxs", strings.NewReader(`{"node":"`+node+`","tx_type":"all","count":1}`))
	rec := httptest.NewRecorder()
	s.handleRecentTxs(rec, req)

	var got recentTxsResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Error != "" || len(got.Txs) != 1 || got.Txs[0].TxID != "indexed" {
		t.Fatalf("unexpected response: %s", rec.Body.String())
	}
	if got := blockCalls.Load(); got != 0 {
		t.Fatalf("block fetches = %d, want 0", got)
	}
}

func TestHandleRecentTxsTrustsEmptyResultFromFreshIndex(t *testing.T) {
	idb, err := indexdb.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idb.Close() })
	if err := idb.IndexBlock(&pb.Block{Header: &pb.BlockHeader{Height: 3}, Body: []*pb.AnyTx{recentTxTestTransfer("indexed")}}); err != nil {
		t.Fatal(err)
	}

	var blockCalls atomic.Int32
	node, client := newRecentTxTestNode(t, 3, func(height uint64) *pb.Block {
		return &pb.Block{Header: &pb.BlockHeader{Height: height}, Body: []*pb.AnyTx{recentTxTestWitnessVote("from-node")}}
	}, &blockCalls)
	s := recentTxTestServer(node, client, idb)
	req := httptest.NewRequest(http.MethodPost, "/api/recenttxs", strings.NewReader(`{"node":"`+node+`","tx_type":"WitnessVote","count":1}`))
	rec := httptest.NewRecorder()
	s.handleRecentTxs(rec, req)

	var got recentTxsResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Error != "" || len(got.Txs) != 0 {
		t.Fatalf("unexpected response: %s", rec.Body.String())
	}
	if got := blockCalls.Load(); got != 0 {
		t.Fatalf("block fetches = %d, want 0", got)
	}
}

func TestHandleRecentTxsUsesIndexWhenTipIsUnavailable(t *testing.T) {
	idb, err := indexdb.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idb.Close() })
	if err := idb.IndexBlock(&pb.Block{Header: &pb.BlockHeader{Height: 1}, Body: []*pb.AnyTx{recentTxTestTransfer("indexed")}}); err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int32
	node := "trusted:6000"
	s := &server{
		client: &http.Client{Transport: recentTxRoundTripFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return nil, errors.New("node offline")
		})},
		timeout:      time.Second,
		defaultNodes: []string{node},
		allowedNodes: map[string]struct{}{node: {}},
		indexDB:      idb,
	}
	req := httptest.NewRequest(http.MethodPost, "/api/recenttxs", strings.NewReader(`{"node":"`+node+`","tx_type":"all","count":1}`))
	rec := httptest.NewRecorder()
	s.handleRecentTxs(rec, req)

	var got recentTxsResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Error != "" || len(got.Txs) != 1 || got.Txs[0].TxID != "indexed" {
		t.Fatalf("unexpected response: %s", rec.Body.String())
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("network calls = %d, want 1", got)
	}
}

type recentTxRoundTripFunc func(*http.Request) (*http.Response, error)

func (f recentTxRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func recentTxTestServer(node string, client *http.Client, idb *indexdb.IndexDB) *server {
	return &server{
		client:       client,
		timeout:      time.Second,
		defaultNodes: []string{node},
		allowedNodes: map[string]struct{}{node: {}},
		indexDB:      idb,
	}
}

func newRecentTxTestNode(t *testing.T, latest uint64, blockAt func(uint64) *pb.Block, blockCalls *atomic.Int32) (string, *http.Client) {
	t.Helper()
	return newRecentTxTestNodeWithContext(t, latest, func(_ context.Context, height uint64) *pb.Block { return blockAt(height) }, blockCalls)
}

func newRecentTxTestNodeWithContext(t *testing.T, latest uint64, blockAt func(context.Context, uint64) *pb.Block, blockCalls *atomic.Int32) (string, *http.Client) {
	t.Helper()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/heightquery":
			writeRecentTxTestProto(w, http.StatusOK, &pb.HeightResponse{LastAcceptedHeight: latest})
		case "/getblock":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			var req pb.GetBlockRequest
			if err := proto.Unmarshal(body, &req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if blockCalls != nil {
				blockCalls.Add(1)
			}
			block := blockAt(r.Context(), req.Height)
			if block == nil {
				writeRecentTxTestProto(w, http.StatusNotFound, &pb.GetBlockResponse{Error: "not found"})
				return
			}
			writeRecentTxTestProto(w, http.StatusOK, &pb.GetBlockResponse{Block: block})
		default:
			http.NotFound(w, r)
		}
	})
	ts := httptest.NewTLSServer(h)
	t.Cleanup(ts.Close)
	return strings.TrimPrefix(ts.URL, "https://"), ts.Client()
}

func writeRecentTxTestProto(w http.ResponseWriter, status int, msg proto.Message) {
	data, err := proto.Marshal(msg)
	if err != nil {
		panic(err)
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(status)
	_, _ = io.Copy(w, bytes.NewReader(data))
}

func recentTxTestTransfer(txID string) *pb.AnyTx {
	return &pb.AnyTx{Content: &pb.AnyTx_Transaction{Transaction: &pb.Transaction{
		Base: &pb.BaseMessage{TxId: txID, FromAddress: "alice", Status: pb.Status_SUCCEED},
		To:   "bob",
	}}}
}

func recentTxTestOrder(txID string) *pb.AnyTx {
	return &pb.AnyTx{Content: &pb.AnyTx_OrderTx{OrderTx: &pb.OrderTx{
		Base: &pb.BaseMessage{TxId: txID, FromAddress: "alice", Status: pb.Status_SUCCEED},
	}}}
}

func recentTxTestWitnessVote(txID string) *pb.AnyTx {
	return &pb.AnyTx{Content: &pb.AnyTx_WitnessVoteTx{WitnessVoteTx: &pb.WitnessVoteTx{
		Base: &pb.BaseMessage{TxId: txID, FromAddress: "witness", Status: pb.Status_SUCCEED},
	}}}
}
