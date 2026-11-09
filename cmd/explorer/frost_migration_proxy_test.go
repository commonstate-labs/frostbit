package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFrostMigrationDownloadProxyUsesMigrationArtifactEndpoint(t *testing.T) {
	client := &http.Client{Transport: explorerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || req.URL.Path != "/frost/vault/migration/download" {
			t.Fatalf("node request = %s %s", req.Method, req.URL.Path)
		}
		if got := req.URL.Query().Get("migration_id"); got != "migration-btc-0-2" {
			t.Fatalf("migration_id = %q", got)
		}
		if got := req.URL.Query().Get("node"); got != "" {
			t.Fatalf("node query leaked upstream: %q", got)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"version":1,"migration_id":"migration-btc-0-2","artifacts":[]}`)),
			Request:    req,
		}, nil
	})}
	srv := &server{
		client:       client,
		timeout:      time.Second,
		defaultNodes: []string{"node.example:6000"},
		allowedNodes: map[string]struct{}{"node.example:6000": {}},
	}

	recorder := httptest.NewRecorder()
	handler := srv.handleFutureProxy("/frost/vault/migration/download")
	handler(recorder, httptest.NewRequest(http.MethodGet,
		"/api/frost/vault/migration/download?node=node.example%3A6000&migration_id=migration-btc-0-2", nil))

	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"migration_id":"migration-btc-0-2"`) {
		t.Fatalf("response status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
