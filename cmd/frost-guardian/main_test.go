package main

import (
	"context"
	"dex/frost/guardian"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestGuardianValidatingNodeUsesExplicitTLSAuthority(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/frost/guardian/intent" {
			t.Errorf("unexpected route %s", r.URL.Path)
		}
		var request guardian.Request
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "bad", 400)
			return
		}
		_ = json.NewEncoder(w).Encode(guardian.Intent{Request: request})
	}))
	defer server.Close()
	certFile := filepath.Join(t.TempDir(), "node.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	request := guardian.Request{Chain: "btc", JobID: "tls-finalized-job"}
	client, err := validatingNodeClient(server.URL, certFile, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Resolve(context.Background(), request)
	if err != nil || result.Request.JobID != request.JobID {
		t.Fatalf("trusted node failed: %v", err)
	}
	bad, err := validatingNodeClient(server.URL, certFile, "other-node.example")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bad.Resolve(context.Background(), request); err == nil {
		t.Fatal("wrong TLS node identity accepted")
	}
	untrusted, err := validatingNodeClient(server.URL, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := untrusted.Resolve(context.Background(), request); err == nil {
		t.Fatal("untrusted self-signed node accepted")
	}
}

func TestGuardianValidatingNodeURLBoundaries(t *testing.T) {
	for _, bad := range []string{"http://node.example:6000", "https://user:secret@node.example/", "https://node.example/path", "https://node.example?redirect=1"} {
		if _, err := validatingNodeClient(bad, "", ""); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}
