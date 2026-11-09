package sender

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dex/config"
	"dex/pb"
)

                                                                                             
                                                                                          
                                      
                                                        
                                           
                            
  
                                                            

                                                                    

type fakeNetTimeout struct{}

func (fakeNetTimeout) Error() string   { return "fake i/o timeout" }
func (fakeNetTimeout) Timeout() bool   { return true }
func (fakeNetTimeout) Temporary() bool { return true }

func TestIsTimeoutSendError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"net.Error timeout", fakeNetTimeout{}, true},
		{"plain timeout substring", errors.New("read failed: timeout"), true},
		{"deadline exceeded substring", context.DeadlineExceeded, true},
		{"unrelated", errors.New("connection refused"), false},
	}
	for _, tc := range cases {
		if got := isTimeoutSendError(tc.err); got != tc.want {
			t.Errorf("%s: got=%v want=%v", tc.name, got, tc.want)
		}
	}
}

func TestIsPeerUnavailableSendError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"timeout", fakeNetTimeout{}, true},
		{"deadline", context.DeadlineExceeded, true},
		{"refused", errors.New("connect: connection refused"), true},
		{"no route", errors.New("sendto: no route to host"), true},
		{"reset", errors.New("read: connection reset by peer"), true},
		{"http 403 reachable", &httpStatusError{statusCode: 403}, false},
	}
	for _, tc := range cases {
		if got := isPeerUnavailableSendError(tc.err); got != tc.want {
			t.Errorf("%s: got=%v want=%v", tc.name, got, tc.want)
		}
	}
}

func TestPeerHealthSkipsUntilProbeWindow(t *testing.T) {
	h := newPeerHealthTracker()
	target := "192.0.2.21:6000"
	now := time.Now()

	h.recordResult(target, errors.New("timeout: no recent network activity"))
	if h.shouldSkipAtEnqueue(target, now) {
		t.Fatal("one failure must not quarantine target")
	}
	h.recordResult(target, errors.New("timeout: no recent network activity"))
	if !h.shouldSkipAtEnqueue(target, now) {
		t.Fatal("second failure must quarantine target")
	}
	if skip, probe := h.beginSend(target, now); !skip || probe {
		t.Fatalf("before probe window beginSend=(%v,%v), want skip=true probe=false", skip, probe)
	}

	later := time.Now().Add(peerHealthBaseProbeDelay + time.Second)
	if skip, probe := h.beginSend(target, later); skip || !probe {
		t.Fatalf("after probe window beginSend=(%v,%v), want skip=false probe=true", skip, probe)
	}
	if skip, probe := h.beginSend(target, later); !skip || probe {
		t.Fatalf("probe in flight beginSend=(%v,%v), want skip=true probe=false", skip, probe)
	}
	h.recordResult(target, nil)
	if h.shouldSkipAtEnqueue(target, later) {
		t.Fatal("success must clear quarantine")
	}
}

func TestIsBackpressureSendError(t *testing.T) {
	if isBackpressureSendError(nil) {
		t.Fatal("nil must not be backpressure")
	}
	if isBackpressureSendError(errors.New("plain")) {
		t.Fatal("non-httpStatusError must not be backpressure")
	}
	if !isBackpressureSendError(&httpStatusError{op: "POST", statusCode: 429}) {
		t.Fatal("429 must be classified as backpressure")
	}
	if isBackpressureSendError(&httpStatusError{op: "POST", statusCode: 503}) {
		t.Fatal("503 must NOT be classified as backpressure")
	}
}

func TestPostProtobufNoReplyReturnsTypedStatusError(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "txpool full", http.StatusTooManyRequests)
	}))
	defer server.Close()

	err := postProtobufNoReply(
		&SendTask{Target: strings.TrimPrefix(server.URL, "https://")},
		server.Client(),
		"doSendTxBatch",
		"/txbatch",
		[]byte("batch"),
	)
	var statusErr *httpStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("expected *httpStatusError, got %T: %v", err, err)
	}
	if statusErr.statusCode != http.StatusTooManyRequests || !isBackpressureSendError(err) || !isNonRetriableSendError(err) {
		t.Fatalf("429 classification failed: %+v", statusErr)
	}
}

