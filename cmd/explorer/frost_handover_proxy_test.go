package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFrostV2ReadonlyProxiesConstrainTargetAndMethod(t *testing.T) {
	for _, path := range []string{"/frost/vault/handover/page", "/frost/withdraw/batch"} {
		t.Run(path, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: explorerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.Method != http.MethodGet || req.URL.Host != "node.example:6000" || req.URL.Path != path || req.URL.Query().Get("node") != "" || req.URL.Query().Get("chain") != "eth" || req.URL.Query().Get("vault_id") != "0" {
					t.Fatalf("unexpected upstream request %s", req.URL)
				}
				return &http.Response{StatusCode: http.StatusConflict, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":"proof unavailable"}`)), Request: req}, nil
			})}
			srv := &server{client: client, timeout: time.Second, defaultNodes: []string{"node.example:6000"}, allowedNodes: map[string]struct{}{"node.example:6000": {}}}
			handle := srv.handleFrostReadonlyV2Proxy(path)
			url := "/api" + path + "?node=node.example%3A6000&chain=eth&vault_id=0&from_epoch=1&to_epoch=10&batch_seq=1"
			good := httptest.NewRecorder()
			handle(good, httptest.NewRequest(http.MethodGet, url, nil))
			if good.Code != http.StatusConflict || good.Header().Get("Cache-Control") != "no-store" || !strings.Contains(good.Body.String(), "proof unavailable") {
				t.Fatalf("status=%d body=%s", good.Code, good.Body)
			}
			post := httptest.NewRecorder()
			handle(post, httptest.NewRequest(http.MethodPost, url, nil))
			if post.Code != 405 {
				t.Fatalf("post=%d", post.Code)
			}
			bad := httptest.NewRecorder()
			handle(bad, httptest.NewRequest(http.MethodGet, "/api"+path+"?node=evil.example&chain=eth", nil))
			if bad.Code != 400 || calls != 1 {
				t.Fatalf("bad target=%d calls=%d", bad.Code, calls)
			}
		})
	}
}
