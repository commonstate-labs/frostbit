package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultIPTLSDirectory = "https://acme-v02.api.letsencrypt.org/directory"
	ipTLSChallengePrefix  = "/.well-known/acme-challenge/"
	ipTLSMaxConnections   = 256
	ipTLSMaxCacheBytes    = 1 << 20
)

                                                                              
                                                                               
                                   
type ipTLSOptions struct {
	Enabled                                           bool
	IP, ListenAddr, CacheDir, DirectoryURL            string
	Now                                               func() time.Time
	CheckInterval, RetryMin, RetryMax, AttemptTimeout time.Duration
	HTTPClient                                        *http.Client
	Roots                                             *x509.CertPool
	Logf                                              func(string, ...any)
}

type ipTLSCertificate struct {
	certificate tls.Certificate
	renewAt     time.Time
}

type ipTLSManager struct {
	opts                    ipTLSOptions
	ip                      string
	current                 atomic.Pointer[ipTLSCertificate]
	challengeMu             sync.RWMutex
	token, keyAuthorization string
	lifecycleMu             sync.Mutex
	started, stopped        bool
	cancel                  context.CancelFunc
	server                  *http.Server
	listener                net.Listener
	done                    chan struct{}
	doneOnce                sync.Once
	retryAfter              atomic.Int64
	httpClient              *http.Client
	ownedTransport          *http.Transport
}

func newIPTLSManager(opts ipTLSOptions) (*ipTLSManager, error) {
	if !opts.Enabled {
		return nil, nil
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logf == nil {
		opts.Logf = log.Printf
	}
	if opts.ListenAddr == "" {
		opts.ListenAddr = ":8445"
	}
	if opts.DirectoryURL == "" {
		opts.DirectoryURL = defaultIPTLSDirectory
	}
	if opts.CheckInterval <= 0 {
		opts.CheckInterval = time.Hour
	}
	if opts.RetryMin <= 0 {
		opts.RetryMin = time.Minute
	}
	if opts.RetryMax <= 0 {
		opts.RetryMax = time.Hour
	}
	if opts.RetryMax < opts.RetryMin {
		opts.RetryMax = opts.RetryMin
	}
	if opts.AttemptTimeout <= 0 || opts.AttemptTimeout > 3*time.Minute {
		opts.AttemptTimeout = 3 * time.Minute
	}
	if opts.CacheDir == "" {
		return nil, errors.New("IP TLS cache directory is required")
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(opts.IP))
	if err != nil || !isPublicExplorerIP(ip) {
		return nil, fmt.Errorf("IP TLS requires one explicit public IP (got %q)", opts.IP)
	}
	ip = ip.Unmap()
	listenHost, _, err := net.SplitHostPort(opts.ListenAddr)
	if err != nil {
		return nil, fmt.Errorf("IP TLS listen address: %w", err)
	}
	if listenHost != "" {
		if _, err := netip.ParseAddr(listenHost); err != nil {
			return nil, errors.New("IP TLS listen host must be an IP literal")
		}
	}
	directory, err := url.Parse(opts.DirectoryURL)
	if err != nil || directory.Host == "" || directory.User != nil || directory.Fragment != "" ||
		(directory.Scheme != "https" && !(directory.Scheme == "http" && isLoopbackHostname(directory.Hostname()))) {
		return nil, errors.New("IP TLS ACME directory must use HTTPS (HTTP is allowed only on loopback for tests)")
	}
	if err := prepareIPTLSCache(opts.CacheDir); err != nil {
		return nil, err
	}
	m := &ipTLSManager{opts: opts, ip: ip.String(), done: make(chan struct{})}
	m.httpClient = m.acmeHTTPClient()
	if cached, err := m.loadCertificate(); err == nil {
		m.current.Store(cached)
		m.opts.Logf("[IPTLS] cached certificate loaded for %s; expires %s", m.ip, cached.certificate.Leaf.NotAfter.UTC().Format(time.RFC3339))
	} else if !errors.Is(err, os.ErrNotExist) {
		m.opts.Logf("[IPTLS] ignoring invalid certificate cache for %s: %v", m.ip, err)
	}
	return m, nil
}

func isPublicExplorerIP(ip netip.Addr) bool {
	return ip.IsValid() && ip.Zone() == "" && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast()
}

                                                                         
                                                                              
                                                                              
func isDiscoverableExplorerIP(ip netip.Addr) bool {
	if !isPublicExplorerIP(ip) {
		return false
	}
	ip = ip.Unmap()
	for _, prefix := range explorerIPDiscoveryExcludedPrefixes {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

var explorerIPDiscoveryExcludedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"), netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/32"), netip.MustParsePrefix("2001:2::/48"),
	netip.MustParsePrefix("2001:10::/28"), netip.MustParsePrefix("2001:20::/28"),
	netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
}

func isLoopbackHostname(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

                                                                             
                                                                            
func discoverExplorerIP(listenAddr string) (string, error) {
	if host, _, err := net.SplitHostPort(listenAddr); err == nil {
		if ip, err := netip.ParseAddr(host); err == nil && isDiscoverableExplorerIP(ip) {
			return ip.Unmap().String(), nil
		}
	}
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}
	unique := make(map[netip.Addr]struct{})
	for _, address := range addresses {
		prefix, err := netip.ParsePrefix(address.String())
		if err == nil && isDiscoverableExplorerIP(prefix.Addr()) {
			unique[prefix.Addr().Unmap()] = struct{}{}
		}
	}
	if len(unique) != 1 {
		return "", fmt.Errorf("found %d public interface IPs; set --ip-tls-ip", len(unique))
	}
	for ip := range unique {
		return ip.String(), nil
	}
	return "", errors.New("no public IP")
}

func (m *ipTLSManager) Address() string {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	if m.listener == nil {
		return ""
	}
	return m.listener.Addr().String()
}

func (m *ipTLSManager) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	if hello != nil && hello.ServerName != "" && hello.ServerName != m.ip {
		return nil, errors.New("IP TLS SNI does not match configured IP")
	}
	cert := m.current.Load()
	if cert == nil {
		return nil, errors.New("IP TLS certificate is not available yet")
	}
	now := m.opts.Now()
	if now.Before(cert.certificate.Leaf.NotBefore) || !now.Before(cert.certificate.Leaf.NotAfter) {
		return nil, errors.New("IP TLS certificate is not currently valid")
	}
	return &cert.certificate, nil
}

