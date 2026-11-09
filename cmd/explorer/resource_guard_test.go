package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"golang.org/x/time/rate"
)

func TestResourceGuardAppliesRequestBodyLimit(t *testing.T) {
	guard := newResourceGuard(4)
	handler := guard.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/anything", strings.NewReader(strings.Repeat("x", int(explorerDefaultBodyLimit)+1)))
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d body=%q, want %d", rr.Code, rr.Body.String(), http.StatusRequestEntityTooLarge)
	}
}

func TestResourceGuardIgnoresForwardedForFromUntrustedPeer(t *testing.T) {
	guard := newResourceGuard(4)
	guard.trustedProxies = parseTrustedProxyPrefixes("10.0.0.0/8")
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.RemoteAddr = "203.0.113.9:1234"
	req.Header.Set("X-Forwarded-For", "198.51.100.7")
	if got := guard.clientIP(req); got != "203.0.113.9" {
		t.Fatalf("clientIP=%q, untrusted peer must not override socket address", got)
	}
}

func TestResourceGuardTrustedProxySeparatesFrontendClients(t *testing.T) {
	guard := newResourceGuard(4)
	guard.trustedProxies = parseTrustedProxyPrefixes("10.0.0.0/8")
	budget := explorerRouteBudget{class: explorerWorkStandard, rate: 1, burst: 1}
	request := func(client string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
		req.RemoteAddr = "10.0.0.5:443"
		req.Header.Set("X-Forwarded-For", client)
		return req
	}
	if !guard.allowClient(request("198.51.100.1"), budget) {
		t.Fatal("first client should receive its own limiter bucket")
	}
	if guard.allowClient(request("198.51.100.1"), budget) {
		t.Fatal("same client must exhaust its one-token bucket")
	}
	if !guard.allowClient(request("198.51.100.2"), budget) {
		t.Fatal("second client behind the same trusted proxy must have an independent bucket")
	}
}

func TestResourceGuardTrustedProxyRejectsPrependedSpoofedHop(t *testing.T) {
	guard := newResourceGuard(4)
	guard.trustedProxies = parseTrustedProxyPrefixes("10.0.0.0/8")
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.RemoteAddr = "10.0.0.5:443"
	                                                                         
	                                                                              
	req.Header.Set("X-Forwarded-For", "192.0.2.123, 198.51.100.8")
	if got := guard.clientIP(req); got != "198.51.100.8" {
		t.Fatalf("clientIP=%q, want nearest untrusted hop", got)
	}
}

func TestResourceGuardLoadsTrustedProxiesOnlyWhenExplicitlyConfigured(t *testing.T) {
	t.Setenv("EXPLORER_TRUSTED_PROXIES", "10.0.0.0/8, 2001:db8::/32, invalid")
	guard := newResourceGuard(4)
	if len(guard.trustedProxies) != 2 {
		t.Fatalf("trusted proxy prefixes=%d, want two valid explicit ranges", len(guard.trustedProxies))
	}
}

func TestResourceGuardClientStateUsesBoundedLRU(t *testing.T) {
	guard := newResourceGuard(4)
	guard.maxClientState = 3
	budget := explorerRouteBudget{class: explorerWorkStandard, rate: rate.Inf, burst: 1}
	request := func(ip string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
		req.RemoteAddr = ip + ":1234"
		return req
	}

	for _, ip := range []string{"192.0.2.1", "192.0.2.2", "192.0.2.3"} {
		if !guard.allowClient(request(ip), budget) {
			t.Fatalf("initial request for %s was rejected", ip)
		}
	}
	                                                                       
	                                   
	guard.allowClient(request("192.0.2.1"), budget)
	guard.allowClient(request("192.0.2.4"), budget)

	guard.mu.Lock()
	defer guard.mu.Unlock()
	if len(guard.clients) != 3 || guard.clientLRU.Len() != 3 {
		t.Fatalf("client state map/list size=%d/%d, want 3/3", len(guard.clients), guard.clientLRU.Len())
	}
	if _, exists := guard.clients["192.0.2.2"]; exists {
		t.Fatal("least recently used client was not evicted")
	}
	for _, ip := range []string{"192.0.2.1", "192.0.2.3", "192.0.2.4"} {
		if _, exists := guard.clients[ip]; !exists {
			t.Fatalf("live client %s was unexpectedly evicted", ip)
		}
	}
	if got := guard.clientLRU.Front().Value; got != "192.0.2.3" {
		t.Fatalf("LRU front=%v, want 192.0.2.3", got)
	}
	if got := guard.clientLRU.Back().Value; got != "192.0.2.4" {
		t.Fatalf("LRU back=%v, want 192.0.2.4", got)
	}
}

