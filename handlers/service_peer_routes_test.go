package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dex/config"
	"dex/logs"
)

type serviceReadDeadlineRecorder struct {
	*httptest.ResponseRecorder
	deadline time.Time
	sawSet   bool
}

func (w *serviceReadDeadlineRecorder) SetReadDeadline(deadline time.Time) error {
	w.deadline = deadline
	if !deadline.IsZero() {
		w.sawSet = true
	}
	return nil
}

type serviceDeadlineObservingReader struct {
	w       *serviceReadDeadlineRecorder
	inner   *strings.Reader
	sawRead bool
}

func (r *serviceDeadlineObservingReader) Read(dst []byte) (int, error) {
	if !r.w.deadline.IsZero() {
		r.sawRead = true
	}
	return r.inner.Read(dst)
}

func TestServiceRoutesUseDataLaneWithoutMinerTrust(t *testing.T) {
	pg := NewPeerGate(config.DefaultConfig().PeerGate, logs.NewNodeLogger("service-routes", 0))
	t.Cleanup(pg.Stop)
	paths := []string{
		"/service/peers",
		"/service/probe",
		"/service/callback",
		"/service/gossip",
		"/service/syncblocks",
	}
	for _, path := range paths {
		if !pg.syncPaths[path] {
			t.Fatalf("%s is not in the bounded data/sync lane", path)
		}
		if pg.consensusPaths[path] || pg.trustedOnlyPaths[path] {
			t.Fatalf("%s unexpectedly grants miner consensus/trusted routing", path)
		}
	}
}

func TestServiceRouteFailsClosedWithoutPublicIdentity(t *testing.T) {
	hm := &HandlerManager{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/service/peers", strings.NewReader(`{}`))
	hm.HandleServicePeerExchange(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
}

func TestDecodeBoundedServiceJSONRejectsUnknownAndOversize(t *testing.T) {
	var value struct {
		Known string `json:"known"`
	}
	if err := decodeBoundedServiceJSON(strings.NewReader(`{"known":"ok","unknown":1}`), 128, &value); err == nil {
		t.Fatal("unknown JSON field was accepted")
	}
	if err := decodeBoundedServiceJSON(strings.NewReader(`{"known":"payload"}`), 4, &value); err == nil {
		t.Fatal("oversized JSON body was accepted")
	}
}

func TestDecodeBoundedServiceRequestJSONAppliesReadDeadline(t *testing.T) {
	w := &serviceReadDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	body := &serviceDeadlineObservingReader{w: w, inner: strings.NewReader(`{"known":"ok"}`)}
	r := httptest.NewRequest(http.MethodPost, "/service/peers", body)
	var value struct {
		Known string `json:"known"`
	}
	if err := decodeBoundedServiceRequestJSON(w, r, 128, time.Second, &value); err != nil {
		t.Fatal(err)
	}
	if !w.sawSet || !body.sawRead {
		t.Fatal("service request body was read without an active read deadline")
	}
	if !w.deadline.IsZero() {
		t.Fatal("service request read deadline was not cleared after decoding")
	}
}
