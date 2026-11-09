package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestV2ReadonlyHandlersRejectMalformedScopeBeforeDatabase(t *testing.T) {
	hm := &HandlerManager{}
	for _, tc := range []struct {
		path    string
		handler http.HandlerFunc
	}{
		{"/frost/vault/handover/page?chain=eth&vault_id=0&from_epoch=01&to_epoch=2", hm.HandleFrostHandoverPage},
		{"/frost/vault/handover/page?chain=eth&vault_id=4294967296&from_epoch=1&to_epoch=2", hm.HandleFrostHandoverPage},
		{"/frost/vault/handover/page?chain=eth&vault_id=0&from_epoch=1&to_epoch=18446744073709551616", hm.HandleFrostHandoverPage},
		{"/frost/withdraw/batch?chain=eth&vault_id=-1&batch_seq=1", hm.HandleFrostWithdrawBatch},
	} {
		r := httptest.NewRecorder()
		tc.handler(r, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if r.Code != 400 {
			t.Fatalf("%s status=%d", tc.path, r.Code)
		}
		r = httptest.NewRecorder()
		tc.handler(r, httptest.NewRequest(http.MethodPost, tc.path, nil))
		if r.Code != 405 || r.Header().Get("Allow") != "GET" {
			t.Fatalf("POST %s status=%d", tc.path, r.Code)
		}
	}
}
