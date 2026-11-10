package sender

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"dex/logs"
	"github.com/quic-go/quic-go/http3"
)

func TestPushHTTPTraceGate(t *testing.T) {
	t.Setenv("DEX_CONSENSUS_TRACE", "0")
	if err := os.Unsetenv("DEX_CONSENSUS_TRACE"); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, "https://peer.invalid/pushquery", nil)
	if err != nil {
		t.Fatal(err)
	}
	push := &SendTask{TraceKind: "push"}
	if got, trace := attachPushHTTPTrace(push, request); got != request || trace != nil || httptrace.ContextClientTrace(got.Context()) != nil {
		t.Fatal("default-disabled diagnostics attached an HTTP trace")
	}
	t.Setenv("DEX_CONSENSUS_TRACE", "0")
	if got, trace := attachPushHTTPTrace(push, request); got != request || trace != nil {
		t.Fatal("explicitly disabled diagnostics attached an HTTP trace")
	}
	t.Setenv("DEX_CONSENSUS_TRACE", "1")
	for _, task := range []*SendTask{nil, {}, {TraceKind: "pull"}, {TraceKind: "chit"}, {TraceKind: "tx"}} {
		if got, trace := attachPushHTTPTrace(task, request); got != request || trace != nil || httptrace.ContextClientTrace(got.Context()) != nil {
			t.Fatal("non-Push request acquired a diagnostic trace")
		}
	}
	got, trace := attachPushHTTPTrace(push, request)
	if got == request || trace == nil || httptrace.ContextClientTrace(got.Context()) == nil || httptrace.ContextClientTrace(request.Context()) != nil {
		t.Fatal("enabled Push must gain its own context without mutating the original request")
	}
	var absent *pushHTTPTrace
	absent.gotResponse(nil)
	absent.finish(nil, 0, nil)
}

func TestPushHTTPTraceCallbacksAndOffsets(t *testing.T) {
	t.Setenv("DEX_CONSENSUS_TRACE", "1")
	req, _ := http.NewRequest(http.MethodPost, "https://peer.invalid/pushquery", nil)
	req, trace := attachPushHTTPTrace(&SendTask{TraceKind: "push"}, req)
	s := trace.snapshot()
	if s.gotConn != -1 || s.wroteHeaders != -1 || s.wroteRequest != -1 || s.firstByte != -1 || s.response != -1 || s.uploadResult != "unknown" || s.status != 0 {
		t.Fatalf("missing callbacks must be explicit, not zero elapsed: %+v", s)
	}
	hooks := httptrace.ContextClientTrace(req.Context())
	hooks.GotConn(httptrace.GotConnInfo{Reused: true})
	hooks.WroteHeaders()
	hooks.WroteRequest(httptrace.WroteRequestInfo{})
	hooks.GotFirstResponseByte()
	first := trace.snapshot().firstByte
	hooks.GotFirstResponseByte()
	trace.gotResponse(nil)
	trace.gotResponse(&http.Response{StatusCode: http.StatusAccepted})
	s = trace.snapshot()
	if s.gotConn < 0 || s.wroteHeaders < s.gotConn || s.wroteRequest < s.wroteHeaders || s.firstByte < s.wroteRequest || s.response < s.firstByte || s.firstByte != first || !s.reused || s.uploadResult != "ok" || s.status != http.StatusAccepted {
		t.Fatalf("callback offsets/status were not recorded: %+v", s)
	}
	for _, tc := range []struct {
		err  error
		want string
	}{{context.DeadlineExceeded, "timeout"}, {errors.New("PRIVATE_UPLOAD_ERROR"), "error"}, {&httpStatusError{statusCode: 503, body: "PRIVATE_BODY"}, "http_503"}} {
		hooks.WroteRequest(httptrace.WroteRequestInfo{Err: tc.err})
		if got := trace.snapshot().uploadResult; got != tc.want {
			t.Fatalf("upload result=%q, want %q", got, tc.want)
		}
	}
	for _, tc := range []struct {
		d    time.Duration
		want float64
	}{{-time.Second, -1}, {0, 0}, {1500 * time.Microsecond, 1.5}} {
		if got := pushTraceOffsetMS(tc.d); got != tc.want {
			t.Fatalf("offset(%v)=%v, want %v", tc.d, got, tc.want)
		}
	}
}

func TestPushHTTPTraceConcurrentCallbacksSnapshotAndFinish(t *testing.T) {
	t.Setenv("DEX_CONSENSUS_TRACE", "1")
	task := &SendTask{TraceKind: "push", TraceRequestID: 16, Target: "peer.invalid", NodeAddress: "test-node"}
	req, _ := http.NewRequest(http.MethodPost, "https://peer.invalid/pushquery", nil)
	req, trace := attachPushHTTPTrace(task, req)
	hooks := httptrace.ContextClientTrace(req.Context())
	start := make(chan struct{})
	var wg sync.WaitGroup
	callbacks := []func(){
		func() { hooks.GotConn(httptrace.GotConnInfo{Reused: true}) },
		hooks.WroteHeaders,
		func() { hooks.WroteRequest(httptrace.WroteRequestInfo{}) },
		hooks.GotFirstResponseByte,
		func() { trace.gotResponse(&http.Response{StatusCode: http.StatusOK}) },
		func() { _ = trace.snapshot() },
	}
	for _, callback := range callbacks {
		wg.Add(1)
		go func(callback func()) {
			defer wg.Done()
			<-start
			for i := 0; i < 100; i++ {
				callback()
			}
		}(callback)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 8; i++ {
			trace.finish(task, 32, nil)
		}
	}()
	close(start)
	wg.Wait()
	                                                                           
	trace.finish(task, 32, context.DeadlineExceeded)
	hooks.WroteRequest(httptrace.WroteRequestInfo{Err: context.DeadlineExceeded})
	s := trace.snapshot()
	if s.gotConn < 0 || s.wroteHeaders < 0 || s.wroteRequest < 0 || s.firstByte < 0 || s.response < 0 || !s.reused || s.status != http.StatusOK || s.uploadResult != "timeout" {
		t.Fatalf("concurrent callback state lost updates: %+v", s)
	}
}