func TestTxGossipBackpressureCooldown(t *testing.T) {
	b := &txGossipBatcher{}
	target := "192.0.2.10:6000"
	now := time.Unix(100, 0)

	b.recordTargetBackpressure(target, errors.New("plain failure"), now)
	if b.targetBackpressured(target, now) {
		t.Fatal("non-429 failure must not start tx gossip cooldown")
	}

	b.recordTargetBackpressure(target, &httpStatusError{statusCode: http.StatusTooManyRequests}, now)
	if !b.targetBackpressured(target, now.Add(txGossipBackpressureCooldown-time.Nanosecond)) {
		t.Fatal("429 must cool down the target")
	}
	if b.targetBackpressured(target, now.Add(txGossipBackpressureCooldown)) {
		t.Fatal("target must become eligible when cooldown expires")
	}
}

func TestIsNonRetriableSendError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain err", errors.New("plain"), false},
		{"4xx 400", &httpStatusError{statusCode: 400}, true},
		{"4xx 404", &httpStatusError{statusCode: 404}, true},
		{"5xx 500 retriable", &httpStatusError{statusCode: 500}, false},
		{"5xx 503 retriable", &httpStatusError{statusCode: 503}, false},
		{"3xx not classified", &httpStatusError{statusCode: 302}, false},
	}
	for _, tc := range cases {
		if got := isNonRetriableSendError(tc.err); got != tc.want {
			t.Errorf("%s: got=%v want=%v", tc.name, got, tc.want)
		}
	}
}

                                                              

func TestCompactFuncName(t *testing.T) {
	cases := map[string]string{
		"":                           "",
		"sender.doSendPullQuery":     "sender.doSendPullQuery",
		"dex/sender.doSendPullQuery": "sender.doSendPullQuery",
		"dex/sender/sub.helper":      "sub.helper",
	}
	for in, want := range cases {
		if got := compactFuncName(in); got != want {
			t.Errorf("compactFuncName(%q)=%q want %q", in, got, want)
		}
	}
}

func TestSendTaskFailureCallbackRunsOnce(t *testing.T) {
	failures := 0
	completions := 0
	var gotErr error
	task := &SendTask{
		OnFailure: func(err error) {
			failures++
			gotErr = err
		},
		OnDone: func() {
			completions++
		},
	}

	wantErr := errors.New("queue rejected")
	task.fail(wantErr)
	task.fail(errors.New("late duplicate failure"))
	task.finish()

	if failures != 1 || completions != 1 {
		t.Fatalf("callbacks failures=%d completions=%d, want 1/1", failures, completions)
	}
	if !errors.Is(gotErr, wantErr) {
		t.Fatalf("failure error=%v, want %v", gotErr, wantErr)
	}
}

func TestShouldBroadcastTxOnControlPlane(t *testing.T) {
	if !shouldBroadcastTxOnControlPlane(&pb.AnyTx{Content: &pb.AnyTx_CertSignTx{CertSignTx: &pb.CertSignTx{}}}) {
		t.Fatal("CertSignTx must use control-plane broadcast")
	}
	if shouldBroadcastTxOnControlPlane(&pb.AnyTx{Content: &pb.AnyTx_Transaction{Transaction: &pb.Transaction{}}}) {
		t.Fatal("ordinary transactions must stay on data-plane gossip")
	}
	if shouldBroadcastTxOnControlPlane(nil) {
		t.Fatal("nil transaction must not use control-plane broadcast")
	}
}

                                                       

