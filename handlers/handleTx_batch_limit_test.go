package handlers

import (
	"bytes"
	"dex/config"
	"dex/pb"
	"dex/stats"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
)

func TestValidateTxBatchTransactionCountBoundariesAndUnvalidatedFallback(t *testing.T) {
	if err := validateTxBatchTransactionCount(config.DefaultMaxTxBatchTransactions, nil); err != nil {
		t.Fatalf("default boundary must be allowed: %v", err)
	}
	if err := validateTxBatchTransactionCount(config.DefaultMaxTxBatchTransactions+1, nil); err == nil {
		t.Fatal("default boundary + 1 must be rejected")
	}

	for _, configured := range []int{0, -1} {
		cfg := config.DefaultConfig()
		cfg.Server.MaxTxBatchTransactions = configured
		if err := validateTxBatchTransactionCount(config.DefaultMaxTxBatchTransactions, cfg); err != nil {
			t.Fatalf("configured limit %d must safely fall back to the default: %v", configured, err)
		}
		if err := validateTxBatchTransactionCount(config.DefaultMaxTxBatchTransactions+1, cfg); err == nil {
			t.Fatalf("configured limit %d must reject above the fallback default", configured)
		}
	}
}

func TestHandleTxBatchRejectsTooManyTransactionsBeforeSubmit(t *testing.T) {
	cfg := config.DefaultConfig()
	hm := &HandlerManager{
		cfg:   cfg,
		Stats: stats.NewStats(),
		                                                                       
		                                                                      
		txPool: nil,
	}
	batch := &pb.BatchGetShortTxResponse{
		Transactions: make([]*pb.AnyTx, config.DefaultMaxTxBatchTransactions+1),
	}
	for i := range batch.Transactions {
		batch.Transactions[i] = &pb.AnyTx{}
	}
	body, err := proto.Marshal(batch)
	if err != nil {
		t.Fatalf("marshal tx batch: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/txbatch", bytes.NewReader(body))
	req.RemoteAddr = "203.0.113.10:1234"
	recorder := httptest.NewRecorder()
	hm.HandleTxBatch(recorder, req)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d; body=%q", recorder.Code, http.StatusRequestEntityTooLarge, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "5001") || !strings.Contains(recorder.Body.String(), "5000") {
		t.Fatalf("limit response must include count and limit, got %q", recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "Failed to submit tx batch") {
		t.Fatalf("oversized batch reached SubmitTx: %q", recorder.Body.String())
	}
}

func TestHandleTxBatchHonorsConfiguredLowerLimitBeforeSubmit(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.MaxTxBatchTransactions = 1
	hm := &HandlerManager{cfg: cfg, Stats: stats.NewStats()}
	batch := &pb.BatchGetShortTxResponse{Transactions: []*pb.AnyTx{{}, {}}}
	body, err := proto.Marshal(batch)
	if err != nil {
		t.Fatalf("marshal tx batch: %v", err)
	}

	recorder := httptest.NewRecorder()
	hm.HandleTxBatch(recorder, httptest.NewRequest(http.MethodPost, "/txbatch", bytes.NewReader(body)))

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d; body=%q", recorder.Code, http.StatusRequestEntityTooLarge, recorder.Body.String())
	}
}
