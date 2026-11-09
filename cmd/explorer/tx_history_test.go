package main

import (
	"dex/cmd/explorer/indexdb"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleTxHistoryAcceptsCompleteBridgeTypeFilter(t *testing.T) {
	idb, err := indexdb.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idb.Close() })

	req := httptest.NewRequest(http.MethodGet,
		"/api/txhistory?address=fbc1test&types=WitnessRequest,FrostWithdrawRequest,FrostWithdrawCancel,FrostWithdrawClaim,FrostWithdrawSigned",
		nil,
	)
	rec := httptest.NewRecorder()
	(&server{indexDB: idb}).handleTxHistory(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("complete bridge type filter status=%d body=%q, want 200", rec.Code, rec.Body.String())
	}
}

func TestHandleTxHistoryStillRejectsExcessiveTypeFilter(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/txhistory?address=fbc1test&types=a,b,c,d,e,f,g,h,i",
		nil,
	)
	rec := httptest.NewRecorder()
	(&server{}).handleTxHistory(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("excessive type filter status=%d body=%q, want 400", rec.Code, rec.Body.String())
	}
}
