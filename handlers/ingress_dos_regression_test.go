package handlers

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dex/config"
	"dex/consensus"
	"dex/pb"
	"dex/utils"

	"github.com/quic-go/quic-go/http3"
	"google.golang.org/protobuf/proto"
)

type ingressSlowBody struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *ingressSlowBody) Read([]byte) (int, error) {
	b.once.Do(func() { close(b.started) })
	<-b.release
	return 0, io.EOF
}
func (b *ingressSlowBody) Close() error { return nil }

func signedIngressPull(t *testing.T, pg *PeerGate, ip string) []byte {
	t.Helper()
	consensus.ClearNodePublicKeys()
	t.Cleanup(func() {
		consensus.ClearNodePublicKeys()
		consensus.SetNodePublicKeyResolver(nil)
	})
	key := utils.NewKeyManager()
	if err := key.InitKeyRandom(); err != nil {
		t.Fatal(err)
	}
	pg.SetDeps(nil, &peerGateTestDB{accounts: map[string]*pb.Account{
		"honest": {Address: "honest", IsMiner: true, Ip: ip + ":6000", PublicKeys: &pb.PublicKeys{Keys: map[int32][]byte{
			int32(pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1_DEX): key.PublicKeyBytes(),
		}}},
	}})
	msg := &pb.PullQuery{Address: "honest", Responder: "local", ChainId: 1, RequestId: 1,
		BlockId: "block-1", RequestedHeight: 1, VrfSeed: []byte("seed"), SeqId: 1,
		Nonce: bytes.Repeat([]byte{1}, consensus.SnowmanQueryNonceSize)}
	var err error
	msg.Signature, err = key.Sign(consensus.ComputePullQueryDigest(msg))
	if err != nil {
		t.Fatal(err)
	}
	body, err := proto.Marshal(msg)
	if err != nil || !pg.verifyAndPromote(ip, "/pullquery", body) {
		t.Fatalf("could not establish verified source: %v", err)
	}
	return body
}

func TestIngressSlowAnonymousBodyCannotOccupyConsensusReserve(t *testing.T) {
	cfg := config.DefaultPeerGateConfig()
	cfg.MaxInflight, cfg.ConsensusReserve = 2, 1
	hm := newTrafficGuardTestManager(t, cfg)
	valid := signedIngressPull(t, hm.peerGate, "192.0.2.99")
	mux := http.NewServeMux()
	hm.registeredRoutes["/pullquery"] = struct{}{}
	mux.HandleFunc("/pullquery", hm.boundRequestBody("/pullquery", hm.peerGate.Middleware("/pullquery", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))
	handler := hm.WrapTraffic(mux, mux)
	body := &ingressSlowBody{started: make(chan struct{}), release: make(chan struct{})}
	done := make(chan struct{})
	defer func() { close(body.release); <-done }()
	request := httptest.NewRequest(http.MethodPost, "/pullquery", body)
	request.RemoteAddr = "192.0.2.1:1000"
	go func() { handler.ServeHTTP(httptest.NewRecorder(), request); close(done) }()
	select {
	case <-body.started:
	case <-time.After(time.Second):
		t.Fatal("anonymous request did not begin reading")
	}
	if got := serveTrafficRequest(handler, "/pullquery", "192.0.2.2:1000").Code; got != http.StatusServiceUnavailable {
		t.Fatalf("second anonymous request = %d, want bounded rejection", got)
	}
	request = httptest.NewRequest(http.MethodPost, "/pullquery", bytes.NewReader(valid))
	request.RemoteAddr = "192.0.2.99:1000"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("verified signed query rejected during anonymous saturation: %d", recorder.Code)
	}
}

type ingressCountingBody struct {
	io.Reader
	reads int
}

func (b *ingressCountingBody) Read(dst []byte) (int, error) { b.reads++; return b.Reader.Read(dst) }
func (*ingressCountingBody) Close() error                   { return nil }