func (m *ipTLSManager) ChallengeHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		host := r.Host
		if split, _, err := net.SplitHostPort(host); err == nil {
			host = split
		}
		ip, err := netip.ParseAddr(strings.Trim(host, "[]"))
		if err != nil || ip.Unmap().String() != m.ip || len(r.URL.Path) > len(ipTLSChallengePrefix)+256 || !strings.HasPrefix(r.URL.Path, ipTLSChallengePrefix) {
			http.NotFound(w, r)
			return
		}
		token := strings.TrimPrefix(r.URL.Path, ipTLSChallengePrefix)
		m.challengeMu.RLock()
		active := m.token != "" && token == m.token
		value := m.keyAuthorization
		m.challengeMu.RUnlock()
		if !active {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(value))
		}
	})
}

                                                                            
                                                                               
                                                                            
func withoutIPTLSForwardingHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Del("X-Forwarded-For")
		r.Header.Del("Forwarded")
		r.Header.Del("X-Real-IP")
		next.ServeHTTP(w, r)
	})
}

                                                                               
                                                                                
func (m *ipTLSManager) Start(parent context.Context, handler http.Handler) {
	m.lifecycleMu.Lock()
	if m.started || m.stopped {
		m.lifecycleMu.Unlock()
		return
	}
	m.started = true
	ctx, cancel := context.WithCancel(parent)
	m.cancel = cancel
	listener, err := net.Listen("tcp", m.opts.ListenAddr)
	if err != nil {
		m.lifecycleMu.Unlock()
		cancel()
		m.doneOnce.Do(func() { close(m.done) })
		m.opts.Logf("[IPTLS] optional listener unavailable: %v", err)
		return
	}
	m.listener = newConnectionLimitListener(listener, ipTLSMaxConnections)
	m.server = &http.Server{Handler: withoutIPTLSForwardingHeaders(handler), TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: m.GetCertificate},
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	server, limited := m.server, m.listener
	m.lifecycleMu.Unlock()
	m.opts.Logf("[IPTLS] optional HTTPS IP listener active at %s; certificate issuance runs in background", listener.Addr())
	var tasks sync.WaitGroup
	tasks.Add(2)
	go func() {
		defer tasks.Done()
		if err := server.ServeTLS(limited, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			m.opts.Logf("[IPTLS] optional HTTPS listener stopped: %v", err)
			cancel()
			_ = server.Close()
		}
	}()
	go func() {
		defer tasks.Done()
		                                                                         
		                                                                       
		defer limited.Close()
		m.run(ctx)
	}()
	go func() { tasks.Wait(); m.doneOnce.Do(func() { close(m.done) }) }()
}

func (m *ipTLSManager) Shutdown(ctx context.Context) error {
	m.lifecycleMu.Lock()
	m.stopped = true
	cancel, server, started := m.cancel, m.server, m.started
	m.lifecycleMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if !started {
		m.doneOnce.Do(func() { close(m.done) })
	}
	if server != nil {
		if err := server.Shutdown(ctx); err != nil {
			_ = server.Close()
		}
	}
	select {
	case <-m.done:
		if m.ownedTransport != nil {
			m.ownedTransport.CloseIdleConnections()
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *ipTLSManager) run(ctx context.Context) {
	defer m.clearChallenge()
	retry := m.opts.RetryMin
	for {
		if ctx.Err() != nil {
			return
		}
		now := m.opts.Now()
		cached := m.current.Load()
		delay := m.opts.CheckInterval
		if cached == nil || !now.Before(cached.renewAt) {
			attempt, cancel := context.WithTimeout(ctx, m.opts.AttemptTimeout)
			err := m.obtainCertificate(attempt)
			cancel()
			m.clearChallenge()
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				delay = retry
				if after := time.Unix(0, m.retryAfter.Load()).Sub(m.opts.Now()); after > delay {
					delay = after
				}
				m.opts.Logf("[IPTLS] certificate attempt for %s failed; retry in %s: %.2048s", m.ip, delay, err.Error())
				if retry < m.opts.RetryMax/2 {
					retry *= 2
				} else {
					retry = m.opts.RetryMax
				}
			} else {
				retry = m.opts.RetryMin
			}
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (m *ipTLSManager) clearChallenge() {
	m.challengeMu.Lock()
	m.token, m.keyAuthorization = "", ""
	m.challengeMu.Unlock()
}

func defaultExplorerIPTLSCache(dataDir string) string { return filepath.Join(dataDir, "ip-tls") }
