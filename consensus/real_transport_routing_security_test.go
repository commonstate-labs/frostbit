package consensus

import (
	"dex/db"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"dex/types"
	"testing"
	"time"
)

func TestRealTransportVerifiedRouteOverridesBootstrapAndCache(t *testing.T) {
	dbManager := newRoutingTestDB(t)
	installRoutingTestMiner(t, dbManager, 1, &pb.Account{
		Address: "verified-miner",
		Ip:      "10.0.0.8:8443",
		Index:   1,
		IsMiner: true,
	})

	rt := &RealTransport{
		dbManager:      dbManager,
		nodeIPCache:    map[types.NodeID]string{"verified-miner": "192.0.2.44:9443"},
		bootstrapPeers: make(map[types.NodeID]string),
		Logger:         logs.NewNodeLogger("verified-route-test", 0),
	}
	rt.SetBootstrapPeers(map[types.NodeID]string{
		"verified-miner": "203.0.113.99:9443",
		"unknown-peer":   "203.0.113.100:9443",
	})

	got, err := rt.getNodeIP("verified-miner")
	if err != nil {
		t.Fatalf("getNodeIP(verified-miner): %v", err)
	}
	if got != "10.0.0.8:8443" {
		t.Fatalf("unverified route overrode chain endpoint: got %q", got)
	}
	if _, err := rt.getNodeIP("unknown-peer"); err == nil {
		t.Fatal("bootstrap route remained usable after verified miner set was installed")
	}

	all := rt.GetAllPeers("")
	if len(all) != 1 || all[0] != "verified-miner" {
		t.Fatalf("GetAllPeers mixed bootstrap with verified members: %v", all)
	}
	sampled := rt.SamplePeers("", 10)
	if len(sampled) != 1 || sampled[0] != "verified-miner" {
		t.Fatalf("SamplePeers mixed bootstrap with verified members: %v", sampled)
	}
}

func TestRealTransportVerifiedEmptyRouteUsesOnlyLocalManifest(t *testing.T) {
	dbManager := newRoutingTestDB(t)
	installRoutingTestMiner(t, dbManager, 1, &pb.Account{
		Address: "genesis-miner",
		Index:   1,
		IsMiner: true,
	})

	rt := &RealTransport{
		dbManager: dbManager,
		nodeIPCache: map[types.NodeID]string{
			"genesis-miner": "192.0.2.44:9443",
		},
		bootstrapPeers: make(map[types.NodeID]string),
		Logger:         logs.NewNodeLogger("genesis-route-test", 0),
	}
	rt.SetBootstrapPeers(map[types.NodeID]string{
		"genesis-miner": "203.0.113.10:8443",
	})

	got, err := rt.getNodeIP("genesis-miner")
	if err != nil {
		t.Fatalf("getNodeIP(genesis-miner): %v", err)
	}
	if got != "203.0.113.10:8443" {
		t.Fatalf("unsigned cache overrode local manifest route: got %q", got)
	}

	                                                                        
	                                                                         
	rt.SetBootstrapPeers(map[types.NodeID]string{
		"genesis-miner": "203.0.113.11:9443",
	})
	got, err = rt.getNodeIP("genesis-miner")
	if err != nil {
		t.Fatalf("getNodeIP(genesis-miner) after rotation: %v", err)
	}
	if got != "203.0.113.11:9443" {
		t.Fatalf("rotated manifest route not used for dialing: got %q", got)
	}
	account, err := dbManager.GetAccount("genesis-miner")
	if err != nil {
		t.Fatalf("GetAccount(genesis-miner): %v", err)
	}
	if account.GetIp() != "" {
		t.Fatalf("process-local route leaked into finalized account: %q", account.GetIp())
	}

	                                                                         
	                                                                   
	rt.SetBootstrapPeers(nil)
	if _, err := rt.getNodeIP("genesis-miner"); err == nil {
		t.Fatal("verified miner without chain or manifest endpoint used an unsigned cache")
	}
}

func TestRealTransportUsesBootstrapOnlyWithEmptyVerifiedSet(t *testing.T) {
	dbManager := newRoutingTestDB(t)
	rt := &RealTransport{
		dbManager:      dbManager,
		nodeIPCache:    make(map[types.NodeID]string),
		bootstrapPeers: make(map[types.NodeID]string),
		Logger:         logs.NewNodeLogger("bootstrap-route-test", 0),
	}
	rt.SetBootstrapPeers(map[types.NodeID]string{
		"bootstrap-b": "10.0.0.2:8443",
		"bootstrap-a": "10.0.0.1:8443",
	})

	got, err := rt.getNodeIP("bootstrap-a")
	if err != nil {
		t.Fatalf("getNodeIP(bootstrap-a): %v", err)
	}
	if got != "10.0.0.1:8443" {
		t.Fatalf("bootstrap endpoint=%q want %q", got, "10.0.0.1:8443")
	}
	all := rt.GetAllPeers("")
	if len(all) != 2 || all[0] != "bootstrap-a" || all[1] != "bootstrap-b" {
		t.Fatalf("GetAllPeers bootstrap fallback=%v", all)
	}
	sampled := rt.SamplePeers("", 1)
	if len(sampled) != 1 || sampled[0] != "bootstrap-a" {
		t.Fatalf("SamplePeers bootstrap fallback=%v", sampled)
	}
}

