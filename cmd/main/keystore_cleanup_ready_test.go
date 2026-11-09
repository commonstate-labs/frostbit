package main

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"dex/updater"
)

func TestKeystoreCleanupRequiresBoundNodeTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.NotFoundHandler())
	defer server.Close()
	legacyReady := make(chan struct{})
	close(legacyReady)
	tlsConfig := server.TLS.Clone()
	launcher := &NodeLauncher{
		httpReady: legacyReady,
		instance: &NodeInstance{Server: &http.Server{
			Addr: server.Listener.Addr().String(), TLSConfig: tlsConfig,
		}},
	}
	ctx := context.Background()
	if err := verifyKeystoreCleanupReady(ctx, launcher); err == nil {
		t.Fatal("timeout readiness authorized deletion")
	}
	launcher.listenersBound = true
	if err := verifyKeystoreCleanupReady(ctx, launcher); err != nil {
		t.Fatalf("real TLS listener rejected: %v", err)
	}
	tlsConfig.Certificates = []tls.Certificate{{Certificate: [][]byte{[]byte("different certificate")}}}
	if err := verifyKeystoreCleanupReady(ctx, launcher); err == nil {
		t.Fatal("different listener certificate authorized deletion")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := verifyKeystoreCleanupReady(canceled, launcher); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled cleanup returned %v", err)
	}
}

func TestKeystoreCleanupWaitsForUpdaterStartupConfirmation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cfg := updater.DefaultConfig()
	cfg.Enabled = false
	cfg.StateDir = t.TempDir()
	u := startNodeUpdater(ctx, &cfg, nil)
	select {
	case <-u.startupConfirmed:
	case <-ctx.Done():
		t.Fatal("startup without a pending update never confirmed")
	}
	                                                                       
	                                                                        
	if err := os.WriteFile(filepath.Join(cfg.StateDir, "state.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	u = startNodeUpdater(ctx, &cfg, nil)
	select {
	case <-u.done:
	case <-ctx.Done():
		t.Fatal("invalid update state did not finish")
	}
	select {
	case <-u.errors:
	default:
		t.Fatal("invalid update state was accepted")
	}
	select {
	case <-u.startupConfirmed:
		t.Fatal("failed updater startup authorized deletion")
	default:
	}
}
