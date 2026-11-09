package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mholt/acmez/v3/acme"
)

func ipTLSUnitOptions(t *testing.T) ipTLSOptions {
	t.Helper()
	return ipTLSOptions{Enabled: true, IP: "203.0.113.77", ListenAddr: "127.0.0.1:0", CacheDir: t.TempDir(), Logf: func(string, ...any) {}}
}

func TestIPTLSDiscoveryRejectsNonPublicInterfaceRanges(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "10.0.0.1", "172.16.0.1", "192.168.1.2", "169.254.0.1", "100.64.1.2", "100.127.255.254", "198.18.0.1", "198.19.255.254", "192.0.2.1", "198.51.100.1", "203.0.113.1", "240.1.2.3", "2001:db8::1", "2001:2::1", "3fff::1", "64:ff9b::c000:201"} {
		if isDiscoverableExplorerIP(netip.MustParseAddr(address)) {
			t.Errorf("automatically selected non-public interface address %s", address)
		}
	}
	for _, address := range []string{"52.128.244.130", "216.118.251.6", "2606:4700::1111"} {
		if !isDiscoverableExplorerIP(netip.MustParseAddr(address)) {
			t.Errorf("public address rejected: %s", address)
		}
	}
	                                                                           
	                                                                           
	if _, err := newIPTLSManager(ipTLSUnitOptions(t)); err != nil {
		t.Fatal(err)
	}
}

