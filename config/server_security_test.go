package config

import (
	"fmt"
	"strings"
	"testing"
)

func TestDefaultServerDiagnosticsAreDisabledAndLoopbackBound(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Server.EnableDebugAPI {
		t.Fatal("debug API must be disabled by default")
	}
	if cfg.Server.EnablePprof {
		t.Fatal("pprof must be disabled by default")
	}
	if cfg.Server.PprofBind != "127.0.0.1" {
		t.Fatalf("default pprof bind = %q, want loopback", cfg.Server.PprofBind)
	}
	if cfg.Server.MaxTxBatchTransactions != DefaultMaxTxBatchTransactions {
		t.Fatalf("default max tx batch transactions = %d, want %d", cfg.Server.MaxTxBatchTransactions, DefaultMaxTxBatchTransactions)
	}
	if cfg.Server.MaxConnections <= 0 || cfg.Server.MaxConnectionsPerIP <= 0 || cfg.Server.MaxConnectionsPerIP > cfg.Server.MaxConnections {
		t.Fatalf("invalid default connection limits: total=%d perIP=%d", cfg.Server.MaxConnections, cfg.Server.MaxConnectionsPerIP)
	}
	if cfg.Server.HTTPReadHeaderTimeout <= 0 || cfg.Server.HTTPIdleTimeout <= 0 || cfg.Server.MaxHeaderBytes <= 0 {
		t.Fatalf("HTTP transport limits must be enabled by default: %#v", cfg.Server)
	}
	if cfg.Server.QUICHandshakeIdleTimeout <= 0 || cfg.Server.QUICMaxIncomingStreams <= 0 || cfg.Server.QUICMaxIncomingUniStreams <= 0 {
		t.Fatalf("QUIC transport limits must be enabled by default: %#v", cfg.Server)
	}
	if cfg.PeerGate.DefaultPathRPS <= 0 || cfg.PeerGate.DefaultPathBurst <= 0 {
		t.Fatalf("unregistered routes need a finite default bucket: %#v", cfg.PeerGate)
	}
	if cfg.PeerGate.MaxInflight <= cfg.PeerGate.ConsensusReserve {
		t.Fatalf("invalid default in-flight reserve: %#v", cfg.PeerGate)
	}
	if cfg.PeerGate.TxBatchMaxInflight <= 0 || cfg.PeerGate.TxBatchMaxInflight > cfg.PeerGate.MaxInflight-cfg.PeerGate.ConsensusReserve {
		t.Fatalf("invalid tx batch in-flight cap: %#v", cfg.PeerGate)
	}
}

func TestServerSecurityValidationRejectsUnsafePprofBind(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Server.EnablePprof = true
	cfg.Server.PprofBind = "0.0.0.0"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "pprof") {
		t.Fatalf("unsafe pprof bind validation error=%v", err)
	}
}

func TestServerSecurityValidationRejectsTxBatchAboveProtocolCap(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Server.MaxTxBatchTransactions = DefaultMaxTxBatchTransactions + 1
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "MaxTxBatchTransactions") {
		t.Fatalf("oversized tx batch config validation error=%v", err)
	}
}

func TestPeerGateInflightAutoSizing(t *testing.T) {
	tests := []struct {
		cpu     int
		total   int
		reserve int
	}{
		{cpu: 1, total: 256, reserve: 64},
		{cpu: 4, total: 256, reserve: 64},
		{cpu: 8, total: 512, reserve: 128},
		{cpu: 16, total: 1024, reserve: 256},
		{cpu: 32, total: 2048, reserve: 512},
		{cpu: 128, total: 2048, reserve: 512},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("cpu_%d", tt.cpu), func(t *testing.T) {
			total, reserve := peerGateInflightForCPU(tt.cpu)
			if total != tt.total || reserve != tt.reserve {
				t.Fatalf("auto inflight for cpu=%d = %d/%d, want %d/%d", tt.cpu, total, reserve, tt.total, tt.reserve)
			}
		})
	}
}

func TestPeerGateInflightZeroUsesAutoAndPositiveValuesOverride(t *testing.T) {
	cfg := DefaultConfig()
	cfg.PeerGate.MaxInflight = 0
	cfg.PeerGate.ConsensusReserve = 0
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate auto inflight config: %v", err)
	}
	wantTotal, wantReserve := DefaultPeerGateInflight()
	if cfg.PeerGate.MaxInflight != wantTotal || cfg.PeerGate.ConsensusReserve != wantReserve {
		t.Fatalf("resolved auto inflight = %d/%d, want %d/%d", cfg.PeerGate.MaxInflight, cfg.PeerGate.ConsensusReserve, wantTotal, wantReserve)
	}

	cfg = DefaultConfig()
	cfg.PeerGate.MaxInflight = 777
	cfg.PeerGate.ConsensusReserve = 111
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate explicit inflight config: %v", err)
	}
	if cfg.PeerGate.MaxInflight != 777 || cfg.PeerGate.ConsensusReserve != 111 {
		t.Fatalf("explicit inflight override changed to %d/%d", cfg.PeerGate.MaxInflight, cfg.PeerGate.ConsensusReserve)
	}
}

func TestPeerGateInflightRejectsNegativeAutoSentinel(t *testing.T) {
	cfg := DefaultConfig()
	cfg.PeerGate.MaxInflight = -1
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "non-negative") {
		t.Fatalf("negative inflight validation error=%v", err)
	}
}