func TestRealTransportUsesVerifiedServiceEndpointOnlyForSyncRequest(t *testing.T) {
	dbManager := newRoutingTestDB(t)
	installRoutingTestMiner(t, dbManager, 1, &pb.Account{
		Address: "verified-miner",
		Ip:      "10.0.0.8:8443",
		Index:   1,
		IsMiner: true,
	})
	serviceStore, serviceID := newVerifiedFrontierServicePeer(t, "8.8.4.4:19000", 1, 100, 3)
	rt := &RealTransport{
		dbManager:      dbManager,
		nodeIPCache:    make(map[types.NodeID]string),
		bootstrapPeers: make(map[types.NodeID]string),
		Logger:         logs.NewNodeLogger("service-route-test", 0),
	}
	rt.SetServicePeerStore(serviceStore)

	if _, err := rt.getNodeIP(serviceID); err == nil {
		t.Fatal("generic getNodeIP admitted a non-miner service identity")
	}
	if _, err := rt.resolveMessageEndpoint(serviceID, types.Message{Type: types.MsgHeightQuery, ServiceSync: true}); err == nil {
		t.Fatal("non-sync message used the service endpoint")
	}
	if _, err := rt.resolveMessageEndpoint(serviceID, types.Message{Type: types.MsgSyncRequest}); err == nil {
		t.Fatal("ordinary miner sync used the service endpoint")
	}
	got, err := rt.resolveMessageEndpoint(serviceID, types.Message{Type: types.MsgSyncRequest, ServiceSync: true})
	if err != nil {
		t.Fatalf("resolve sync service endpoint: %v", err)
	}
	if got != "8.8.4.4:19000" {
		t.Fatalf("sync service endpoint=%q want %q", got, "8.8.4.4:19000")
	}
}

func TestRealTransportServiceSyncUsesVerifiedRecordEvenWhenMinerEndpointExists(t *testing.T) {
	dbManager := newRoutingTestDB(t)
	serviceStore, serviceID := newVerifiedFrontierServicePeer(t, "1.0.0.1:19000", 1, 100, 3)
	installRoutingTestMiner(t, dbManager, 1, &pb.Account{
		Address: string(serviceID),
		Ip:      "10.0.0.9:8443",
		Index:   1,
		IsMiner: true,
	})
	rt := &RealTransport{
		dbManager:      dbManager,
		nodeIPCache:    make(map[types.NodeID]string),
		bootstrapPeers: make(map[types.NodeID]string),
		Logger:         logs.NewNodeLogger("miner-service-route-test", 0),
	}
	rt.SetServicePeerStore(serviceStore)

	got, err := rt.resolveMessageEndpoint(serviceID, types.Message{Type: types.MsgSyncRequest, ServiceSync: true})
	if err != nil {
		t.Fatalf("resolve service sync endpoint: %v", err)
	}
	if got != "1.0.0.1:19000" {
		t.Fatalf("miner chain endpoint overrode verified service route: got=%q", got)
	}
	got, err = rt.resolveMessageEndpoint(serviceID, types.Message{Type: types.MsgSyncRequest})
	if err != nil {
		t.Fatalf("resolve ordinary miner sync endpoint: %v", err)
	}
	if got != "10.0.0.9:8443" {
		t.Fatalf("ordinary miner sync did not retain authoritative miner route: got=%q", got)
	}
}

func TestConsensusNodeManagerWiresServicePeerStoreToSyncAndTransport(t *testing.T) {
	serviceStore, _ := newVerifiedFrontierServicePeer(t, "4.2.2.2:19000", 1, 100, 3)
	sm := newFrontierCoreSyncManager(newFrontierCoreTestStore("block-5", 5), nil, 1, 1)
	rt := &RealTransport{}
	manager := &ConsensusNodeManager{
		Node:      &Node{SyncManager: sm},
		Transport: rt,
	}

	manager.SetServicePeerStore(serviceStore)
	if sm.servicePeerStore() != serviceStore {
		t.Fatal("manager did not wire service store into SyncManager")
	}
	rt.mu.RLock()
	wired := rt.servicePeers
	rt.mu.RUnlock()
	if wired != serviceStore {
		t.Fatal("manager did not wire service store into RealTransport")
	}
}

func newRoutingTestDB(t *testing.T) *db.Manager {
	t.Helper()
	dbManager, err := db.NewManager(t.TempDir(), logs.NewNodeLogger("routing-db-test", 0))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	dbManager.InitWriteQueue(128, time.Hour)
	t.Cleanup(func() { dbManager.Close() })
	return dbManager
}

func installRoutingTestMiner(t *testing.T, dbManager *db.Manager, index uint64, account *pb.Account) {
	t.Helper()
	if err := dbManager.SaveAccount(account); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}
	dbManager.EnqueueSet(keys.KeyIndexToAccount(index), keys.KeyAccount(account.Address))
	if err := dbManager.ForceFlushHot(); err != nil {
		t.Fatalf("ForceFlushHot: %v", err)
	}
	if err := dbManager.IndexMgr.RebuildBitmapFromDB(); err != nil {
		t.Fatalf("RebuildBitmapFromDB: %v", err)
	}
	dbManager.InvalidateMinerCache()
}
