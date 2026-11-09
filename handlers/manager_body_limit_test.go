package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dex/config"
	"dex/logs"
)

func TestRegisterRoutesLimitsBodyBeforePeerGate(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.MaxRequestBodySize = 8
	pg := NewPeerGate(config.DefaultPeerGateConfig(), logs.NewNodeLogger("body-limit-test", 0))
	t.Cleanup(pg.Stop)

	hm := &HandlerManager{peerGate: pg, cfg: cfg}
	mux := http.NewServeMux()
	hm.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/pushquery", strings.NewReader("0123456789"))
	req.RemoteAddr = "203.0.113.10:1234"
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body must be rejected before PeerGate parsing: got=%d want=%d body=%q", rr.Code, http.StatusRequestEntityTooLarge, rr.Body.String())
	}
}
