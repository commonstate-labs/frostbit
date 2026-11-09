package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublicCORSExactOriginsAndCredentials(t *testing.T) {
	const allowed = "https://dex.example"
	for _, tc := range []struct {
		name, origin string
		status       int
	}{
		{"allowed", allowed, http.StatusOK},
		{"non-browser", "", http.StatusOK},
		{"other-origin", "https://other.example", http.StatusForbidden},
		{"hostname-suffix", allowed + ".evil", http.StatusForbidden},
		{"different-port", allowed + ":8443", http.StatusForbidden},
		{"null", "null", http.StatusForbidden},
		{"path", allowed + "/path", http.StatusForbidden},
		{"query", allowed + "?x=1", http.StatusForbidden},
		{"empty-query", allowed + "?", http.StatusForbidden},
		{"fragment", allowed + "#x", http.StatusForbidden},
		{"credentials", "https://user@dex.example", http.StatusForbidden},
		{"non-http", "ftp://dex.example", http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := &server{corsOrigins: []string{allowed, "ftp://dex.example", allowed + "/path"}}
			called := false
			handler := srv.withPublicCORS(newResourceGuard(4), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.Header().Add("Vary", "Accept-Encoding")
				w.WriteHeader(http.StatusOK)
			}))
			req := httptest.NewRequest(http.MethodGet, "https://backend.example/api/nodes", nil)
			req.Header.Set("Origin", tc.origin)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != tc.status || called != (tc.status == http.StatusOK) {
				t.Fatalf("status=%d called=%v body=%s", rr.Code, called, rr.Body.String())
			}
			wantOrigin, wantCredentials := "", ""
			if tc.origin == allowed {
				wantOrigin, wantCredentials = allowed, "true"
			}
			if got := rr.Header().Get("Access-Control-Allow-Origin"); got != wantOrigin {
				t.Fatalf("allow origin=%q want %q", got, wantOrigin)
			}
			if got := rr.Header().Get("Access-Control-Allow-Credentials"); got != wantCredentials {
				t.Fatalf("allow credentials=%q want %q", got, wantCredentials)
			}
			if !corsTestVaryContains(rr.Header(), "Origin") || (called && !corsTestVaryContains(rr.Header(), "Accept-Encoding")) {
				t.Fatalf("incomplete Vary: %v", rr.Header().Values("Vary"))
			}
		})
	}
}

func TestPublicCORSPreflightDoesNotCallRoutes(t *testing.T) {
	const allowed = "https://dex.example"
	for _, tc := range []struct {
		name, method, headers string
		status                int
	}{
		{"get", http.MethodGet, "", http.StatusNoContent},
		{"post-json", http.MethodPost, "content-type", http.StatusNoContent},
		{"csrf", http.MethodPost, "Content-Type, x-explorer-csrf-token", http.StatusNoContent},
		{"legacy-options", "", "", http.StatusNoContent},
		{"unlisted-method", http.MethodDelete, "", http.StatusForbidden},
		{"unlisted-header", http.MethodPost, "Content-Type, X-Explorer-Admin-Token", http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := &server{corsOrigins: []string{allowed}}
			handler := srv.withPublicCORS(newResourceGuard(4), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("preflight reached a route/upstream")
			}))
			req := httptest.NewRequest(http.MethodOptions, "https://backend.example/api/summary", nil)
			req.Header.Set("Origin", allowed)
			req.Header.Set("Access-Control-Request-Method", tc.method)
			req.Header.Set("Access-Control-Request-Headers", tc.headers)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != tc.status {
				t.Fatalf("status=%d want %d body=%s", rr.Code, tc.status, rr.Body.String())
			}
			if rr.Header().Get("Access-Control-Allow-Origin") != allowed || rr.Header().Get("Access-Control-Allow-Credentials") != "true" {
				t.Fatalf("missing exact credentialed CORS: %v", rr.Header())
			}
			for _, vary := range []string{"Origin", "Access-Control-Request-Method", "Access-Control-Request-Headers"} {
				if !corsTestVaryContains(rr.Header(), vary) {
					t.Fatalf("missing Vary %q: %v", vary, rr.Header().Values("Vary"))
				}
			}
			if tc.status == http.StatusNoContent && (rr.Header().Get("Access-Control-Allow-Methods") != explorerCORSMethods || rr.Header().Get("Access-Control-Allow-Headers") != "Content-Type, "+simulatorCSRFHeader) {
				t.Fatalf("missing allowed methods/headers: %v", rr.Header())
			}
		})
	}
}

