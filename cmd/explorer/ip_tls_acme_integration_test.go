package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const ipTLSWireTestIP = "203.0.113.10"

func ipTLSWireEventually(t *testing.T, description string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}

func ipTLSWireManager(t *testing.T, ca *ipTLSWireCA, cache string, now func() time.Time, overrides ...func(*ipTLSOptions)) (*ipTLSManager, *httptest.Server) {
	t.Helper()
	opts := ipTLSOptions{
		Enabled: true, IP: ipTLSWireTestIP, ListenAddr: "127.0.0.1:0", CacheDir: cache,
		DirectoryURL: ca.directoryURL(), HTTPClient: ca.acmeClient(), Roots: ca.roots, Now: now,
		CheckInterval: 10 * time.Millisecond, RetryMin: 40 * time.Millisecond, RetryMax: 80 * time.Millisecond,
		AttemptTimeout: 10 * time.Second, Logf: t.Logf,
	}
	for _, override := range overrides {
		override(&opts)
	}
	manager, err := newIPTLSManager(opts)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/.well-known/acme-challenge/", manager.ChallengeHandler())
	mux.HandleFunc("/api/nodes", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ordinary_http":"available"}`)
	})
	ordinary := httptest.NewServer(mux)
	t.Cleanup(ordinary.Close)
	ca.setChallengeURL(ordinary.URL)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		shutdown, done := context.WithTimeout(context.Background(), 2*time.Second)
		defer done()
		if err := manager.Shutdown(shutdown); err != nil {
			t.Errorf("IP TLS shutdown: %v", err)
		}
	})
	started := make(chan struct{})
	go func() {
		manager.Start(ctx, mux)
		close(started)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("Start blocked on certificate acquisition")
	}
	ipTLSWireEventually(t, "optional TLS listener", func() bool { return manager.Address() != "" })
	return manager, ordinary
}

func ipTLSWireHTTPAvailable(t *testing.T, server *httptest.Server) {
	t.Helper()
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: time.Second}
	defer client.CloseIdleConnections()
	response, err := client.Get(server.URL + "/api/nodes")
	if err != nil {
		t.Fatalf("ordinary backend unavailable: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1024))
	if err != nil || response.StatusCode != http.StatusOK || string(body) != `{"ordinary_http":"available"}` {
		t.Fatalf("ordinary backend status=%d body=%q err=%v", response.StatusCode, body, err)
	}
}

func ipTLSWireTLSSerial(manager *ipTLSManager, ca *ipTLSWireCA) (string, error) {
	                                                                         
	                                                                            
	address := manager.Address()
	host, _, err := net.SplitHostPort(address)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return "", fmt.Errorf("test refuses non-loopback TLS listener %q", address)
	}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", address, &tls.Config{
		RootCAs: ca.roots, ServerName: ipTLSWireTestIP, MinVersion: tls.VersionTLS12,
	})
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
		return "", err
	}
	certificates := conn.ConnectionState().PeerCertificates
	if len(certificates) == 0 || len(certificates[0].IPAddresses) != 1 || !certificates[0].IPAddresses[0].Equal(net.ParseIP(ipTLSWireTestIP)) {
		return "", fmt.Errorf("TLS did not present exactly the configured IP SAN")
	}
	request, _ := http.NewRequest(http.MethodGet, "https://"+ipTLSWireTestIP+"/api/nodes", nil)
	request.Close = true
	if err := request.Write(conn); err != nil {
		return "", err
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1024))
	if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(body), `"ordinary_http":"available"`) {
		return "", fmt.Errorf("IP TLS did not serve the ordinary backend handler")
	}
	return certificates[0].SerialNumber.String(), nil
}

