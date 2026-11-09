package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"dex/config"
	"dex/logs"
)

func newTrafficGuardTestManager(t *testing.T, cfg config.PeerGateConfig) *HandlerManager {
	t.Helper()
	pg := NewPeerGate(cfg, logs.NewNodeLogger("traffic-guard-test", 0))
	t.Cleanup(pg.Stop)
	return &HandlerManager{
		peerGate:         pg,
		cfg:              config.DefaultConfig(),
		registeredRoutes: make(map[string]struct{}),
	}
}

func serveTrafficRequest(handler http.Handler, path, remoteAddr string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = remoteAddr
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func TestWrapTrafficProtectsDirectAndUnmatchedRoutes(t *testing.T) {
	cfg := config.DefaultPeerGateConfig()
	cfg.UnknownRPS = 1000
	cfg.UnknownBurst = 1000
	cfg.PathRateLimits = map[string]float64{
		"/direct":        1,
		unmatchedRouteID: 1,
	}
	hm := newTrafficGuardTestManager(t, cfg)
	mux := http.NewServeMux()
	mux.HandleFunc("/direct", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	handler := hm.WrapTraffic(mux, mux)

	if got := serveTrafficRequest(handler, "/direct", "192.0.2.1:1001").Code; got != http.StatusNoContent {
		t.Fatalf("first direct request=%d, want %d", got, http.StatusNoContent)
	}
	if got := serveTrafficRequest(handler, "/direct", "192.0.2.2:1002").Code; got != http.StatusTooManyRequests {
		t.Fatalf("second direct request=%d, want route throttling", got)
	}

	if got := serveTrafficRequest(handler, "/random-a", "192.0.2.3:1003").Code; got != http.StatusNotFound {
		t.Fatalf("first unmatched request=%d, want %d", got, http.StatusNotFound)
	}
	if got := serveTrafficRequest(handler, "/random-b", "192.0.2.4:1004").Code; got != http.StatusTooManyRequests {
		t.Fatalf("random paths did not share the unmatched bucket: status=%d", got)
	}
}

func TestWrapTrafficDoesNotDoubleChargeRegisteredRoute(t *testing.T) {
	cfg := config.DefaultPeerGateConfig()
	cfg.UnknownRPS = 1000
	cfg.UnknownBurst = 1000
	cfg.PathRateLimits = map[string]float64{"/managed": 1}
	hm := newTrafficGuardTestManager(t, cfg)
	hm.registeredRoutes["/managed"] = struct{}{}

	mux := http.NewServeMux()
	mux.HandleFunc("/managed", hm.peerGate.Middleware("/managed", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	if got := serveTrafficRequest(hm.WrapTraffic(mux, mux), "/managed", "192.0.2.10:1001").Code; got != http.StatusNoContent {
		t.Fatalf("registered route was charged twice: status=%d", got)
	}
}

func TestCanonicalRouteIDCollapsesUnknownPaths(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/known", func(http.ResponseWriter, *http.Request) {})
	if got := CanonicalRouteID(mux, httptest.NewRequest(http.MethodGet, "/known", nil)); got != "/known" {
		t.Fatalf("known route ID=%q", got)
	}
	for _, path := range []string{"/random-a", "/random-b"} {
		if got := CanonicalRouteID(mux, httptest.NewRequest(http.MethodGet, path, nil)); got != unmatchedRouteID {
			t.Fatalf("unknown route %q ID=%q, want %q", path, got, unmatchedRouteID)
		}
	}
}

func TestWrapTrafficProtectsServeMuxCanonicalRedirects(t *testing.T) {
	cfg := config.DefaultPeerGateConfig()
	cfg.UnknownRPS, cfg.UnknownBurst = 1000, 1000
	cfg.PathRateLimits = map[string]float64{redirectedRouteID: 1}
	hm := newTrafficGuardTestManager(t, cfg)
	mux := http.NewServeMux()
	hm.RegisterRoutes(mux)
	handler := hm.WrapTraffic(mux, mux)
	for i, path := range []string{"//pullquery", "/x/../pullquery", "/./pullquery"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.RemoteAddr = "192.0.2.1:1000"
		if route := CanonicalRouteID(mux, request); route != redirectedRouteID {
			t.Fatalf("%s route=%q; redirect must not inherit the bound handler", path, route)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		want := http.StatusTooManyRequests
		if i == 0 {
			want = http.StatusMovedPermanently
		}
		if recorder.Code != want {
			t.Fatalf("%s status=%d, want shared redirect budget status=%d", path, recorder.Code, want)
		}
	}
	request := httptest.NewRequest(http.MethodPost, "//pullquery", bytes.NewReader(make([]byte, controlRequestBodyLimit+1)))
	request.RemoteAddr = "192.0.2.2:1000"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("redirect bypassed body limit: %d", recorder.Code)
	}
}

func TestCanonicalRouteIDPreservesEscapedSegmentsAndCONNECT(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/pullquery", func(http.ResponseWriter, *http.Request) {})
	for _, target := range []string{"/pull%71uery", "/x/%2e%2e/pullquery"} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		if CanonicalRouteID(mux, request) == redirectedRouteID {
			t.Fatalf("escaped path %s is not a ServeMux clean-path redirect", target)
		}
	}
	request := httptest.NewRequest(http.MethodConnect, "https://example.test//pullquery", nil)
	if CanonicalRouteID(mux, request) == redirectedRouteID {
		t.Fatal("CONNECT path must not be cleaned")
	}
}

func TestWrapTrafficProtectsTrailingSlashRedirectWithoutDoubleChargingSubtree(t *testing.T) {
	cfg := config.DefaultPeerGateConfig()
	cfg.UnknownRPS, cfg.UnknownBurst = 1000, 1000
	cfg.PathRateLimits = map[string]float64{redirectedRouteID: 1, "/prefix/": 1}
	hm := newTrafficGuardTestManager(t, cfg)
	hm.registeredRoutes["/prefix/"] = struct{}{}
	mux := http.NewServeMux()
	mux.HandleFunc("/prefix/", hm.peerGate.Middleware("/prefix/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	handler := hm.WrapTraffic(mux, mux)
	for i, want := range []int{http.StatusMovedPermanently, http.StatusTooManyRequests} {
		if got := serveTrafficRequest(handler, "/prefix", "192.0.2.1:1000").Code; got != want {
			t.Fatalf("redirect %d status=%d, want=%d", i, got, want)
		}
	}
	if got := serveTrafficRequest(handler, "/prefix/child", "192.0.2.1:1000").Code; got != http.StatusNoContent {
		t.Fatalf("subtree handler was double charged or lost its budget to redirects: %d", got)
	}
}
