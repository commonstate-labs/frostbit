package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mholt/acmez/v3"
	"github.com/mholt/acmez/v3/acme"
)

type ipTLSChallengeSolver struct{ manager *ipTLSManager }

func (s ipTLSChallengeSolver) Present(ctx context.Context, challenge acme.Challenge) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if challenge.Type != acme.ChallengeTypeHTTP01 || challenge.Identifier.Type != "ip" || challenge.Identifier.Value != s.manager.ip ||
		!validIPTLSChallengeToken(challenge.Token) || len(challenge.KeyAuthorization) > 1024 || !strings.HasPrefix(challenge.KeyAuthorization, challenge.Token+".") {
		return errors.New("IP TLS received an invalid HTTP-01 challenge")
	}
	s.manager.challengeMu.Lock()
	defer s.manager.challengeMu.Unlock()
	if s.manager.token != "" {
		return errors.New("IP TLS already has an active challenge")
	}
	s.manager.token, s.manager.keyAuthorization = challenge.Token, challenge.KeyAuthorization
	return nil
}

func validIPTLSChallengeToken(token string) bool {
	if len(token) == 0 || len(token) > 256 {
		return false
	}
	for _, c := range token {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func (s ipTLSChallengeSolver) CleanUp(_ context.Context, _ acme.Challenge) error {
	s.manager.clearChallenge()
	return nil
}

type ipTLSResponseTransport struct {
	base    http.RoundTripper
	manager *ipTLSManager
}
type ipTLSBoundedBody struct {
	io.Reader
	io.Closer
}

func (t ipTLSResponseTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	response.Body = ipTLSBoundedBody{Reader: io.LimitReader(response.Body, ipTLSMaxCacheBytes), Closer: response.Body}
	if response.StatusCode >= 400 {
		if value := response.Header.Get("Retry-After"); value != "" {
			now := t.manager.opts.Now()
			var deadline time.Time
			if seconds, err := strconv.ParseInt(value, 10, 32); err == nil && seconds > 0 {
				deadline = now.Add(time.Duration(seconds) * time.Second)
			} else if parsed, err := http.ParseTime(value); err == nil {
				deadline = parsed
			}
			for deadline.After(now) {
				previous := t.manager.retryAfter.Load()
				if deadline.UnixNano() <= previous || t.manager.retryAfter.CompareAndSwap(previous, deadline.UnixNano()) {
					break
				}
			}
		}
	}
	return response, nil
}

func (m *ipTLSManager) acmeHTTPClient() *http.Client {
	var client http.Client
	if m.opts.HTTPClient != nil {
		client = *m.opts.HTTPClient
	}
	transport := client.Transport
	if transport == nil {
		base := http.DefaultTransport.(*http.Transport).Clone()
		base.MaxConnsPerHost = 2
		base.MaxIdleConns = 2
		base.MaxIdleConnsPerHost = 2
		base.IdleConnTimeout = 30 * time.Second
		base.ResponseHeaderTimeout = 20 * time.Second
		base.TLSHandshakeTimeout = 10 * time.Second
		transport = base
		m.ownedTransport = base
	}
	client.Transport = ipTLSResponseTransport{base: transport, manager: m}
	if client.Timeout <= 0 || client.Timeout > 30*time.Second {
		client.Timeout = 30 * time.Second
	}
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return errors.New("IP TLS ACME redirect limit")
		}
		if request.URL.Scheme != "https" && !(request.URL.Scheme == "http" && isLoopbackHostname(request.URL.Hostname())) {
			return errors.New("IP TLS ACME redirect must use HTTPS")
		}
		return nil
	}
	return &client
}

func (m *ipTLSManager) obtainCertificate(ctx context.Context) error {
	account, err := m.loadAccount()
	if err != nil {
		return fmt.Errorf("load ACME account: %w", err)
	}
	client := acmez.Client{Client: &acme.Client{Directory: m.opts.DirectoryURL, HTTPClient: m.httpClient, UserAgent: "DEX-Explorer-IPTLS", PollTimeout: m.opts.AttemptTimeout},
		ChallengeSolvers: map[string]acmez.Solver{acme.ChallengeTypeHTTP01: ipTLSChallengeSolver{manager: m}}}
	directory, err := client.GetDirectory(ctx)
	if err != nil {
		return err
	}
	if directory.Meta == nil {
		return errors.New("ACME directory does not advertise required shortlived profile")
	}
	if _, ok := directory.Meta.Profiles["shortlived"]; !ok {
		return errors.New("ACME directory does not advertise required shortlived profile")
	}
	if account.Status != acme.StatusValid || account.Location == "" {
		account, err = client.NewAccount(ctx, account)
		if err != nil {
			return err
		}
		if err = m.saveAccount(account); err != nil {
			return fmt.Errorf("persist ACME account: %w", err)
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	csr, err := acmez.NewCSR(key, []string{m.ip})
	if err != nil {
		return err
	}
	parameters, err := acmez.OrderParametersFromCSR(account, csr)
	if err != nil {
		return err
	}
	parameters.Profile = "shortlived"
	chains, err := client.ObtainCertificate(ctx, parameters)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	var validationErr error
	for _, chain := range chains {
		if len(chain.ChainPEM)+len(keyPEM) > ipTLSMaxCacheBytes {
			validationErr = errors.New("ACME certificate chain exceeds cache limit")
			continue
		}
		bundle := append(append([]byte(nil), chain.ChainPEM...), keyPEM...)
		validated, err := m.parseCertificate(bundle)
		if err != nil {
			validationErr = err
			continue
		}
		if err = atomicIPTLSPrivateFile(filepath.Join(m.opts.CacheDir, "certificate.pem"), bundle); err != nil {
			return fmt.Errorf("persist IP TLS certificate: %w", err)
		}
		m.current.Store(validated)
		m.retryAfter.Store(0)
		m.opts.Logf("[IPTLS] certificate installed for %s; serial=%s expires=%s renew_after=%s", m.ip, validated.certificate.Leaf.SerialNumber.String(), validated.certificate.Leaf.NotAfter.UTC().Format(time.RFC3339), validated.renewAt.UTC().Format(time.RFC3339))
		return nil
	}
	if validationErr != nil {
		return validationErr
	}
	return errors.New("ACME did not return a certificate chain")
}
