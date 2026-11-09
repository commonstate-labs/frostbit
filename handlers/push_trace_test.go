package handlers

import (
	"bytes"
	"crypto/tls"
	"dex/config"
	"dex/consensus"
	"dex/logs"
	"dex/pb"
	"dex/stats"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/quic-go/quic-go/http3"
	"google.golang.org/protobuf/proto"
)

func TestPushRequestTraceOptInAndStatus(t *testing.T) {
	hm := &HandlerManager{address: "local"}
	request := httptest.NewRequest(http.MethodPost, "/pushquery", nil)
	for _, enabled := range []string{"0", "1"} {
		t.Setenv("DEX_CONSENSUS_TRACE", enabled)
		for _, path := range []string{"/pushquery", "/pullquery"} {
			recorder := httptest.NewRecorder()
			w, r, finish := hm.startPushRequestTrace(path, recorder, request)
			if enabled == "0" || path != "/pushquery" {
				if w != recorder || r != request || finish != nil {
					t.Fatal("disabled/non-Push request was wrapped")
				}
				continue
			}
			if pushTraceFromRequest(r) == nil || request.Context() == r.Context() {
				t.Fatal("Push trace missing or original request mutated")
			}
			w.WriteHeader(http.StatusServiceUnavailable)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("unchanged response"))
			finish()
			if recorder.Code != http.StatusServiceUnavailable || recorder.Body.String() != "unchanged response" || w.(*pushTraceResponseWriter).status != recorder.Code {
				t.Fatal("trace changed response status/body")
			}
		}
	}
	for _, explicit := range []bool{false, true} {
		rr := httptest.NewRecorder()
		w := &pushTraceResponseWriter{ResponseWriter: rr}
		if explicit {
			w.WriteHeader(http.StatusOK)
		}
		_, _ = w.Write([]byte("ok"))
		if w.status != http.StatusOK || rr.Code != http.StatusOK {
			t.Fatal("implicit/explicit success changed")
		}
	}
}

type pushTraceSlowDB struct{ peerGateTestDB }

func (db *pushTraceSlowDB) GetAccount(address string) (*pb.Account, error) {
	time.Sleep(10 * time.Millisecond)
	return db.peerGateTestDB.GetAccount(address)
}

func TestPushRequestTraceGateSourceAndHandlerVerification(t *testing.T) {
	t.Setenv("DEX_CONSENSUS_TRACE", "1")
	consensus.ClearNodePublicKeys()
	consensus.SetNodePublicKeyResolver(nil)
	t.Cleanup(func() { consensus.ClearNodePublicKeys(); consensus.SetNodePublicKeyResolver(nil) })
	key := newConsensusQueryTestKey(t)
	consensus.RegisterNodePublicKey("alice", key.PublicKeyBytes())
	pg := newTestPeerGate(t)
	pg.SetDeps(nil, &pushTraceSlowDB{peerGateTestDB{accounts: map[string]*pb.Account{
		"alice": {Address: "alice", IsMiner: true, Ip: "192.0.2.1:19000"},
	}}})
	hm := &HandlerManager{address: "bob", cfg: config.DefaultConfig(), peerGate: pg, Stats: stats.NewStats(), Logger: logs.NewNodeLogger("push-test", 0)}
	msg := &pb.PushQuery{Address: "alice", Responder: "bob", ChainId: pb.GetDefaultChainID(), RequestId: 16, RequestedHeight: 5,
		Nonce: bytes.Repeat([]byte{1}, consensus.SnowmanQueryNonceSize), Container: []byte("private-container")}
	msg.Signature, _ = key.Sign(consensus.ComputePushQueryDigest(msg))
	wire, err := proto.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	var got *pushRequestTrace
	handler := hm.boundRequestBody("/pushquery", pg.Middleware("/pushquery", func(w http.ResponseWriter, r *http.Request) {
		got = pushTraceFromRequest(r)
		if got == nil || got.verified || got.peer != "unverified" {
			t.Fatal("PeerGate signature alone incorrectly labeled full query verified")
		}
		hm.HandlePushQuery(w, r)
	}))
	r := httptest.NewRequest(http.MethodPost, "/pushquery", bytes.NewReader(wire))
	r.RemoteAddr = "192.0.2.1:32000"
	rr := httptest.NewRecorder()
	handler(rr, r)
	if rr.Code != http.StatusServiceUnavailable || got == nil || !got.verified || got.peer != "alice" || got.stage != "consensus_unavailable" {
		t.Fatalf("handler verification/exit not tracked: status=%d trace=%+v", rr.Code, got)
	}
	if got.bodyBytes != len(wire) || got.containerBytes != len(msg.Container) || got.phases[pushBody] < 0 || got.phases[pushDecode] < 0 || got.phases[pushVerify] < 0 {
		t.Fatalf("body/decode/verify phase missing: %+v", got)
	}
	if got.phases[pushSourceGate] < 10*time.Millisecond || got.phases[pushGate] < got.phases[pushSourceGate] || got.phases[pushAccepted] != -1 || got.phases[pushEnqueue] != -1 {
		t.Fatalf("nested source timing or unreached phase misleading: %+v", got.phases)
	}
}

