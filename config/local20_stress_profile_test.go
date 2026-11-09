package config

import (
	"path/filepath"
	"testing"
)

func TestLocal20KeepsExplicitStressCapacityWithAuthenticatedDiagnostics(t *testing.T) {
	cfg, err := LoadFromDir(filepath.Join("env", "local20"))
	if err != nil {
		t.Fatalf("load local20 config: %v", err)
	}

	if cfg.TxPool.PendingTxCacheSize != 50000 ||
		cfg.TxPool.ShortPendingTxCacheSize != 50000 ||
		cfg.TxPool.CacheTxSize != 50000 ||
		cfg.TxPool.ShortTxCacheSize != 50000 ||
		cfg.TxPool.MessageQueueSize != 50000 ||
		cfg.TxPool.MaxPendingTxs != 30000 ||
		cfg.TxPool.TargetTxsPerBlock != 5000 ||
		cfg.TxPool.MaxTxsPerBlock != 5000 ||
		cfg.TxPool.MaxTxsLimitPerBlock != 5000 {
		t.Fatalf("local20 TxPool stress profile mismatch: %#v", cfg.TxPool)
	}
	if cfg.Sender.QueueCapacity != 5000 ||
		cfg.Sender.TxGossipFanout != 20 ||
		cfg.Sender.TxGossipInputCapacity != 50000 ||
		cfg.Sender.TxGossipMaxPendingPerPeer != 20000 {
		t.Fatalf("local20 Sender stress profile mismatch: %#v", cfg.Sender)
	}
	if cfg.PeerGate.TrustedRPS != 5000 || cfg.PeerGate.TrustedBurst != 10000 ||
		cfg.PeerGate.SyncRPS != 5000 || cfg.PeerGate.SyncBurst != 10000 ||
		cfg.PeerGate.UnknownRPS != 5000 || cfg.PeerGate.UnknownBurst != 10000 ||
		cfg.PeerGate.BanThreshold != 100000 {
		t.Fatalf("local20 PeerGate stress profile mismatch: %#v", cfg.PeerGate)
	}
	if !cfg.Server.EnableDebugAPI || cfg.Server.EnablePprof {
		t.Fatal("local20 must enable authenticated diagnostics while keeping pprof disabled")
	}
	if len(cfg.Discovery.BootstrapPeers) != 0 {
		t.Fatal("local20 bootstrap routing must remain in the public peer manifest")
	}
}
