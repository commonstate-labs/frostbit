package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"dex/config"
)

func TestRequireExplorerServiceTokenRejectsUnsetSecret(t *testing.T) {
	t.Setenv(config.ExplorerServiceTokenEnv, "")
	called := false
	handler := requireExplorerServiceToken(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodPost, "/debug/toggle_miner?op=join", nil)
	request.Header.Set(config.ExplorerServiceHeader, "explorer")
	request.Header.Set(config.ExplorerServiceTokenHeader, "guessed")
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	if recorder.Code != http.StatusForbidden || called {
		t.Fatalf("status=%d called=%t, want 403/false", recorder.Code, called)
	}
}

func TestRequireExplorerServiceTokenFailsClosed(t *testing.T) {
	t.Setenv(config.ExplorerServiceTokenEnv, "miner-service-secret")
	called := 0
	handler := requireExplorerServiceToken(func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(http.StatusNoContent)
	})

	tests := []struct {
		name    string
		service string
		token   string
		status  int
	}{
		{name: "missing headers", status: http.StatusForbidden},
		{name: "service name only", service: "explorer", status: http.StatusForbidden},
		{name: "token only", token: "miner-service-secret", status: http.StatusForbidden},
		{name: "wrong token", service: "explorer", token: "wrong", status: http.StatusForbidden},
		{name: "valid", service: "explorer", token: "miner-service-secret", status: http.StatusNoContent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/debug/toggle_miner?op=join", nil)
			if test.service != "" {
				request.Header.Set(config.ExplorerServiceHeader, test.service)
			}
			if test.token != "" {
				request.Header.Set(config.ExplorerServiceTokenHeader, test.token)
			}
			recorder := httptest.NewRecorder()
			handler(recorder, request)
			if recorder.Code != test.status {
				t.Fatalf("status=%d, want %d; body=%s", recorder.Code, test.status, recorder.Body.String())
			}
		})
	}
	if called != 1 {
		t.Fatalf("protected handler called %d times, want 1", called)
	}
}

func TestExplorerAdmissionWaitAllowsOnlyAuthenticatedOrLocalExplorer(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/tx", nil)
	req.Header.Set(config.ExplorerServiceHeader, "explorer")

	t.Setenv(config.ExplorerServiceTokenEnv, "")
	req.RemoteAddr = "127.0.0.1:12345"
	if !explorerServiceAuthorized(req, true) || explorerServiceAuthorized(req, false) {
		t.Fatal("loopback development exception did not stay scoped to admission wait")
	}
	req.RemoteAddr = "192.0.2.10:12345"
	if explorerServiceAuthorized(req, true) {
		t.Fatal("remote request bypassed missing service token")
	}

	t.Setenv(config.ExplorerServiceTokenEnv, "service-secret")
	req.Header.Set(config.ExplorerServiceTokenHeader, "service-secret")
	if !explorerServiceAuthorized(req, true) {
		t.Fatal("authenticated Explorer service was rejected")
	}
}

func TestOperatorMinerRouteRequiresServiceTokenAndPOST(t *testing.T) {
	t.Setenv(config.ExplorerServiceTokenEnv, "miner-service-secret")
	cfg := config.DefaultConfig()
	hm := &HandlerManager{cfg: cfg}
	hm.SetOperatorControlAPI(true)
	mux := http.NewServeMux()
	hm.RegisterRoutes(mux)

	unauthorized := httptest.NewRecorder()
	mux.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/debug/toggle_miner?op=join", nil))
	if unauthorized.Code != http.StatusForbidden {
		t.Fatalf("unauthorized status=%d, want 403", unauthorized.Code)
	}

	getRequest := httptest.NewRequest(http.MethodGet, "/debug/toggle_miner?op=join", nil)
	getRequest.Header.Set(config.ExplorerServiceHeader, "explorer")
	getRequest.Header.Set(config.ExplorerServiceTokenHeader, "miner-service-secret")
	methodRecorder := httptest.NewRecorder()
	mux.ServeHTTP(methodRecorder, getRequest)
	if methodRecorder.Code != http.StatusMethodNotAllowed || methodRecorder.Header().Get("Allow") != http.MethodPost {
		t.Fatalf("GET status=%d allow=%q", methodRecorder.Code, methodRecorder.Header().Get("Allow"))
	}
}
