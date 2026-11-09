package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func frostUTXOProxyTestServer(transport http.RoundTripper) *server {
	client := &http.Client{Transport: transport}
	return &server{client: client, gateway: newNodeGateway(client, []string{"node.example:6000"}, 4), timeout: time.Second}
}

func TestFrostVaultUTXOProxyPreservesBoundedPageAndUpstreamErrors(t *testing.T) {
	calls := 0
	srv := frostUTXOProxyTestServer(explorerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Method != http.MethodGet || req.URL.Host != "node.example:6000" || req.URL.Path != "/frost/vault/utxos" || req.URL.Query().Has("node") || req.URL.Query().Get("chain") != "btc" || req.URL.Query().Get("vault_id") != "0" || req.URL.Query().Get("limit") != "200" || req.URL.Query().Get("cursor") != "opaque-cursor" {
			t.Fatalf("unexpected upstream request %s %s", req.Method, req.URL)
		}
		body, status := `{"chain":"btc","vault_id":0,"items":[],"limit":200,"next_cursor":"next","has_more":true}`, http.StatusOK
		if calls == 2 {
			body, status = `{"error":"UTXO query unavailable"}`, http.StatusServiceUnavailable
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	}))
	for _, wantedStatus := range []int{200, 503} {
		rec := httptest.NewRecorder()
		srv.handleFrostVaultUTXOs(rec, httptest.NewRequest(http.MethodGet, "/api/frost/vault/utxos?node=node-0&chain=btc&vault_id=0&limit=200&cursor=opaque-cursor", nil))
		if rec.Code != wantedStatus || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
		}
		if wantedStatus == 503 && !strings.Contains(rec.Body.String(), "unavailable") {
			t.Fatalf("upstream error replaced with empty list: %s", rec.Body)
		}
	}
	if calls != 2 || len(srv.gateway.global) != 0 || len(srv.gateway.perNode["node.example:6000"]) != 0 {
		t.Fatalf("calls=%d leaked gateway slots", calls)
	}
}

func TestFrostVaultUTXOProxyRejectsInvalidParametersBeforeDial(t *testing.T) {
	srv := frostUTXOProxyTestServer(explorerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		t.Fatalf("invalid query dialed upstream: %s", req.URL)
		return nil, errors.New("unexpected upstream request")
	}))
	for _, query := range []string{
		"chain=btc", "vault_id=0", "chain=eth&vault_id=0", "chain=btc&vault_id=-1", "chain=btc&vault_id=4294967296",
		"chain=btc&vault_id=01", "chain=btc&vault_id=0&limit=201", "chain=btc&vault_id=0&limit=0", "chain=btc&vault_id=0&limit=+1",
		"chain=btc&vault_id=0&limit=1&limit=2", "chain=btc&vault_id=0&vault_id=1", "chain=btc&vault_id=0&offset=1",
		"chain=btc&vault_id=0&cursor=" + strings.Repeat("a", 1025), "chain=btc&vault_id=0;limit=1",
		"node=evil.example%3A6000&chain=btc&vault_id=0", "node=node-0&node=node-0&chain=btc&vault_id=0",
		strings.Repeat("x", 4097),
	} {
		rec := httptest.NewRecorder()
		srv.handleFrostVaultUTXOs(rec, httptest.NewRequest(http.MethodGet, "/api/frost/vault/utxos?"+query, nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("query=%q status=%d body=%s", query, rec.Code, rec.Body)
		}
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		rec := httptest.NewRecorder()
		srv.handleFrostVaultUTXOs(rec, httptest.NewRequest(method, "/api/frost/vault/utxos?chain=btc&vault_id=0", nil))
		if rec.Code != 405 || rec.Header().Get("Allow") != "GET" {
			t.Errorf("method=%s status=%d", method, rec.Code)
		}
	}
}

func TestFrostVaultUTXOProxyCancellationReleasesGateway(t *testing.T) {
	entered := make(chan struct{})
	srv := frostUTXOProxyTestServer(explorerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		close(entered)
		<-req.Context().Done()
		return nil, req.Context().Err()
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		srv.handleFrostVaultUTXOs(rec, httptest.NewRequest(http.MethodGet, "/api/frost/vault/utxos?chain=btc&vault_id=0", nil).WithContext(ctx))
		result <- rec
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("proxy did not enter gateway")
	}
	cancel()
	select {
	case rec := <-result:
		if rec.Code != 503 || len(srv.gateway.global) != 0 || len(srv.gateway.perNode["node.example:6000"]) != 0 {
			t.Fatalf("cancel status=%d leaked gateway slots", rec.Code)
		}
	case <-time.After(time.Second):
		t.Fatal("proxy ignored cancellation")
	}
}

func TestFrostVaultUTXOProxyResponseLimitAndHeavyBudget(t *testing.T) {
	srv := frostUTXOProxyTestServer(explorerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(strings.Repeat("x", 100))), Request: req}, nil
	}))
	srv.gateway.maxResponseBytes = 64
	rec := httptest.NewRecorder()
	srv.handleFrostVaultUTXOs(rec, httptest.NewRequest(http.MethodGet, "/api/frost/vault/utxos?chain=btc&vault_id=0", nil))
	if rec.Code != http.StatusBadGateway || len(srv.gateway.global) != 0 {
		t.Fatalf("over-budget response status=%d gateway slots=%d", rec.Code, len(srv.gateway.global))
	}
	budget := explorerBudgetForRoute("/api/frost/vault/utxos")
	if budget.class != explorerWorkHeavy || budget.timeout != 8*time.Second || budget.bodyLimit != explorerDefaultBodyLimit {
		t.Fatalf("UTXO route missing common heavy budget: %+v", budget)
	}
	guard := newResourceGuard(4)
	guard.gates[explorerWorkHeavy] = make(chan struct{}, 1)
	guard.gates[explorerWorkHeavy] <- struct{}{}
	called := false
	guarded := guard.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	rec = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/frost/vault/utxos?chain=btc&vault_id=0", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	guarded.ServeHTTP(rec, request)
	if called || rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("heavy admission bypassed called=%v status=%d", called, rec.Code)
	}
}
