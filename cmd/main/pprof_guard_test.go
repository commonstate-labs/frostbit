package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPprofGuardRejectsRemoteAndAllowsLoopback(t *testing.T) {
	guarded := wrapPprofHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), 1)

	remote := httptest.NewRequest(http.MethodGet, "/debug/pprof/heap", nil)
	remote.RemoteAddr = "198.51.100.10:1234"
	remoteRecorder := httptest.NewRecorder()
	guarded.ServeHTTP(remoteRecorder, remote)
	if remoteRecorder.Code != http.StatusForbidden {
		t.Fatalf("remote pprof status=%d, want %d", remoteRecorder.Code, http.StatusForbidden)
	}

	local := httptest.NewRequest(http.MethodGet, "/debug/pprof/heap", nil)
	local.RemoteAddr = "127.0.0.1:1234"
	localRecorder := httptest.NewRecorder()
	guarded.ServeHTTP(localRecorder, local)
	if localRecorder.Code != http.StatusNoContent {
		t.Fatalf("loopback pprof status=%d, want %d", localRecorder.Code, http.StatusNoContent)
	}
}

func TestPprofGuardRejectsConcurrentProfile(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	guarded := wrapPprofHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusNoContent)
	}), 1)

	done := make(chan struct{})
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/debug/pprof/profile", nil)
		req.RemoteAddr = "127.0.0.1:1234"
		guarded.ServeHTTP(httptest.NewRecorder(), req)
		close(done)
	}()
	<-entered

	req := httptest.NewRequest(http.MethodGet, "/debug/pprof/trace", nil)
	req.RemoteAddr = "127.0.0.1:1235"
	recorder := httptest.NewRecorder()
	guarded.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("concurrent pprof status=%d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	close(release)
	<-done
}