func newTestSendQueueWithLimits(control, immediate, data int) *SendQueue {
	cfg := config.DefaultConfig()
	cfg.Sender.ControlMaxInflightPerTarget = control
	cfg.Sender.ImmediateMaxInflightPerTarget = immediate
	cfg.Sender.DataMaxInflightPerTarget = data
	return &SendQueue{cfg: cfg, InflightMap: map[string]int32{}}
}

                                                                                                                         
func TestInflight_AcquireRelease(t *testing.T) {
	sq := newTestSendQueueWithLimits(2, 2, 2)
	target := "peer-1"

	if !sq.tryAcquireInflight(target, "control") || !sq.tryAcquireInflight(target, "control") {
		t.Fatal("first 2 acquires must succeed (limit=2)")
	}
	if sq.tryAcquireInflight(target, "control") {
		t.Fatal("3rd acquire must be denied at limit")
	}
	sq.releaseInflight(target, "control")
	if !sq.tryAcquireInflight(target, "control") {
		t.Fatal("after release, acquire must succeed again")
	}
	sq.releaseInflight(target, "control")
	sq.releaseInflight(target, "control")
	if _, ok := sq.InflightMap[target]; ok {
		t.Fatalf("InflightMap leak after full release: %+v", sq.InflightMap)
	}
}

                               
func TestInflight_LimitZeroIsUnbounded(t *testing.T) {
	sq := newTestSendQueueWithLimits(0, 0, 0)
	for i := 0; i < 100; i++ {
		if !sq.tryAcquireInflight("peer", "data") {
			t.Fatalf("limit=0 must be unbounded, denied at %d", i)
		}
	}
}

                                                
func TestInflight_PerQueueTypeLimit(t *testing.T) {
	sq := newTestSendQueueWithLimits(1, 2, 3)

	                    
	if !sq.tryAcquireInflight("ctrl", "control") {
		t.Fatal("ctrl acquire 1 must succeed")
	}
	if sq.tryAcquireInflight("ctrl", "control") {
		t.Fatal("ctrl acquire 2 must be denied")
	}

	                      
	for i := 0; i < 2; i++ {
		if !sq.tryAcquireInflight("imm", "immediate") {
			t.Fatalf("imm acquire %d must succeed", i+1)
		}
	}
	if sq.tryAcquireInflight("imm", "immediate") {
		t.Fatal("imm acquire 3 must be denied")
	}

	                 
	for i := 0; i < 3; i++ {
		if !sq.tryAcquireInflight("dat", "data") {
			t.Fatalf("dat acquire %d must succeed", i+1)
		}
	}
	if sq.tryAcquireInflight("dat", "data") {
		t.Fatal("dat acquire 4 must be denied")
	}
}

func TestMaxInflightPerTarget_NilSafe(t *testing.T) {
	var sq *SendQueue
	if sq.maxInflightPerTarget("control") != 0 {
		t.Fatal("nil receiver must return 0")
	}
	sq2 := &SendQueue{}           
	if sq2.maxInflightPerTarget("control") != 0 {
		t.Fatal("nil cfg must return 0")
	}
}

                                                              

func TestHttpStatusError_String(t *testing.T) {
	e := &httpStatusError{op: "POST /tx", statusCode: 503, body: "overload"}
	got := e.Error()
	for _, want := range []string{"POST /tx", "503", "overload"} {
		if !strings.Contains(got, want) {
			t.Errorf("Error()=%q missing %q", got, want)
		}
	}
}

                                                                                               
                                             
func TestHttpStatusError_ErrorsAsUnwrap(t *testing.T) {
	wrapped := &wrappedErr{inner: &httpStatusError{statusCode: 429}}
	if !isBackpressureSendError(wrapped) {
		t.Fatal("errors.As must traverse wrap and classify 429 as backpressure")
	}
}

type wrappedErr struct{ inner error }

func (w *wrappedErr) Error() string { return "wrapped: " + w.inner.Error() }
func (w *wrappedErr) Unwrap() error { return w.inner }

                                                             

func TestPublicKeyToPEM(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	out := PublicKeyToPEM(&priv.PublicKey)
	if !strings.Contains(out, "BEGIN PUBLIC KEY") || !strings.Contains(out, "END PUBLIC KEY") {
		t.Fatalf("PEM missing markers:\n%s", out)
	}
}