func TestIngressMalformedConsensusSpendsBudgetBeforeReading(t *testing.T) {
	cfg := config.DefaultPeerGateConfig()
	cfg.PathRateLimits = map[string]float64{"/pullquery": 1}
	hm := newTrafficGuardTestManager(t, cfg)
	valid := signedIngressPull(t, hm.peerGate, "192.0.2.99")
	handler := hm.peerGate.Middleware("/pullquery", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	for i := 0; i < 2; i++ {
		body := &ingressCountingBody{Reader: bytes.NewReader([]byte{0xff})}
		request := httptest.NewRequest(http.MethodPost, "/pullquery", body)
		request.RemoteAddr = fmt.Sprintf("192.0.2.%d:1000", i+1)
		recorder := httptest.NewRecorder()
		handler(recorder, request)
		if i == 0 && (recorder.Code != http.StatusForbidden || body.reads == 0) {
			t.Fatalf("first malformed request = %d, reads=%d", recorder.Code, body.reads)
		}
		if i == 1 && (recorder.Code != http.StatusTooManyRequests || body.reads != 0) {
			t.Fatalf("exhausted request = %d, reads=%d; must reject before body work", recorder.Code, body.reads)
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/pullquery", bytes.NewReader(valid))
	request.RemoteAddr = "192.0.2.99:1000"
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("anonymous path tokens starved verified source: %d", recorder.Code)
	}
}

func TestIngressUnknownGlobalBudgetCannotRotateRoutes(t *testing.T) {
	cfg := config.DefaultPeerGateConfig()
	cfg.DefaultPathRPS, cfg.DefaultPathBurst = 0.001, 1
	hm := newTrafficGuardTestManager(t, cfg)
	for i, path := range []string{"/status", "/heightquery"} {
		handler := hm.peerGate.Middleware(path, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
		got := serveTrafficRequest(handler, path, fmt.Sprintf("192.0.2.%d:1000", i+1)).Code
		want := http.StatusNoContent
		if i > 0 {
			want = http.StatusTooManyRequests
		}
		if got != want {
			t.Fatalf("%s = %d, want %d", path, got, want)
		}
	}
}

func TestIngressBodyPolicyBoundsChunkedAndPreservesLargePayloads(t *testing.T) {
	hm := newTrafficGuardTestManager(t, config.DefaultPeerGateConfig())
	hm.cfg.Server.MaxRequestBodySize = 128 << 10
	for _, path := range []string{"/pullquery", "/chits", "/pushquery", "/frostmsg", "/txbatch", "/batchgetdata", "/service/syncblocks"} {
		t.Run(path, func(t *testing.T) {
			handler := hm.boundRequestBody(path, func(w http.ResponseWriter, r *http.Request) {
				data, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, "too large", http.StatusRequestEntityTooLarge)
					return
				}
				if len(data) != 96<<10 {
					t.Errorf("unexpected payload length %d", len(data))
				}
				w.WriteHeader(http.StatusNoContent)
			})
			for _, contentLength := range []int64{-1, 96 << 10} {
				request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(make([]byte, 96<<10)))
				request.ContentLength = contentLength
				recorder := httptest.NewRecorder()
				handler(recorder, request)
				want := http.StatusRequestEntityTooLarge
				if largeRequestBody(path) || path == "/service/syncblocks" {
					want = http.StatusNoContent
				}
				if recorder.Code != want {
					t.Fatalf("contentLength=%d status=%d, want=%d", contentLength, recorder.Code, want)
				}
			}
		})
	}
}

func TestIngressLargeBodiesHaveSeparateBoundedSourcePools(t *testing.T) {
	cfg := config.DefaultPeerGateConfig()
	cfg.TxBatchMaxInflight = 2
	hm := newTrafficGuardTestManager(t, cfg)
	pg := hm.peerGate
	for _, path := range []string{"/tx", "/txbatch"} {
		release, ok := pg.acquireInflight(path, false)
		if !ok {
			t.Fatalf("could not acquire %s", path)
		}
		defer release()
	}
	if _, ok := pg.acquireInflight("/pushquery", false); ok {
		t.Fatal("anonymous bulk requests bypassed shared memory budget")
	}
	for _, path := range []string{"/pushquery", "/chits", "/heightquery", "/acceptedfrontier"} {
		release, ok := pg.acquireInflight(path, true)
		if !ok {
			t.Fatalf("anonymous bulk saturation blocked verified %s", path)
		}
		release()
	}
}