func TestIPTLSOptionsAndPrivateCacheBoundaries(t *testing.T) {
	if manager, err := newIPTLSManager(ipTLSOptions{}); err != nil || manager != nil {
		t.Fatalf("disabled manager=%v err=%v", manager, err)
	}
	for name, change := range map[string]func(*ipTLSOptions){
		"private-ip":     func(o *ipTLSOptions) { o.IP = "192.168.1.2" },
		"non-ip":         func(o *ipTLSOptions) { o.IP = "example.com" },
		"bad-listen":     func(o *ipTLSOptions) { o.ListenAddr = "hostname:8445" },
		"http-ca":        func(o *ipTLSOptions) { o.DirectoryURL = "http://example.com/directory" },
		"ca-credentials": func(o *ipTLSOptions) { o.DirectoryURL = "https://user:password@ca.example/directory" },
	} {
		t.Run(name, func(t *testing.T) {
			options := ipTLSUnitOptions(t)
			change(&options)
			if _, err := newIPTLSManager(options); err == nil {
				t.Fatal("invalid options accepted")
			}
		})
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "private.pem")
	if err := atomicIPTLSPrivateFile(path, []byte("old")); err != nil {
		t.Fatal(err)
	}
	if err := atomicIPTLSPrivateFile(path, []byte("replacement")); err != nil {
		t.Fatal(err)
	}
	if value, err := readIPTLSPrivateFile(path); err != nil || string(value) != "replacement" {
		t.Fatalf("atomic replacement=%q err=%v", value, err)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(path)
		if info.Mode().Perm() != 0600 {
			t.Fatalf("mode=%o", info.Mode().Perm())
		}
	}
	if err := atomicIPTLSPrivateFile(path, make([]byte, ipTLSMaxCacheBytes+1)); err == nil {
		t.Fatal("oversized write accepted")
	}
	if data, _ := os.ReadFile(path); string(data) != "replacement" {
		t.Fatal("failed write corrupted old cache")
	}
	if err := os.Mkdir(filepath.Join(dir, "directory.pem"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := readIPTLSPrivateFile(filepath.Join(dir, "directory.pem")); err == nil {
		t.Fatal("cache directory accepted as key")
	}
}

func TestIPTLSHTTP01UsesOnlyActiveTokenAndConfiguredIP(t *testing.T) {
	manager, err := newIPTLSManager(ipTLSUnitOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	solver := ipTLSChallengeSolver{manager: manager}
	challenge := acme.Challenge{Type: acme.ChallengeTypeHTTP01, Identifier: acme.Identifier{Type: "ip", Value: manager.ip}, Token: "unit_token-123", KeyAuthorization: "unit_token-123.account-thumbprint"}
	if err := solver.Present(context.Background(), challenge); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, host, path string
		status             int
	}{
		{"GET", manager.ip, ipTLSChallengePrefix + challenge.Token, 200},
		{"HEAD", manager.ip + ":80", ipTLSChallengePrefix + challenge.Token, 200},
		{"POST", manager.ip, ipTLSChallengePrefix + challenge.Token, 405},
		{"GET", "other.example", ipTLSChallengePrefix + challenge.Token, 404},
		{"GET", "203.0.113.78", ipTLSChallengePrefix + challenge.Token, 404},
		{"GET", manager.ip, ipTLSChallengePrefix + "unknown", 404},
		{"GET", manager.ip, ipTLSChallengePrefix + strings.Repeat("a", 257), 404},
	} {
		r := httptest.NewRequest(tc.method, "http://"+tc.host+tc.path, nil)
		rr := httptest.NewRecorder()
		manager.ChallengeHandler().ServeHTTP(rr, r)
		if rr.Code != tc.status {
			t.Errorf("%s %s %s status=%d want=%d", tc.method, tc.host, tc.path, rr.Code, tc.status)
		}
		if tc.status == 200 && tc.method == "GET" && rr.Body.String() != challenge.KeyAuthorization {
			t.Fatal("incorrect key authorization")
		}
		if tc.method == "HEAD" && rr.Body.Len() != 0 {
			t.Fatal("HEAD returned a body")
		}
	}
	if err := solver.CleanUp(context.Background(), challenge); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "http://"+manager.ip+ipTLSChallengePrefix+challenge.Token, nil)
	rr := httptest.NewRecorder()
	manager.ChallengeHandler().ServeHTTP(rr, r)
	if rr.Code != 404 {
		t.Fatal("cleaned challenge remained accessible")
	}
	challenge.Token = "invalid<token>"
	challenge.KeyAuthorization = challenge.Token + ".thumbprint"
	if err := solver.Present(context.Background(), challenge); err == nil {
		t.Fatal("non-base64url challenge token accepted")
	}
}

func ipTLSUnitBundle(t *testing.T, ip string) ([]byte, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	root := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "IP TLS unit root"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(365 * 24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	rootDER, err := x509.CreateCertificate(rand.Reader, root, root, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	root, err = x509.ParseCertificate(rootDER)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(48 * time.Hour), IPAddresses: []net.IP{net.ParseIP(ip)}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, root, &leafKey.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		t.Fatal(err)
	}
	bundle := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})...)
	pool := x509.NewCertPool()
	pool.AddCert(root)
	return bundle, pool
}

type ipTLSUnitRoundTripper func(*http.Request) (*http.Response, error)

