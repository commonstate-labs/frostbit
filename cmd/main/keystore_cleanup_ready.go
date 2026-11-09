package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"time"

	"dex/logs"
)

func finishKeystoreImportCleanup(ctx context.Context, launcher *NodeLauncher, cleanup *keystoreImportCleanup, source string) {
	defer cleanup.close()
	if err := verifyKeystoreCleanupReady(ctx, launcher); err != nil {
		logs.Warn("Skipping plaintext import source removal for %s (%v)", source, err)
		return
	}
	removed, err := cleanup.remove(ctx)
	if removed {
		logs.Info("Removed plaintext keystore import source %s", source)
		if err != nil {
			logs.Warn("Plaintext import source removal durability is uncertain for %s (%v)", source, err)
		}
	} else if err != nil {
		logs.Warn("Skipping plaintext import source removal for %s (%v)", source, err)
	}
}

func verifyKeystoreCleanupReady(ctx context.Context, launcher *NodeLauncher) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if launcher == nil || !launcher.listenersBound || launcher.Instance() == nil {
		return fmt.Errorf("node did not confirm TCP and QUIC listener binding")
	}
	node := launcher.Instance()
	server := node.Server
	if server == nil || server.TLSConfig == nil || len(server.TLSConfig.Certificates) == 0 || len(server.TLSConfig.Certificates[0].Certificate) == 0 {
		return fmt.Errorf("node TLS listener certificate is unavailable")
	}
	host, port, err := net.SplitHostPort(server.Addr)
	if err != nil {
		return fmt.Errorf("parse node listener address: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("node listener address is not a numeric IP")
	}
	if ip.IsUnspecified() {
		host = "127.0.0.1"
	}
	leaf := server.TLSConfig.Certificates[0].Certificate[0]
	dialer := tls.Dialer{Config: &tls.Config{
		MinVersion: tls.VersionTLS13,
		                                                                        
		                                                                   
		InsecureSkipVerify: true,
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 || !bytes.Equal(state.PeerCertificates[0].Raw, leaf) {
				return fmt.Errorf("node listener TLS certificate mismatch")
			}
			return nil
		},
	}}
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	conn, err := dialer.DialContext(probeCtx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		return fmt.Errorf("check node TLS listener: %w", err)
	}
	_ = conn.Close()
	if node.Emergency != nil && node.Emergency.IsStopped() {
		return fmt.Errorf("node is safety-stopped")
	}
	return ctx.Err()
}
