package config

import (
	"crypto/tls"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestNodeTLSConfigTrustsOnlyProvidedCertificate(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "node.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := NodeTLSConfig(path, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.InsecureSkipVerify {
		t.Fatal("explicit trust disabled verification")
	}
	transport := &http.Transport{TLSClientConfig: cfg}
	defer transport.CloseIdleConnections()
	response, err := (&http.Client{Transport: transport}).Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	cfg = cfg.Clone()
	cfg.ServerName = "not-this-node.example"
	badTransport := &http.Transport{TLSClientConfig: cfg}
	defer badTransport.CloseIdleConnections()
	if response, err := (&http.Client{Transport: badTransport}).Get(server.URL); err == nil {
		response.Body.Close()
		t.Fatal("incorrect TLS identity accepted")
	}
}

func TestBrokenExplorerTrustFileFailsClosed(t *testing.T) {
	t.Setenv("DEX_EXPLORER_NODE_CA_FILE", filepath.Join(t.TempDir(), "missing.pem"))
	cfg := ExplorerNodeTLSConfig()
	if cfg.VerifyConnection == nil || cfg.VerifyConnection(tls.ConnectionState{}) == nil {
		t.Fatal("bad trust configuration silently downgraded")
	}
}