func (f ipTLSUnitRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestIPTLSCachedHandshakesDoNotIssueOrReadDisk(t *testing.T) {
	options := ipTLSUnitOptions(t)
	bundle, roots := ipTLSUnitBundle(t, options.IP)
	options.Roots = roots
	var requests atomic.Int64
	options.HTTPClient = &http.Client{Transport: ipTLSUnitRoundTripper(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return nil, errors.New("unexpected CA request")
	})}
	if err := atomicIPTLSPrivateFile(filepath.Join(options.CacheDir, "certificate.pem"), bundle); err != nil {
		t.Fatal(err)
	}
	manager, err := newIPTLSManager(options)
	if err != nil {
		t.Fatal(err)
	}
	                                                                         
	                                                                          
	if err := os.Remove(filepath.Join(options.CacheDir, "certificate.pem")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 512; i++ {
		if _, err := manager.GetCertificate(&tls.ClientHelloInfo{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := manager.GetCertificate(&tls.ClientHelloInfo{ServerName: "other.example"}); err == nil {
		t.Fatal("wrong SNI accepted")
	}
	manager.Start(context.Background(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ready") }))
	if manager.Address() == "" {
		t.Fatal("TLS listener was not started")
	}
	if got := cap(manager.listener.(*connectionLimitListener).slots); got != ipTLSMaxConnections {
		t.Fatalf("connection cap=%d", got)
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: options.IP}, DisableKeepAlives: true, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, manager.Address())
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	var group sync.WaitGroup
	problems := make(chan error, 32)
	for i := 0; i < 32; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			response, err := client.Get("https://" + options.IP + "/")
			if err != nil {
				problems <- err
				return
			}
			defer response.Body.Close()
			if response.StatusCode != 200 {
				problems <- errors.New("cached TLS API not ready")
			}
		}()
	}
	group.Wait()
	close(problems)
	for err := range problems {
		t.Error(err)
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := manager.Shutdown(shutdown); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 0 {
		t.Fatalf("handshakes caused %d CA requests", requests.Load())
	}
	manager.opts.Now = func() time.Time { return manager.current.Load().certificate.Leaf.NotAfter.Add(time.Second) }
	if _, err := manager.GetCertificate(&tls.ClientHelloInfo{}); err == nil {
		t.Fatal("expired certificate served")
	}
}

func TestIPTLSDirectIngressIgnoresForwardedIdentityAndPreservesProtocol(t *testing.T) {
	guard := newResourceGuard(4)
	guard.trustedProxies = parseTrustedProxyPrefixes("173.245.48.0/20,127.0.0.1/32")
	for _, remote := range []string{"173.245.48.99:34567", "127.0.0.1:55000"} {
		t.Run(remote, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "https://203.0.113.77:8445/api/ws", nil)
			request.RemoteAddr = remote
			request.Header.Set("X-Forwarded-For", "203.0.113.123")
			request.Header.Set("Forwarded", "for=203.0.113.123;proto=https")
			request.Header.Set("X-Real-IP", "203.0.113.123")
			request.Header.Set("Origin", "https://frostbit.io")
			request.Header.Set("Cookie", "session=test-session")
			request.Header.Set("Upgrade", "websocket")
			request.Header.Set("Connection", "Upgrade")
			originalTLS, originalHost := request.TLS, request.Host
			peer, _, _ := net.SplitHostPort(remote)
			                                                                      
			                                                                     
			if guard.clientIP(request) != "203.0.113.123" {
				t.Fatal("fixture did not exercise a trusted proxy peer")
			}
			called := false
			publicHandler := guard.Wrap(http.HandlerFunc(func(w http.ResponseWriter, received *http.Request) {
				called = true
				if guard.clientIP(received) != peer {
					t.Errorf("direct limiter identity=%s want=%s", guard.clientIP(received), peer)
				}
				for _, header := range []string{"X-Forwarded-For", "Forwarded", "X-Real-IP"} {
					if received.Header.Get(header) != "" {
						t.Errorf("forwarding header survived: %s", header)
					}
				}
				if received.Header.Get("Origin") != "https://frostbit.io" || received.Header.Get("Cookie") != "session=test-session" ||
					received.Header.Get("Upgrade") != "websocket" || received.Header.Get("Connection") != "Upgrade" ||
					received.TLS != originalTLS || received.RemoteAddr != remote || received.Host != originalHost {
					t.Fatal("direct ingress changed browser, WebSocket or transport context")
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			recorder := httptest.NewRecorder()
			withoutIPTLSForwardingHeaders(publicHandler).ServeHTTP(recorder, request)
			if !called || recorder.Code != http.StatusNoContent {
				t.Fatalf("guarded handler called=%v status=%d", called, recorder.Code)
			}
			if _, exists := guard.clients[peer]; !exists {
				t.Fatal("original shared guard did not account the real peer")
			}
			if _, exists := guard.clients["203.0.113.123"]; exists {
				t.Fatal("client selected its limiter bucket via XFF")
			}
		})
	}
}
