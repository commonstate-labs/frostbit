package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

                                                                             
                                                                            
                                                                              
type ipTLSWireCA struct {
	t       *testing.T
	server  *httptest.Server
	ip      string
	base    time.Time
	root    *x509.Certificate
	rootKey *ecdsa.PrivateKey
	roots   *x509.CertPool
	rootPEM []byte

	mu               sync.Mutex
	nonces           map[string]bool
	nextNonce        int
	accountKey       *ecdsa.PublicKey
	accountThumb     string
	orders           map[int]*ipTLSWireOrder
	newOrders        int
	issued           int
	validated        int
	verifiedJWS      int
	accountRequests  int
	profiles         []string
	problems         []string
	failOrders       bool
	blockOrders      bool
	wrongSAN         bool
	challengeBaseURL string
	blockEntered     chan struct{}
	blockCanceled    chan struct{}
	blockRelease     chan struct{}
	enterOnce        sync.Once
	cancelOnce       sync.Once
	releaseOnce      sync.Once
}

type ipTLSWireOrder struct {
	id        int
	status    string
	validated bool
	chainPEM  []byte
}

type ipTLSWireStats struct {
	NewOrders, Issued, Validated, VerifiedJWS, AccountRequests int
	Profiles, Problems                                         []string
}

func newIPTLSWireCA(t *testing.T, ip string) *ipTLSWireCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	rootTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(88001), Subject: pkix.Name{CommonName: "Explorer IP TLS test root"},
		NotBefore: now.Add(-24 * time.Hour), NotAfter: now.Add(365 * 24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	rootCert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(rootCert)
	ca := &ipTLSWireCA{
		t: t, ip: ip, base: now, root: rootCert, rootKey: key, roots: roots,
		rootPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		nonces:  make(map[string]bool), orders: make(map[int]*ipTLSWireOrder),
		blockEntered: make(chan struct{}), blockCanceled: make(chan struct{}), blockRelease: make(chan struct{}),
	}
	ca.server = httptest.NewTLSServer(http.HandlerFunc(ca.serveHTTP))
	t.Cleanup(func() {
		ca.releaseOnce.Do(func() { close(ca.blockRelease) })
		ca.server.CloseClientConnections()
		ca.server.Close()
	})
	return ca
}

func (ca *ipTLSWireCA) directoryURL() string { return ca.server.URL + "/directory" }

func (ca *ipTLSWireCA) acmeClient() *http.Client {
	client := ca.server.Client()
	transport := client.Transport.(*http.Transport).Clone()
	transport.Proxy = nil
	allowed := strings.TrimPrefix(ca.server.URL, "https://")
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != allowed {
			return nil, fmt.Errorf("fake ACME test forbids non-fixture network target %q", address)
		}
		return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, network, address)
	}
	client.Transport = transport
	client.Timeout = 2 * time.Second
	ca.t.Cleanup(client.CloseIdleConnections)
	return client
}

func (ca *ipTLSWireCA) stats() ipTLSWireStats {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	return ipTLSWireStats{
		NewOrders: ca.newOrders, Issued: ca.issued, Validated: ca.validated,
		VerifiedJWS: ca.verifiedJWS, AccountRequests: ca.accountRequests,
		Profiles: append([]string(nil), ca.profiles...), Problems: append([]string(nil), ca.problems...),
	}
}

func (ca *ipTLSWireCA) setChallengeURL(url string) {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	ca.challengeBaseURL = url
}

func (ca *ipTLSWireCA) setOrderFailure(fail bool) {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	ca.failOrders = fail
}

func (ca *ipTLSWireCA) setOrderBlock() {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	ca.blockOrders = true
}

func (ca *ipTLSWireCA) setWrongSAN() {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	ca.wrongSAN = true
}

func (ca *ipTLSWireCA) addNonce(w http.ResponseWriter) {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	ca.nextNonce++
	nonce := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("ip-tls-test-nonce-%d", ca.nextNonce)))
	ca.nonces[nonce] = true
	w.Header().Set("Replay-Nonce", nonce)
}

