package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"dex/handlers"
	"dex/stats"
)

func TestLatencyMetricsUseCanonicalRouteForRandom404s(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/known", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	node := &NodeInstance{HandlerManager: &handlers.HandlerManager{Stats: stats.NewStats()}}
	handler := wrapHandlerWithLatencyMetrics(node, mux, mux)

	for _, path := range []string{"/random-a", "/random-b", "/random-c"} {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/known", nil))

	got := node.HandlerManager.Stats.GetLatencyStats(false)
	if got["handler/_unmatched"].Count != 3 {
		t.Fatalf("unmatched metric count=%d, want 3; metrics=%v", got["handler/_unmatched"].Count, got)
	}
	if got["handler/known"].Count != 1 {
		t.Fatalf("known metric count=%d, want 1; metrics=%v", got["handler/known"].Count, got)
	}
	if len(got) != 2 {
		t.Fatalf("random raw paths created extra metrics: %v", got)
	}
}
