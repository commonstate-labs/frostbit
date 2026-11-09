package sender

import (
	"dex/db"
	"dex/logs"
	"dex/pb"
	"testing"
	"time"
)

func TestApplyMinerIPMapCannotOverrideVerifiedAccountEndpoint(t *testing.T) {
	dbManager, err := db.NewManager(t.TempDir(), logs.NewNodeLogger("discovery-routing-test", 0))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	dbManager.InitWriteQueue(128, time.Hour)
	t.Cleanup(func() { dbManager.Close() })

	const (
		address    = "miner-address"
		verifiedIP = "10.0.0.8:8443"
		attackerIP = "203.0.113.99:9443"
	)
	if err := dbManager.SaveAccount(&pb.Account{
		Address: address,
		Ip:      verifiedIP,
		IsMiner: true,
	}); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}
	if err := dbManager.ForceFlushHot(); err != nil {
		t.Fatalf("ForceFlushHot: %v", err)
	}

	sm := &SenderManager{
		dbManager:    dbManager,
		minerIPCache: make(map[string]string),
	}
	sm.SetMinerIP(address, verifiedIP)

	updated, err := sm.applyMinerIPMap(&pb.GetMinerIPMapResponse{
		Height:  10,
		Entries: []*pb.MinerIPEntry{{Address: address, Ip: attackerIP}},
	})
	if err != nil {
		t.Fatalf("applyMinerIPMap: %v", err)
	}
	if updated != 0 {
		t.Fatalf("unsigned redirect reported %d updates, want 0", updated)
	}
	if got := sm.LookupMinerIP(address); got != verifiedIP {
		t.Fatalf("unsigned discovery redirected verified miner: got %q want %q", got, verifiedIP)
	}

	sm.minerIPMu.RLock()
	cached := sm.minerIPCache[address]
	sm.minerIPMu.RUnlock()
	if cached != verifiedIP {
		t.Fatalf("route cache changed to unverified endpoint: got %q want %q", cached, verifiedIP)
	}
}

func TestApplyMinerIPMapOnlyCachesEndpointMatchingVerifiedState(t *testing.T) {
	dbManager, err := db.NewManager(t.TempDir(), logs.NewNodeLogger("discovery-routing-match-test", 0))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	dbManager.InitWriteQueue(128, time.Hour)
	t.Cleanup(func() { dbManager.Close() })

	const (
		address    = "miner-address"
		verifiedIP = "10.0.0.8:8443"
	)
	if err := dbManager.SaveAccount(&pb.Account{
		Address: address,
		Ip:      verifiedIP,
		IsMiner: true,
	}); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}
	if err := dbManager.ForceFlushHot(); err != nil {
		t.Fatalf("ForceFlushHot: %v", err)
	}

	sm := &SenderManager{
		dbManager:    dbManager,
		minerIPCache: map[string]string{address: "192.0.2.1:1"},
	}
	updated, err := sm.applyMinerIPMap(&pb.GetMinerIPMapResponse{
		Height:  10,
		Entries: []*pb.MinerIPEntry{{Address: address, Ip: verifiedIP}},
	})
	if err != nil {
		t.Fatalf("applyMinerIPMap: %v", err)
	}
	if updated != 1 {
		t.Fatalf("matching verified endpoint updates=%d want 1", updated)
	}
	if got := sm.LookupMinerIP(address); got != verifiedIP {
		t.Fatalf("verified endpoint was not restored: got %q want %q", got, verifiedIP)
	}
}
