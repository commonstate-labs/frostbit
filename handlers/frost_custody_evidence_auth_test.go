package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dex/config"
)

func TestCustodyEvidenceRequiresServiceAuthenticationBeforeIO(t *testing.T) {
	                                                                          
	var hm *HandlerManager
	for _, route := range []struct {
		method, path string
		serve        http.HandlerFunc
	}{
		{http.MethodPost, "/frost/btc/internal-outputs", hm.HandleFrostBTCInternalOutputs},
	} {
		for _, test := range []struct{ name, configured, service, token string }{
			{"unset-secret", "", "explorer", "guessed"},
			{"missing-headers", "custody-test-service-token", "", ""},
			{"service-only", "custody-test-service-token", "explorer", ""},
			{"wrong-token", "custody-test-service-token", "explorer", "wrong"},
		} {
			t.Run(route.path+test.name, func(t *testing.T) {
				t.Setenv(config.ExplorerServiceTokenEnv, test.configured)
				r := httptest.NewRequest(route.method, route.path, strings.NewReader(`{}`))
				r.RemoteAddr = "127.0.0.1:1234"
				r.Header.Set(config.ExplorerServiceHeader, test.service)
				r.Header.Set(config.ExplorerServiceTokenHeader, test.token)
				w := httptest.NewRecorder()
				route.serve(w, r)
				if w.Code != http.StatusForbidden {
					t.Fatalf("unauthorized status=%d", w.Code)
				}
			})
		}
		t.Run(route.path+"authenticated-invalid", func(t *testing.T) {
			t.Setenv(config.ExplorerServiceTokenEnv, "custody-test-service-token")
			r := httptest.NewRequest(route.method, route.path, strings.NewReader(`{}`))
			r.Header.Set(config.ExplorerServiceHeader, "explorer")
			r.Header.Set(config.ExplorerServiceTokenHeader, "custody-test-service-token")
			w := httptest.NewRecorder()
			route.serve(w, r)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("authenticated invalid request status=%d", w.Code)
			}
		})
	}
}
