package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"dex/config"
)

func TestRequireExplorerServiceTokenFailsClosedAndChecksBothHeaders(t *testing.T) {
	called := 0
	next := func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(http.StatusNoContent)
	}

	t.Run("unset token", func(t *testing.T) {
		t.Setenv(config.ExplorerServiceTokenEnv, "")
		handler := requireExplorerServiceToken(next)
		req := httptest.NewRequest(http.MethodGet, "/simulator/config", nil)
		req.Header.Set(config.ExplorerServiceHeader, "explorer")
		recorder := httptest.NewRecorder()
		handler(recorder, req)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("status=%d, want 403", recorder.Code)
		}
	})

	t.Run("missing service header", func(t *testing.T) {
		t.Setenv(config.ExplorerServiceTokenEnv, "secret")
		handler := requireExplorerServiceToken(next)
		req := httptest.NewRequest(http.MethodGet, "/simulator/config", nil)
		req.Header.Set(config.ExplorerServiceTokenHeader, "secret")
		recorder := httptest.NewRecorder()
		handler(recorder, req)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("status=%d, want 403", recorder.Code)
		}
	})

	t.Run("wrong token", func(t *testing.T) {
		t.Setenv(config.ExplorerServiceTokenEnv, "secret")
		handler := requireExplorerServiceToken(next)
		req := httptest.NewRequest(http.MethodGet, "/simulator/config", nil)
		req.Header.Set(config.ExplorerServiceHeader, "explorer")
		req.Header.Set(config.ExplorerServiceTokenHeader, "forged")
		recorder := httptest.NewRecorder()
		handler(recorder, req)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("status=%d, want 403", recorder.Code)
		}
	})

	t.Run("valid", func(t *testing.T) {
		t.Setenv(config.ExplorerServiceTokenEnv, "secret")
		handler := requireExplorerServiceToken(next)
		req := httptest.NewRequest(http.MethodGet, "/simulator/config", nil)
		req.Header.Set(config.ExplorerServiceHeader, "explorer")
		req.Header.Set(config.ExplorerServiceTokenHeader, "secret")
		recorder := httptest.NewRecorder()
		handler(recorder, req)
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("status=%d, want 204", recorder.Code)
		}
	})

	if called != 1 {
		t.Fatalf("next called=%d, want exactly once", called)
	}
}

func TestRegisterSimulatorControlRouteSupportsNarrowOperatorMode(t *testing.T) {
	t.Setenv(config.ExplorerServiceTokenEnv, "operator-test-secret")
	previousSimulator := globalSimulator
	globalSimulator = nil
	t.Cleanup(func() { globalSimulator = previousSimulator })

	disabledMux := http.NewServeMux()
	registerSimulatorControlRoute(disabledMux, &NodeInstance{})
	if _, pattern := disabledMux.Handler(httptest.NewRequest(http.MethodGet, "/simulator/config", nil)); pattern != "" {
		t.Fatalf("disabled simulator control route registered as %q", pattern)
	}

	debugOnlyMux := http.NewServeMux()
	registerSimulatorControlRoute(debugOnlyMux, &NodeInstance{EnableDebugAPI: true})
	if _, pattern := debugOnlyMux.Handler(httptest.NewRequest(http.MethodGet, "/simulator/config", nil)); pattern != "" {
		t.Fatalf("read-only debug mode registered simulator mutation route as %q", pattern)
	}

	mux := http.NewServeMux()
	registerSimulatorControlRoute(mux, &NodeInstance{EnableOperatorControlAPI: true})
	unauthorized := httptest.NewRecorder()
	mux.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/simulator/config", nil))
	if unauthorized.Code != http.StatusForbidden {
		t.Fatalf("unauthorized status=%d, want 403", unauthorized.Code)
	}

	request := httptest.NewRequest(http.MethodGet, "/simulator/config", nil)
	request.Header.Set(config.ExplorerServiceHeader, "explorer")
	request.Header.Set(config.ExplorerServiceTokenHeader, "operator-test-secret")
	authorized := httptest.NewRecorder()
	mux.ServeHTTP(authorized, request)
	if authorized.Code != http.StatusServiceUnavailable {
		t.Fatalf("authorized status=%d, want 503 from absent simulator", authorized.Code)
	}
}