func (ca *ipTLSWireCA) json(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (ca *ipTLSWireCA) reject(w http.ResponseWriter, message string) {
	ca.mu.Lock()
	ca.problems = append(ca.problems, message)
	ca.mu.Unlock()
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type": "urn:ietf:params:acme:error:malformed", "detail": message, "status": http.StatusBadRequest,
	})
}

func (ca *ipTLSWireCA) readJWS(r *http.Request) ([]byte, error) {
	if r.Method != http.MethodPost {
		return nil, fmt.Errorf("ACME resource requires POST: %s", r.URL.Path)
	}
	var envelope struct{ Protected, Payload, Signature string }
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode JWS: %w", err)
	}
	protectedBytes, err := base64.RawURLEncoding.DecodeString(envelope.Protected)
	if err != nil {
		return nil, fmt.Errorf("decode protected: %w", err)
	}
	var protected struct {
		Alg, Nonce, URL, KID string
		JWK                  *struct{ Kty, Crv, X, Y string }
	}
	if err := json.Unmarshal(protectedBytes, &protected); err != nil {
		return nil, err
	}
	if protected.Alg != "ES256" || protected.URL != ca.server.URL+r.URL.RequestURI() {
		return nil, fmt.Errorf("invalid JWS algorithm or URL at %s", r.URL.Path)
	}
	ca.mu.Lock()
	nonceOK := ca.nonces[protected.Nonce]
	delete(ca.nonces, protected.Nonce)
	key := ca.accountKey
	ca.mu.Unlock()
	if !nonceOK {
		return nil, fmt.Errorf("missing, reused or unknown nonce")
	}
	var thumb string
	if r.URL.Path == "/new-account" {
		if protected.JWK == nil || protected.KID != "" || protected.JWK.Kty != "EC" || protected.JWK.Crv != "P-256" {
			return nil, fmt.Errorf("newAccount must carry a P-256 JWK")
		}
		x, xerr := base64.RawURLEncoding.DecodeString(protected.JWK.X)
		y, yerr := base64.RawURLEncoding.DecodeString(protected.JWK.Y)
		if xerr != nil || yerr != nil {
			return nil, fmt.Errorf("invalid JWK coordinates")
		}
		key = &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
		if !key.Curve.IsOnCurve(key.X, key.Y) {
			return nil, fmt.Errorf("JWK is not on P-256")
		}
		canonicalJWK := fmt.Sprintf(`{"crv":"P-256","kty":"EC","x":"%s","y":"%s"}`, protected.JWK.X, protected.JWK.Y)
		digest := sha256.Sum256([]byte(canonicalJWK))
		thumb = base64.RawURLEncoding.EncodeToString(digest[:])
	} else if protected.KID != ca.server.URL+"/account/1" || key == nil || protected.JWK != nil {
		return nil, fmt.Errorf("request does not use registered account KID")
	}
	sig, err := base64.RawURLEncoding.DecodeString(envelope.Signature)
	if err != nil || len(sig) != 64 {
		return nil, fmt.Errorf("invalid JWS signature size")
	}
	digest := sha256.Sum256([]byte(envelope.Protected + "." + envelope.Payload))
	if !ecdsa.Verify(key, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		return nil, fmt.Errorf("JWS signature verification failed")
	}
	payload, err := base64.RawURLEncoding.DecodeString(envelope.Payload)
	if err != nil {
		return nil, err
	}
	ca.mu.Lock()
	ca.verifiedJWS++
	if thumb != "" {
		ca.accountKey, ca.accountThumb = key, thumb
	}
	ca.mu.Unlock()
	return payload, nil
}