func TestIngressVerifiedSourceLookupDoesNotRenewLease(t *testing.T) {
	pg := newTestPeerGate(t)
	pg.promote("192.0.2.1", "miner")
	entry := pg.trustedIPs["192.0.2.1"]
	last := atomic.LoadInt64(&entry.LastSeen)
	if !pg.isVerifiedSource("192.0.2.1") || atomic.LoadInt64(&entry.LastSeen) != last || atomic.LoadUint64(&entry.HitCount) != 0 {
		t.Fatal("resource lookup renewed a verified source lease")
	}
	atomic.StoreInt64(&entry.LastSeen, time.Now().Add(-2*pg.trustedTTL).Unix())
	if pg.isVerifiedSource("192.0.2.1") {
		t.Fatal("expired source retained reserve before periodic cleanup")
	}
}

func TestIngressUnboundSignedMessagesCannotPromoteReplaySource(t *testing.T) {
	for _, endpoint := range []string{"", "miner.example.test:6000"} {
		t.Run(endpoint, func(t *testing.T) {
			cfg := config.DefaultPeerGateConfig()
			cfg.MaxInflight, cfg.ConsensusReserve = 2, 1
			hm := newTrafficGuardTestManager(t, cfg)
			pg := hm.peerGate
			signed := signedIngressPull(t, pg, "192.0.2.99")
			pg.dbManager.(*peerGateTestDB).accounts["honest"].Ip = endpoint
			pg.trustedIPs = make(map[string]*TrustedEntry)
			handler := pg.Middleware("/pullquery", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
			for _, ip := range []string{"192.0.2.1", "192.0.2.2"} {
				request := httptest.NewRequest(http.MethodPost, "/pullquery", bytes.NewReader(signed))
				request.RemoteAddr = ip + ":1000"
				recorder := httptest.NewRecorder()
				handler(recorder, request)
				if recorder.Code != http.StatusNoContent {
					t.Fatalf("valid legacy message should retain bounded admission: %d", recorder.Code)
				}
				if hm.IsVerifiedPeerIP(ip) || pg.isTrusted(ip) {
					t.Fatal("unbound signed replay promoted a source IP")
				}
			}
			release, ok := pg.acquireInflight("/status", false)
			if !ok {
				t.Fatal("could not occupy ordinary budget")
			}
			defer release()
			if got := serveTrafficRequest(handler, "/pullquery", "192.0.2.1:1000").Code; got != http.StatusServiceUnavailable {
				t.Fatalf("unbound replay obtained reserve: %d", got)
			}
		})
	}
}

func TestIngressLoopbackSourceCannotPromoteAnotherHeaderIdentity(t *testing.T) {
	pg := newTestPeerGate(t)
	pg.enforceLoopback = true
	if allowed, bound := pg.sourceIPAdmission("alice", "loopback:alice"); !allowed || !bound {
		t.Fatal("signed matching local identity lost its source binding")
	}
	if allowed, bound := pg.sourceIPAdmission("alice", "loopback:bob"); !allowed || bound {
		t.Fatal("self-declared header bound a different local identity")
	}
}

func TestIngressAnonymousLimiterMetadataCannotExhaustVerifiedBudget(t *testing.T) {
	cfg := config.DefaultPeerGateConfig()
	cfg.MaxTrackedIPs = 1
	hm := newTrafficGuardTestManager(t, cfg)
	pg := hm.peerGate
	pg.allowUnknown("192.0.2.1")
	pg.allowUnknown("192.0.2.2")
	if limiter := pg.getBoundedLimiter(&pg.trustedLimiters, pg.trustedOverflow, "192.0.2.99", 100, 1); limiter == pg.trustedOverflow {
		t.Fatal("anonymous metadata saturation forced verified miner into shared overflow")
	}
	if pg.limiterEntries != 1 || pg.priorityLimiterEntries != 1 {
		t.Fatal("source metadata classes are not independently bounded")
	}
	pg.cleanup()
	if pg.priorityLimiterEntries != 0 {
		t.Fatal("verified limiter metadata not reclaimed")
	}
}

type ingressWriterWrapper struct{ http.ResponseWriter }

func (w ingressWriterWrapper) Unwrap() http.ResponseWriter { return w.ResponseWriter }

type ingressReadSignal struct {
	io.ReadCloser
	started chan struct{}
	once    sync.Once
}

func (b *ingressReadSignal) Read(dst []byte) (int, error) {
	b.once.Do(func() { close(b.started) })
	return b.ReadCloser.Read(dst)
}

                                                                                
                                                                                
func TestIngressReadDeadlineInterruptsTCPAndHTTP3(t *testing.T) {
	for _, test := range []struct {
		transport string
		oversized bool
	}{{"tcp", false}, {"http3", false}, {"tcp", true}, {"http3", true}} {
		t.Run(fmt.Sprintf("%s/oversized=%t", test.transport, test.oversized), func(t *testing.T) {
			cfg := config.DefaultPeerGateConfig()
			cfg.EnforceLoopback = true
			cfg.UnknownRPS, cfg.UnknownBurst = 100, 100
			cfg.MaxInflight, cfg.ConsensusReserve = 2, 1
			hm := newTrafficGuardTestManager(t, cfg)
			hm.cfg.Server.HTTPTimeout = 200 * time.Millisecond
			started, finished := make(chan struct{}), make(chan struct{})
			mux := http.NewServeMux()
			for _, path := range []string{"/pullquery", "/statedb/checkpoint/chunk", "/txbatch"} {
				path := path
				hm.registeredRoutes[path] = struct{}{}
				mux.HandleFunc(path, hm.boundRequestBody(path, hm.peerGate.Middleware(path, func(w http.ResponseWriter, r *http.Request) {
					if _, err := io.Copy(io.Discard, r.Body); err != nil {
						http.Error(w, "read failed", http.StatusBadRequest)
						return
					}
					w.WriteHeader(http.StatusNoContent)
				})))
			}
			bounded := hm.WrapTraffic(mux, mux)
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Test-Slow") == "1" {
					r.Body = &ingressReadSignal{ReadCloser: r.Body, started: started}
					defer close(finished)
				}
				bounded.ServeHTTP(ingressWriterWrapper{w}, r)
			})
			var client *http.Client
			var baseURL string
			if test.transport == "tcp" {
				server := httptest.NewServer(handler)
				t.Cleanup(server.Close)
				baseURL, client = server.URL, server.Client()
			} else {
				certServer := httptest.NewTLSServer(http.NotFoundHandler())
				tlsConfig := certServer.TLS.Clone()
				certServer.Close()
				conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				server := &http3.Server{TLSConfig: tlsConfig, Handler: handler}
				serveDone := make(chan struct{})
				go func() { _ = server.Serve(conn); close(serveDone) }()
				t.Cleanup(func() { _ = server.Close(); _ = conn.Close(); <-serveDone })
				roundTripper := &http3.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
				t.Cleanup(func() { _ = roundTripper.Close() })
				client = &http.Client{Transport: roundTripper}
				baseURL = "https://" + conn.LocalAddr().String()
			}
			client.Timeout = 3 * time.Second
			reader, writer := io.Pipe()
			defer reader.Close()
			defer writer.Close()
			request, err := http.NewRequest(http.MethodPost, baseURL+"/pullquery", reader)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("X-Test-Slow", "1")
			if test.oversized {
				request.ContentLength = 96 << 10
			}
			clientDone := make(chan struct{})
			go func() {
				if response, err := client.Do(request); err == nil {
					response.Body.Close()
				}
				close(clientDone)
			}()
			if !test.oversized {
				select {
				case <-started:
				case <-time.After(time.Second):
					t.Fatal("transport did not start reading stalled body")
				}
			}
			select {
			case <-finished:
			case <-time.After(1500 * time.Millisecond):
				t.Fatal("transport body read outlived request deadline")
			}
			_ = writer.Close()
			<-clientDone
			if len(hm.peerGate.totalInflight) != 0 || len(hm.peerGate.unverifiedInflight) != 0 {
				t.Fatal("slow request retained admission capacity after timeout")
			}
			for _, path := range []string{"/statedb/checkpoint/chunk", "/txbatch"} {
				payload := []byte(`{}`)
				if path == "/txbatch" {
					payload = make([]byte, 96<<10)
				}
				response, err := client.Post(baseURL+path, "application/octet-stream", bytes.NewReader(payload))
				if err != nil {
					t.Fatalf("healthy %s request failed after timeout: %v", path, err)
				}
				response.Body.Close()
				if response.StatusCode != http.StatusNoContent {
					t.Fatalf("healthy %s status=%d", path, response.StatusCode)
				}
			}
		})
	}
}