func TestPublicCORSWalletCannotReflectUnlistedWebOrigins(t *testing.T) {
	srv := &server{corsOrigins: []string{"https://dex.example"}}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/wallet/submittx", srv.handleWalletSubmitTx)
	mux.HandleFunc("/api/wallet/nonce", srv.handleWalletNonce)
	mux.HandleFunc("/api/wallet/receipt", srv.handleWalletReceipt)
	mux.HandleFunc("/api/wallet/deposit-address", srv.handleWalletDepositAddress)
	mux.HandleFunc("/api/nodes", srv.handleNodes)
	handler := srv.withPublicCORS(newResourceGuard(4), mux)
	for _, path := range []string{"/api/wallet/submittx", "/api/wallet/nonce", "/api/wallet/receipt", "/api/wallet/deposit-address"} {
		for _, origin := range []string{"https://dex.example", "chrome-extension://abcdefghijklmnopabcdefghijklmnop", "moz-extension://wallet-extension", "https://evil.example"} {
			req := httptest.NewRequest(http.MethodPost, "https://backend.example"+path, strings.NewReader("{"))
			req.Header.Set("Origin", origin)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if origin == "https://evil.example" {
				if rr.Code != http.StatusForbidden || rr.Header().Get("Access-Control-Allow-Origin") != "" {
					t.Fatalf("wallet reflected rejected origin: path=%s status=%d headers=%v", path, rr.Code, rr.Header())
				}
			} else if rr.Code != http.StatusOK || rr.Header().Get("Access-Control-Allow-Origin") != origin || !strings.Contains(rr.Body.String(), "invalid request") {
				t.Fatalf("wallet compatibility failed: path=%s origin=%s status=%d body=%s", path, origin, rr.Code, rr.Body.String())
			}
		}
	}
	for _, path := range []string{"/api/summary", "/api/operator/node/diagnostics", "/api/wallet/nonce/extra"} {
		req := httptest.NewRequest(http.MethodGet, "https://backend.example"+path, nil)
		req.Header.Set("Origin", "chrome-extension://abcdefghijklmnopabcdefghijklmnop")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden || rr.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("extension escaped wallet routes: path=%s status=%d headers=%v", path, rr.Code, rr.Header())
		}
	}
	                                                                        
	                                                                   
	for _, path := range []string{"/api/nodes", "/api/tokens", "/api/address", "/api/token-icon"} {
		req := httptest.NewRequest(http.MethodOptions, "https://backend.example"+path, nil)
		req.Header.Set("Origin", "chrome-extension://abcdefghijklmnopabcdefghijklmnop")
		req.Header.Set("Access-Control-Request-Method", http.MethodPost)
		req.Header.Set("Access-Control-Request-Headers", "Content-Type")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusNoContent || rr.Header().Get("Access-Control-Allow-Origin") != req.Header.Get("Origin") {
			t.Fatalf("wallet read dependency failed: path=%s status=%d headers=%v", path, rr.Code, rr.Header())
		}
	}
}

func TestPublicCORSRetainsLocalDefaultAndOriginlessOptions(t *testing.T) {
	for _, origin := range []string{"https://backend.example", "http://localhost:5173", "http://127.0.0.1:8080", "http://[::1]:8080", ""} {
		srv := &server{}
		called := false
		handler := srv.withPublicCORS(newResourceGuard(4), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			called = true
			w.WriteHeader(http.StatusAccepted)
		}))
		method := http.MethodGet
		if origin == "" {
			method = http.MethodOptions
		}
		req := httptest.NewRequest(method, "https://backend.example/api/nodes", nil)
		req.Header.Set("Origin", origin)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusAccepted || !called || rr.Header().Get("Access-Control-Allow-Origin") != origin {
			t.Fatalf("local/originless behavior failed: origin=%q status=%d called=%v headers=%v", origin, rr.Code, called, rr.Header())
		}
	}
}

func TestPublicCORSCoversAdmissionErrorsAndPreflightBudget(t *testing.T) {
	const origin = "https://dex.example"
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		for _, method := range []string{http.MethodGet, http.MethodOptions} {
			guard := newResourceGuard(1)
			srv := &server{corsOrigins: []string{origin}}
			req := httptest.NewRequest(method, "https://backend.example/api/nodes", nil)
			req.Header.Set("Origin", origin)
			if status == http.StatusServiceUnavailable {
				guard.global <- struct{}{}
			} else {
				guard.allowClient(req, explorerBudgetForRoute(req.URL.Path))
				guard.mu.Lock()
				limiter := guard.clients[guard.clientIP(req)].limits[explorerWorkStandard]
				limiter.SetLimit(0)
				limiter.SetBurst(0)
				guard.mu.Unlock()
			}
			handler := srv.withPublicCORS(guard, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("admission failure reached handler")
			}))
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != status || rr.Header().Get("Access-Control-Allow-Origin") != origin || rr.Header().Get("Access-Control-Allow-Credentials") != "true" {
				t.Fatalf("method=%s status=%d want %d headers=%v", method, rr.Code, status, rr.Header())
			}
		}
	}
}

func TestPublicCORSDoesNotAuthorizeOperatorMutations(t *testing.T) {
	auth, session := newOperatorNodeControlTestAuth(t)
	srv := &server{corsOrigins: []string{auth.publicOrigin, "https://other-explorer.example"}}
	called := 0
	handler := srv.withPublicCORS(newResourceGuard(4), auth.requireOperatorMutation(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(http.StatusNoContent)
	})))
	for _, origin := range []string{"https://other-explorer.example", auth.publicOrigin} {
		req := httptest.NewRequest(http.MethodPost, "https://backend.example/api/node/pause", nil)
		addOperatorSession(req, auth, session, true)
		req.Header.Set("Origin", origin)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		want := http.StatusForbidden
		if origin == auth.publicOrigin {
			want = http.StatusNoContent
		}
		if rr.Code != want || rr.Header().Get("Access-Control-Allow-Origin") != origin {
			t.Fatalf("origin=%q status=%d want %d headers=%v", origin, rr.Code, want, rr.Header())
		}
	}
	if called != 1 {
		t.Fatalf("operator mutation calls=%d want 1", called)
	}
}

func corsTestVaryContains(header http.Header, name string) bool {
	for _, value := range header.Values("Vary") {
		for _, field := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(field), name) {
				return true
			}
		}
	}
	return false
}