func (ca *ipTLSWireCA) orderJSON(order *ipTLSWireOrder) map[string]any {
	base := ca.server.URL
	id := strconv.Itoa(order.id)
	value := map[string]any{
		"status": order.status, "profile": "shortlived", "expires": ca.base.Add(time.Hour),
		"identifiers":    []map[string]string{{"type": "ip", "value": ca.ip}},
		"authorizations": []string{base + "/authz/" + id}, "finalize": base + "/finalize/" + id,
	}
	if len(order.chainPEM) != 0 {
		value["certificate"] = base + "/certificate/" + id
	}
	return value
}

func (ca *ipTLSWireCA) challengeJSON(id int, validated bool) map[string]any {
	status := "pending"
	if validated {
		status = "valid"
	}
	return map[string]any{
		"type": "http-01", "url": ca.server.URL + "/challenge/" + strconv.Itoa(id),
		"status": status, "token": fmt.Sprintf("ip-tls-token-%d", id),
	}
}

func (ca *ipTLSWireCA) serveHTTP(w http.ResponseWriter, r *http.Request) {
	ca.addNonce(w)
	if r.URL.Path == "/directory" {
		ca.json(w, http.StatusOK, map[string]any{
			"newNonce": ca.server.URL + "/new-nonce", "newAccount": ca.server.URL + "/new-account",
			"newOrder": ca.server.URL + "/new-order", "meta": map[string]any{
				"termsOfService": ca.server.URL + "/terms", "profiles": map[string]string{"shortlived": ca.server.URL + "/profile"},
			},
		})
		return
	}
	if r.URL.Path == "/new-nonce" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	payload, err := ca.readJWS(r)
	if err != nil {
		ca.reject(w, err.Error())
		return
	}
	if r.URL.Path == "/new-account" || r.URL.Path == "/account/1" {
		ca.mu.Lock()
		ca.accountRequests++
		ca.mu.Unlock()
		w.Header().Set("Location", ca.server.URL+"/account/1")
		ca.json(w, http.StatusCreated, map[string]any{"status": "valid", "orders": ca.server.URL + "/orders"})
		return
	}
	if r.URL.Path == "/new-order" {
		var order struct {
			Profile     string
			Identifiers []struct{ Type, Value string }
		}
		if err := json.Unmarshal(payload, &order); err != nil || order.Profile != "shortlived" || len(order.Identifiers) != 1 || order.Identifiers[0].Type != "ip" || order.Identifiers[0].Value != ca.ip {
			ca.reject(w, "order must use shortlived and exactly the configured IP identifier")
			return
		}
		ca.mu.Lock()
		ca.newOrders++
		ca.profiles = append(ca.profiles, order.Profile)
		id, fail, block := ca.newOrders, ca.failOrders, ca.blockOrders
		ca.mu.Unlock()
		if block {
			ca.enterOnce.Do(func() { close(ca.blockEntered) })
			select {
			case <-r.Context().Done():
				ca.cancelOnce.Do(func() { close(ca.blockCanceled) })
			case <-ca.blockRelease:
			}
			return
		}
		if fail {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"type": "urn:ietf:params:acme:error:rejectedIdentifier", "detail": "test renewal rejected", "status": 400})
			return
		}
		ca.mu.Lock()
		state := &ipTLSWireOrder{id: id, status: "pending"}
		ca.orders[id] = state
		value := ca.orderJSON(state)
		ca.mu.Unlock()
		w.Header().Set("Location", ca.server.URL+"/order/"+strconv.Itoa(id))
		ca.json(w, http.StatusCreated, value)
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 2 {
		ca.reject(w, "unknown ACME path")
		return
	}
	id, err := strconv.Atoi(parts[1])
	ca.mu.Lock()
	order := ca.orders[id]
	ca.mu.Unlock()
	if err != nil || order == nil {
		ca.reject(w, "unknown order")
		return
	}
	switch parts[0] {
	case "authz":
		var update struct{ Status string }
		_ = json.Unmarshal(payload, &update)
		ca.mu.Lock()
		status := "pending"
		if order.validated {
			status = "valid"
		}
		if update.Status == "deactivated" {
			status = "deactivated"
		}
		value := map[string]any{
			"identifier": map[string]string{"type": "ip", "value": ca.ip}, "status": status,
			"expires": ca.base.Add(time.Hour), "challenges": []map[string]any{ca.challengeJSON(id, order.validated)},
		}
		ca.mu.Unlock()
		ca.json(w, http.StatusOK, value)
	case "challenge":
		ca.mu.Lock()
		baseURL, thumb := ca.challengeBaseURL, ca.accountThumb
		ca.mu.Unlock()
		token := fmt.Sprintf("ip-tls-token-%d", id)
		req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, baseURL+"/.well-known/acme-challenge/"+token, nil)
		req.Host = ca.ip + ":80"
		client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 2 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			ca.reject(w, "HTTP-01 lookup failed: "+err.Error())
			return
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		client.CloseIdleConnections()
		if resp.StatusCode != http.StatusOK || readErr != nil || strings.TrimSpace(string(body)) != token+"."+thumb {
			ca.reject(w, fmt.Sprintf("HTTP-01 key authorization mismatch: status=%d", resp.StatusCode))
			return
		}
		ca.mu.Lock()
		order.validated, order.status = true, "ready"
		ca.validated++
		ca.mu.Unlock()
		ca.json(w, http.StatusOK, ca.challengeJSON(id, true))
	case "order":
		ca.mu.Lock()
		value := ca.orderJSON(order)
		ca.mu.Unlock()
		ca.json(w, http.StatusOK, value)
	case "finalize":
		ca.finalize(w, payload, order)
	case "certificate":
		ca.mu.Lock()
		chain := append([]byte(nil), order.chainPEM...)
		ca.mu.Unlock()
		if len(chain) == 0 {
			ca.reject(w, "certificate not ready")
			return
		}
		w.Header().Set("Content-Type", "application/pem-certificate-chain")
		_, _ = w.Write(chain)
	default:
		ca.reject(w, "unknown ACME resource")
	}
}