func TestIPTLSACMEWireRenewalAndCache(t *testing.T) {
	ca := newIPTLSWireCA(t, ipTLSWireTestIP)
	var clockAdvance atomic.Int64
	now := func() time.Time { return ca.base.Add(time.Duration(clockAdvance.Load())) }
	cache := filepath.Join(t.TempDir(), "managed-ip")
	manager, ordinary := ipTLSWireManager(t, ca, cache, now)
	ipTLSWireHTTPAvailable(t, ordinary)
	ipTLSWireEventually(t, "first real ACME issuance", func() bool {
		cert, err := manager.GetCertificate(&tls.ClientHelloInfo{})
		return err == nil && cert.Leaf.SerialNumber.String() == "99001"
	})
	firstSerial, err := ipTLSWireTLSSerial(manager, ca)
	if err != nil || firstSerial != "99001" {
		t.Fatalf("first IP TLS serial=%q err=%v", firstSerial, err)
	}
	firstAddress := manager.Address()
	initial := ca.stats()
	if initial.Issued != 1 || initial.NewOrders != 1 || initial.Validated != 1 || initial.VerifiedJWS < 5 || len(initial.Problems) != 0 {
		t.Fatalf("incomplete wire issuance: %+v", initial)
	}
	                                                                           
	for i := 0; i < 8; i++ {
		if serial, err := ipTLSWireTLSSerial(manager, ca); err != nil || serial != firstSerial {
			t.Fatalf("cached handshake %d serial=%q err=%v", i, serial, err)
		}
	}
	if after := ca.stats(); after.NewOrders != initial.NewOrders {
		t.Fatalf("handshakes issued additional ACME orders: before=%+v after=%+v", initial, after)
	}
	                                                                         
	                                                                      
	ca.setOrderFailure(true)
	clockAdvance.Store(int64(45 * time.Minute))
	ipTLSWireEventually(t, "failed renewal attempt", func() bool { return ca.stats().NewOrders >= 2 })
	if serial, err := ipTLSWireTLSSerial(manager, ca); err != nil || serial != firstSerial {
		t.Fatalf("renewal failure displaced valid old leaf: serial=%q err=%v", serial, err)
	}
	ipTLSWireHTTPAvailable(t, ordinary)
	ca.setOrderFailure(false)
	ipTLSWireEventually(t, "automatic successful renewal", func() bool {
		cert, err := manager.GetCertificate(&tls.ClientHelloInfo{})
		return err == nil && cert.Leaf.SerialNumber.String() == "99002"
	})
	secondSerial, err := ipTLSWireTLSSerial(manager, ca)
	if err != nil || secondSerial != "99002" || manager.Address() != firstAddress {
		t.Fatalf("renewal did not hot-swap on same listener: serial=%q addr=%q err=%v", secondSerial, manager.Address(), err)
	}
	stats := ca.stats()
	if stats.Issued != 2 || stats.Validated != 2 || len(stats.Problems) != 0 {
		t.Fatalf("wire renewals: %+v", stats)
	}
	for _, profile := range stats.Profiles {
		if profile != "shortlived" {
			t.Fatalf("wrong ACME profile %q", profile)
		}
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := manager.Shutdown(shutdown); err != nil {
		t.Fatal(err)
	}
	                                                                          
	                                                                    
	beforeRestart := ca.stats()
	restarted, newOrdinary := ipTLSWireManager(t, ca, cache, now)
	serial, err := ipTLSWireTLSSerial(restarted, ca)
	if err != nil || serial != secondSerial {
		t.Fatalf("cached restart serial=%q err=%v", serial, err)
	}
	ipTLSWireHTTPAvailable(t, newOrdinary)
	time.Sleep(80 * time.Millisecond)
	afterRestart := ca.stats()
	if afterRestart.NewOrders != beforeRestart.NewOrders || afterRestart.AccountRequests != beforeRestart.AccountRequests {
		t.Fatalf("cached restart contacted CA unnecessarily: before=%+v after=%+v", beforeRestart, afterRestart)
	}
}

func TestIPTLSACMEWireFailureKeepsHTTPAvailable(t *testing.T) {
	ca := newIPTLSWireCA(t, ipTLSWireTestIP)
	ca.setOrderFailure(true)
	manager, ordinary := ipTLSWireManager(t, ca, filepath.Join(t.TempDir(), "failed-ca"), func() time.Time { return ca.base })
	ipTLSWireEventually(t, "ACME rejection", func() bool { return ca.stats().NewOrders > 0 })
	for i := 0; i < 3; i++ {
		ipTLSWireHTTPAvailable(t, ordinary)
	}
	if cert, err := manager.GetCertificate(&tls.ClientHelloInfo{}); err == nil || cert != nil {
		t.Fatal("a failed issuer must not invent an IP certificate")
	}
	if stats := ca.stats(); stats.Issued != 0 || stats.Validated != 0 || len(stats.Problems) != 0 {
		t.Fatalf("failure test used malformed ACME wire: %+v", stats)
	}
}

func TestIPTLSACMEWireBlockedIssuerCancelsWithoutBlockingHTTP(t *testing.T) {
	ca := newIPTLSWireCA(t, ipTLSWireTestIP)
	ca.setOrderBlock()
	manager, ordinary := ipTLSWireManager(t, ca, filepath.Join(t.TempDir(), "blocked-ca"), func() time.Time { return ca.base })
	select {
	case <-ca.blockEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("client did not enter the blocked ACME request")
	}
	ipTLSWireHTTPAvailable(t, ordinary)
	shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Shutdown(shutdown); err != nil {
		t.Fatalf("shutdown did not cancel active ACME operation: %v", err)
	}
	select {
	case <-ca.blockCanceled:
	case <-time.After(time.Second):
		t.Fatal("ACME peer did not observe request cancellation")
	}
	ipTLSWireHTTPAvailable(t, ordinary)
	if conn, err := net.DialTimeout("tcp", manager.Address(), 100*time.Millisecond); err == nil {
		_ = conn.Close()
		t.Fatal("optional IP TLS listener remained open after shutdown")
	}
}

func TestIPTLSACMEWireRejectsUntrustedOrWrongIPLeaf(t *testing.T) {
	for _, name := range []string{"untrusted-root", "wrong-ip-san"} {
		t.Run(name, func(t *testing.T) {
			ca := newIPTLSWireCA(t, ipTLSWireTestIP)
			if name == "wrong-ip-san" {
				ca.setWrongSAN()
			}
			cache := filepath.Join(t.TempDir(), "invalid-leaf")
			var rejected atomic.Bool
			manager, ordinary := ipTLSWireManager(t, ca, cache, func() time.Time { return ca.base }, func(opts *ipTLSOptions) {
				if name == "untrusted-root" {
					opts.Roots = x509.NewCertPool()
				}
				opts.Logf = func(format string, args ...any) {
					message := fmt.Sprintf(format, args...)
					t.Log(message)
					if strings.Contains(message, "certificate attempt") && strings.Contains(message, "failed") {
						rejected.Store(true)
					}
				}
			})
			ipTLSWireEventually(t, "complete rejected certificate attempt", rejected.Load)
			stats := ca.stats()
			if stats.Issued < 1 || stats.Validated < 1 || len(stats.Problems) != 0 {
				t.Fatalf("negative control did not complete a well-formed ACME issuance: %+v", stats)
			}
			if cert, err := manager.GetCertificate(&tls.ClientHelloInfo{}); err == nil || cert != nil {
				t.Fatal("an invalid leaf must not enter the served certificate cache")
			}
			if _, err := os.Stat(filepath.Join(cache, "certificate.pem")); !os.IsNotExist(err) {
				t.Fatalf("invalid leaf was persisted: %v", err)
			}
			ipTLSWireHTTPAvailable(t, ordinary)
		})
	}
}
