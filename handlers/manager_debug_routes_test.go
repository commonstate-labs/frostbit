package handlers

import (
	"dex/config"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegisterRoutesDebugAPIGating(t *testing.T) {
	broadDebugRoutes := []string{
		"/logs",
		"/orderbook/debug",
		"/frost/metrics",
	}
	operatorRoutes := []string{
		"/pause",
		"/debug/toggle_miner",
	}
	coreRoutes := []string{
		"/status",
		"/pushquery",
		"/frostmsg",
		"/frost/health",
		"/future/markets",
	}

	for _, enabled := range []bool{false, true} {
		enabled := enabled
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.Server.EnableDebugAPI = enabled
			hm := &HandlerManager{cfg: cfg}
			mux := http.NewServeMux()
			hm.RegisterRoutes(mux)

			for _, path := range broadDebugRoutes {
				_, pattern := mux.Handler(httptest.NewRequest(http.MethodGet, path, nil))
				if enabled && pattern == "" {
					t.Errorf("debug route %s was not registered when enabled", path)
				}
				if !enabled && pattern != "" {
					t.Errorf("debug route %s registered as %q when disabled", path, pattern)
				}
				if !enabled {
					recorder := httptest.NewRecorder()
					mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
					if recorder.Code != http.StatusNotFound {
						t.Errorf("debug route %s returned %d when disabled, want 404", path, recorder.Code)
					}
				}
			}
			for _, path := range operatorRoutes {
				_, pattern := mux.Handler(httptest.NewRequest(http.MethodGet, path, nil))
				if pattern != "" {
					t.Errorf("read-only debug mode registered operator mutation route %s as %q", path, pattern)
				}
			}

			for _, path := range coreRoutes {
				_, pattern := mux.Handler(httptest.NewRequest(http.MethodGet, path, nil))
				if pattern == "" {
					t.Errorf("core route %s was not registered", path)
				}
			}
		})
	}
}

func TestRegisterRoutesOperatorControlAPIDoesNotExposeBroadDebugRoutes(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.EnableDebugAPI = false
	hm := &HandlerManager{cfg: cfg}
	hm.SetOperatorControlAPI(true)
	mux := http.NewServeMux()
	hm.RegisterRoutes(mux)

	for _, path := range []string{"/pause", "/debug/toggle_miner"} {
		if _, pattern := mux.Handler(httptest.NewRequest(http.MethodPost, path, nil)); pattern == "" {
			t.Fatalf("operator route %s was not registered", path)
		}
	}
	for _, path := range []string{"/logs", "/orderbook/debug", "/frost/metrics"} {
		if _, pattern := mux.Handler(httptest.NewRequest(http.MethodGet, path, nil)); pattern != "" {
			t.Fatalf("broad debug route %s registered as %q", path, pattern)
		}
	}

	t.Setenv(config.ExplorerServiceTokenEnv, "operator-test-secret")
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/pause", nil))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("operator pause without Explorer token returned %d, want 403", recorder.Code)
	}
}