func TestPushHTTPTraceSafeLogFields(t *testing.T) {
	t.Setenv("DEX_CONSENSUS_TRACE", "1")
	logger := logs.NewNodeLogger(t.Name(), 20)
	defer logs.SetThreadLogger(logger)()
	secret := "PRIVATE_BODY_NONCE_AND_ERROR_MUST_NOT_APPEAR"
	task := &SendTask{TraceKind: "push", TraceRequestID: 32, TraceHeight: 77, NodeAddress: "node-A", Target: "peer:19000", Message: []byte(secret)}
	req, _ := http.NewRequest(http.MethodPost, "https://peer.invalid/pushquery", strings.NewReader(secret))
	req, trace := attachPushHTTPTrace(task, req)
	httptrace.ContextClientTrace(req.Context()).WroteRequest(httptrace.WroteRequestInfo{Err: errors.New(secret)})
	trace.gotResponse(&http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader(secret))})
	trace.finish(task, len(secret), &httpStatusError{statusCode: 503, body: secret})
	lines := logger.GetBuffer()
	if len(lines) != 1 {
		t.Fatalf("want one bounded trace line, got %d", len(lines))
	}
	line := lines[0].Message
	for _, want := range []string{"event=push_http", `node="node-A"`, `target="peer:19000"`, "request=32", "height=77", "bytes=" + strconv.Itoa(len(secret)), "got_conn_at_ms=-1.000", "wrote_headers_at_ms=-1.000", "first_byte_at_ms=-1.000", "upload_result=error", "status=503", "result=http_503"} {
		if !strings.Contains(line, want) {
			t.Errorf("missing %q in %s", want, line)
		}
	}
	if strings.Contains(line, secret) || strings.Contains(line, "https://peer.invalid") || strings.ContainsAny(line, "\r\n") {
		t.Fatalf("trace leaked unbounded request/response/error content: %s", line)
	}
}

func TestPushHTTPTraceRealSlowResponse(t *testing.T) {
	t.Setenv("DEX_CONSENSUS_TRACE", "1")
	for _, protocol := range []string{"TLS", "HTTP3"} {
		t.Run(protocol, func(t *testing.T) {
			logger := logs.NewNodeLogger(t.Name(), 20)
			defer logs.SetThreadLogger(logger)()
			// Keep the body open independently of transport cancellation.
			releaseBody := make(chan struct{})
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				<-releaseBody
			})
			var client *http.Client
			var target string
			if protocol == "TLS" {
				server := httptest.NewTLSServer(handler)
				defer server.Close()
				client, target = server.Client(), strings.TrimPrefix(server.URL, "https://")
			} else {
				certServer := httptest.NewTLSServer(handler)
				tlsConfig := certServer.TLS.Clone()
				certServer.Close()
				conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				server := &http3.Server{TLSConfig: tlsConfig, Handler: handler}
				done := make(chan struct{})
				go func() { _ = server.Serve(conn); close(done) }()
				defer func() {
					_ = server.Close()
					select {
					case <-done:
					case <-time.After(2 * time.Second):
						t.Error("HTTP/3 test server did not stop")
					}
				}()
				transport := &http3.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, DisableCompression: true}
				defer transport.Close()
				client, target = &http.Client{Transport: transport}, conn.LocalAddr().String()
			}
			defer close(releaseBody)
			payload := []byte("PRIVATE_PUSH_PAYLOAD_AND_NONCE")
			task := &SendTask{Target: target, NodeAddress: "test-node", TraceKind: "push", TraceRequestID: 16, TraceHeight: 88, Timeout: 300 * time.Millisecond, Message: &pushQueryMsg{requestData: payload}}
			started := time.Now()
			err := doSendPushQuery(task, client)
			if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 3*time.Second {
				t.Fatalf("slow body did not retain bounded task deadline: %v", err)
			}
			var line string
			for _, entry := range logger.GetBuffer() {
				if strings.Contains(entry.Message, "event=push_http") {
					if line != "" {
						t.Fatal("one request emitted duplicate finish traces")
					}
					line = entry.Message
				}
			}
			for _, want := range []string{"event=push_http", "request=16", "height=88", "upload_result=ok", "status=200", "result=timeout"} {
				if !strings.Contains(line, want) {
					t.Errorf("missing %q in %s", want, line)
				}
			}
			for _, field := range []string{"got_conn_at_ms", "wrote_headers_at_ms", "wrote_request_at_ms", "first_byte_at_ms", "response_at_ms", "total_ms"} {
				match := regexp.MustCompile(field + `=([-0-9.]+)`).FindStringSubmatch(line)
				if len(match) != 2 {
					t.Fatalf("missing %s: %s", field, line)
				}
				value, parseErr := strconv.ParseFloat(match[1], 64)
				if parseErr != nil || value < 0 || (field == "total_ms" && value < 250) {
					t.Errorf("invalid actual %s=%s in slow response trace", field, match[1])
				}
			}
			if strings.Contains(line, string(payload)) {
				t.Fatal("real transport trace leaked request payload/nonce")
			}
		})
	}
}