func TestResourceGuardClientStateRemainsBoundedConcurrently(t *testing.T) {
	guard := newResourceGuard(4)
	guard.maxClientState = 32
	budget := explorerRouteBudget{class: explorerWorkStandard, rate: rate.Inf, burst: 1}

	var wg sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				client := (worker*17 + i) % 128
				req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
				req.RemoteAddr = fmt.Sprintf("198.51.100.%d:1234", client)
				guard.allowClient(req, budget)
			}
		}(worker)
	}
	wg.Wait()

	guard.mu.Lock()
	defer guard.mu.Unlock()
	if len(guard.clients) > guard.maxClientState {
		t.Fatalf("client state grew to %d, cap=%d", len(guard.clients), guard.maxClientState)
	}
	if guard.clientLRU.Len() != len(guard.clients) {
		t.Fatalf("client state map/list diverged: %d/%d", len(guard.clients), guard.clientLRU.Len())
	}
	seen := make(map[string]struct{}, guard.clientLRU.Len())
	for entry := guard.clientLRU.Front(); entry != nil; entry = entry.Next() {
		key, ok := entry.Value.(string)
		if !ok {
			t.Fatalf("unexpected LRU value type %T", entry.Value)
		}
		if _, duplicate := seen[key]; duplicate {
			t.Fatalf("duplicate LRU entry for %s", key)
		}
		seen[key] = struct{}{}
		state := guard.clients[key]
		if state == nil || state.lruEntry != entry {
			t.Fatalf("map/list entry mismatch for %s", key)
		}
	}
}

func TestResourceGuardHeavyWorkDoesNotQueueUnboundedly(t *testing.T) {
	guard := newResourceGuard(4)
	                                                                           
	                                                                            
	guard.gates[explorerWorkHeavy] = make(chan struct{}, 1)
	entered := make(chan struct{})
	release := make(chan struct{})
	handler := guard.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusOK)
	}))

	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/api/summary", nil)
		req.RemoteAddr = "127.0.0.1:12345"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		firstDone <- rr
	}()
	<-entered

	second := httptest.NewRequest(http.MethodGet, "/api/summary", nil)
	second.RemoteAddr = "127.0.0.2:12345"
	secondRR := httptest.NewRecorder()
	handler.ServeHTTP(secondRR, second)
	if secondRR.Code != http.StatusServiceUnavailable {
		t.Fatalf("second heavy request status=%d body=%q, want %d", secondRR.Code, secondRR.Body.String(), http.StatusServiceUnavailable)
	}

	close(release)
	if first := <-firstDone; first.Code != http.StatusOK {
		t.Fatalf("first heavy request status=%d, want %d", first.Code, http.StatusOK)
	}
}

func TestHistoryAggregationRoutesUseHeavyBudget(t *testing.T) {
	for _, path := range []string{"/api/user/activity", "/api/cert/summary", "/api/referral/rebates", "/api/tokens", "/api/witness/rewards"} {
		if got := explorerBudgetForRoute(path).class; got != explorerWorkHeavy {
			t.Fatalf("route %s class=%d, want heavy", path, got)
		}
	}
}

func TestOperatorRoutesUseAdminBudget(t *testing.T) {
	for _, path := range []string{
		"/api/simulator/auth/challenge",
		"/api/simulator/auth/verify",
		"/api/simulator/auth/logout",
		"/api/simulator/config",
		"/api/node/pause",
		"/api/debug/toggle_miner",
		"/api/operator/node/diagnostics",
	} {
		budget := explorerBudgetForRoute(path)
		if budget.class != explorerWorkAdmin {
			t.Fatalf("route %s class=%d, want admin", path, budget.class)
		}
		if budget.bodyLimit != explorerAdminBodyLimit {
			t.Fatalf("route %s body limit=%d, want %d", path, budget.bodyLimit, explorerAdminBodyLimit)
		}
	}
	if got := explorerBudgetForRoute("/api/simulator/auth/status").class; got != explorerWorkStandard {
		t.Fatalf("status route class=%d, want standard", got)
	}
}