func (ca *ipTLSWireCA) finalize(w http.ResponseWriter, payload []byte, order *ipTLSWireOrder) {
	var request struct{ CSR string }
	if err := json.Unmarshal(payload, &request); err != nil {
		ca.reject(w, "invalid finalize payload")
		return
	}
	csrDER, err := base64.RawURLEncoding.DecodeString(request.CSR)
	if err != nil {
		ca.reject(w, "invalid CSR encoding")
		return
	}
	csr, err := x509.ParseCertificateRequest(csrDER)
	if err != nil || csr.CheckSignature() != nil || len(csr.IPAddresses) != 1 || !csr.IPAddresses[0].Equal(net.ParseIP(ca.ip)) || len(csr.DNSNames) != 0 || len(csr.EmailAddresses) != 0 || len(csr.URIs) != 0 {
		ca.reject(w, "CSR must have a valid signature and exactly the configured IP SAN")
		return
	}
	ca.mu.Lock()
	if !order.validated {
		ca.mu.Unlock()
		ca.reject(w, "order is not validated")
		return
	}
	serial := ca.issued + 1
	ip := ca.ip
	if ca.wrongSAN {
		ip = "203.0.113.11"
	}
	ca.mu.Unlock()
	lifetime := 160 * time.Hour
	if serial == 1 {
		lifetime = time.Hour
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(int64(99000 + serial)), Subject: csr.Subject,
		NotBefore: ca.base.Add(-time.Minute), NotAfter: ca.base.Add(lifetime),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.ParseIP(ip)}, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.root, csr.PublicKey, ca.rootKey)
	if err != nil {
		ca.reject(w, "test CA signing failed")
		return
	}
	chain := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), ca.rootPEM...)
	ca.mu.Lock()
	ca.issued++
	order.chainPEM, order.status = chain, "valid"
	value := ca.orderJSON(order)
	ca.mu.Unlock()
	w.Header().Set("Location", ca.server.URL+"/order/"+strconv.Itoa(order.id))
	ca.json(w, http.StatusOK, value)
}