func TestPushRequestTraceSafeFieldsSamplingAndMissingPhases(t *testing.T) {
	t.Setenv("DEX_CONSENSUS_TRACE", "1")
	logger := logs.NewNodeLogger(t.Name(), 20)
	defer logs.SetThreadLogger(logger)()
	trace := newPushRequestTrace("local", "[::ffff:192.0.2.1]:90")
	trace.query(&pb.PushQuery{RequestId: 1, RequestedHeight: 20, Address: strings.Repeat("untrusted-secret", 1000), Container: []byte("private-body"), Nonce: []byte("private-nonce"), Signature: []byte("private-signature")}, false)
	trace.finish(http.StatusOK)
	if len(logger.GetBuffer()) != 0 {
		t.Fatal("fast unsampled success emitted")
	}
	trace.finish(http.StatusForbidden)
	lines := logger.GetBuffer()
	if len(lines) != 1 {
		t.Fatalf("failed request must emit once: %d", len(lines))
	}
	for _, want := range []string{"event=push_rx", "query_fields=unverified", "peer=\"unverified\"", "source_ip=192.0.2.1", "accepted_ms=-1.000", "request=1", "height=20", "status=403"} {
		if !strings.Contains(lines[0].Message, want) {
			t.Errorf("missing %q in %s", want, lines[0].Message)
		}
	}
	if strings.Contains(lines[0].Message, "private-") || strings.Contains(lines[0].Message, "untrusted-secret") {
		t.Fatal("unverified identity or signed payload leaked")
	}
	trace.started = time.Now().Add(-250 * time.Millisecond)
	trace.finish(http.StatusOK)
	if len(logger.GetBuffer()) != 2 {
		t.Fatal("slow success not emitted")
	}
	if newPushRequestTrace("local", "[fe80::1%private-zone]:90").sourceIP != "fe80::1" {
		t.Fatal("source IP retained an untrusted zone string")
	}
}

func TestPushRequestTraceReadDeadlineTCPAndHTTP3(t *testing.T) {
	t.Setenv("DEX_CONSENSUS_TRACE", "1")
	for _, protocol := range []string{"tcp", "http3"} {
		t.Run(protocol, func(t *testing.T) {
			hm := &HandlerManager{address: "local", cfg: config.DefaultConfig()}
			hm.cfg.Server.HTTPTimeout = 100 * time.Millisecond
			finished := make(chan error, 1)
			handler := http.HandlerFunc(hm.boundRequestBody("/pushquery", func(w http.ResponseWriter, r *http.Request) {
				if pushTraceFromRequest(r) == nil {
					t.Error("real transport did not exercise trace wrapper")
				}
				                                                               
				                                                                    
				if err := http.NewResponseController(w).SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
					finished <- err
					return
				}
				_, err := decodePushQueryRequest(r)
				http.Error(w, "read failed", http.StatusBadRequest)
				finished <- err
			}))
			var client *http.Client
			var url string
			if protocol == "tcp" {
				server := httptest.NewServer(handler)
				t.Cleanup(server.Close)
				client, url = server.Client(), server.URL
			} else {
				certServer := httptest.NewTLSServer(http.NotFoundHandler())
				tlsConfig := certServer.TLS.Clone()
				certServer.Close()
				conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				server := &http3.Server{TLSConfig: tlsConfig, Handler: handler}
				done := make(chan struct{})
				go func() { _ = server.Serve(conn); close(done) }()
				t.Cleanup(func() { _ = server.Close(); _ = conn.Close(); <-done })
				transport := &http3.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
				t.Cleanup(func() { _ = transport.Close() })
				client, url = &http.Client{Transport: transport}, "https://"+conn.LocalAddr().String()
			}
			client.Timeout = 2 * time.Second
			reader, writer := io.Pipe()
			defer reader.Close()
			defer writer.Close()
			request, _ := http.NewRequest(http.MethodPost, url+"/pushquery", reader)
			clientDone := make(chan struct{})
			go func() {
				if response, err := client.Do(request); err == nil {
					response.Body.Close()
				}
				close(clientDone)
			}()
			select {
			case err := <-finished:
				var netErr net.Error
				if !errors.As(err, &netErr) || !netErr.Timeout() {
					t.Fatalf("expected body deadline error, got %v", err)
				}
			case <-time.After(1500 * time.Millisecond):
				t.Fatal("trace wrapper lost stream read deadline")
			}
			_ = writer.Close()
			<-clientDone
		})
	}
}
