package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dex/frost/withdrawresult"
)

func TestWithdrawResultGatewayPreservesIdentityAndArchiveErrors(t *testing.T) {
	status := http.StatusOK
	response := `{"version":1,"status":"SIGNED","withdraw_id":"w","request_tx_id":"request","chain":"ETH","calldata":"0x1234"}`
	calls := 0
	s := newFrostWithdrawV2TestServer([]string{"node.example:443"}, func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "node.example:443" || r.URL.Path != "/frost/withdraw/result" || r.URL.Query().Get("request_tx_id") != "request" {
			t.Fatalf("wrong outbound query: %s", r.URL)
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(response)), Header: http.Header{}, Request: r}, nil
	})
	query := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.handleFrostWithdrawResult(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}
	for _, path := range []string{"/api/frost/withdraw/result?node=http://evil.example&request_tx_id=request", "/api/frost/withdraw/result?withdraw_id=../private", "/api/frost/withdraw/result"} {
		if w := query(path); w.Code != 400 {
			t.Fatalf("invalid input=%d %s", w.Code, w.Body)
		}
	}
	if calls != 0 {
		t.Fatal("invalid query reached node")
	}
	path := "/api/frost/withdraw/result?node=node-0&request_tx_id=request"
	w := query(path)
	var result withdrawresult.Result
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.WithdrawID != "w" || result.Calldata != "0x1234" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("result=%d %s", w.Code, w.Body)
	}
	response = `{"version":1,"status":"CANCELLED","withdraw_id":"w","request_tx_id":"request","requester":"fbc-alice","original_request_tx":"0x0a04deadbeef","chain":"ETH"}`
	w = query(path)
	var cancelled withdrawresult.Result
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &cancelled) != nil || cancelled.Status != "CANCELLED" || cancelled.Requester != "fbc-alice" || cancelled.OriginalRequestTx != "0x0a04deadbeef" {
		t.Fatalf("cancelled result=%d %s", w.Code, w.Body)
	}
	response = `{"version":1,"status":"SIGNED","withdraw_id":"w","request_tx_id":"request","chain":"ETH","calldata":"0x1234"}`
	response = strings.ReplaceAll(response, `"request_tx_id":"request"`, `"request_tx_id":"other"`)
	if w := query(path); w.Code != 502 {
		t.Fatalf("wrong identity=%d %s", w.Code, w.Body)
	}
	for _, upstream := range []int{404, 409, 410, 413} {
		status, response = upstream, `{"error":"signed transaction bytes pruned; use an archive node"}`
		if w := query(path); w.Code != upstream || !strings.Contains(w.Body.String(), "archive node") {
			t.Fatalf("upstream status=%d body=%s", w.Code, w.Body)
		}
	}
}

func TestExplorerAccountCalldataIncludesTRON(t *testing.T) {
	for _, name := range []string{"ETH", "BNB", "BASE", "ARB", "TRX", "tron"} {
		if !isExplorerAccountChain(name) {
			t.Fatalf("account calldata missing for %s", name)
		}
	}
	if isExplorerAccountChain("BTC") {
		t.Fatal("BTC mistaken for account chain")
	}
}

func TestWithdrawHistorySummaryDoesNotCompeteWithFullPackageDownloads(t *testing.T) {
	guard := newResourceGuard(32)
	for len(guard.gates[explorerWorkHeavy]) < cap(guard.gates[explorerWorkHeavy]) {
		guard.gates[explorerWorkHeavy] <- struct{}{}
	}
	handler := guard.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	for _, testcase := range []struct {
		query  string
		status int
	}{{"", 503}, {"?summary_only=1", 200}, {"?summary_only=true", 503}} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/frost/withdraw/result"+testcase.query, nil))
		if w.Code != testcase.status {
			t.Fatalf("query=%s status=%d expected=%d", testcase.query, w.Code, testcase.status)
		}
	}
}
