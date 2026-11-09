package main

import (
	"bytes"
	"context"
	"dex/config"
	"dex/consensus"
	"dex/db"
	"dex/keys"
	"dex/logs"
	"dex/sender"
	"dex/vm"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewNodeLauncherPropagatesSecurityOptionsAndConsumesWrapKey(t *testing.T) {
	consensus.ClearNodePublicKeys()
	t.Cleanup(consensus.ClearNodePublicKeys)
	cfg := config.DefaultConfig()
	cfg.Server.EnableDebugAPI = true
	cfg.Server.EnablePprof = true
	cfg.Server.PprofBind = "127.0.0.2"
	cfg.Arbitrage.Enabled = true
	arbitrageKey := testPrivateKeyForIndex(19)
	_, arbitrageIdentity, err := deriveTestPeerIdentity(arbitrageKey)
	if err != nil {
		t.Fatal(err)
	}

	wrapKey := bytes.Repeat([]byte{0x6d}, frostWrapKeyBytes)
	wantWrapKey := append([]byte(nil), wrapKey...)
	genesisPeer := testPeerWithIdentity(t, 7, true)
	genesisPeer.Port = "6007"
	genesisPeer.AdvertiseAddr = "validator.example:6007"
	wantGenesisBLS := append([]byte(nil), genesisPeer.BLSPublicKey...)
	minerIdentity := testRuntimeKey(t, "01")
	launcher, err := NewNodeLauncher(NodeConfig{
		ID:                       0,
		KeyManager:               minerIdentity,
		ArbitrageKeyManager:      testRuntimeKey(t, arbitrageKey),
		ArbitrageAddress:         arbitrageIdentity.Address,
		Port:                     "6000",
		DataPath:                 t.TempDir(),
		Config:                   cfg,
		GenesisPeers:             []PeerInfo{genesisPeer},
		BootstrapEndpoints:       []BootstrapEndpoint{{Address: genesisPeer.Address, Endpoint: genesisPeer.AdvertiseAddr}},
		FrostWrapKey:             wrapKey,
		EnableOperatorControlAPI: true,
		ResumeFromExisting:       true,
	})
	if err != nil {
		t.Fatalf("NewNodeLauncher: %v", err)
	}
	node := launcher.Instance()
	if !node.EnableDebugAPI || !node.EnableOperatorControlAPI || !node.EnablePprof || node.PprofBind != "127.0.0.2" {
		t.Fatalf("security options not propagated: debug=%v operator=%v pprof=%v bind=%q", node.EnableDebugAPI, node.EnableOperatorControlAPI, node.EnablePprof, node.PprofBind)
	}
	if !bytes.Equal(node.FrostWrapKey, wantWrapKey) {
		t.Fatal("node did not receive an independent wrap-key copy")
	}
	if !bytes.Equal(wrapKey, make([]byte, len(wrapKey))) {
		t.Fatal("inbound wrap-key buffer was not consumed and cleared")
	}
	if len(launcher.config.FrostWrapKey) != 0 {
		t.Fatal("launcher config retained a wrap-key copy")
	}
	if launcher.config.KeyManager != nil {
		t.Fatal("launcher config retained a node private-key copy")
	}
	if launcher.config.ArbitrageKeyManager != nil {
		t.Fatal("launcher config retained an arbitrage private-key copy")
	}
	if node.KeyManager != minerIdentity {
		t.Fatal("launcher replaced the shared runtime key owner")
	}
	if node.ArbitrageAddress != arbitrageIdentity.Address || node.ArbitrageKeyManager == nil {
		t.Fatalf("node did not receive independent arbitrage identity: address=%q key_present=%v", node.ArbitrageAddress, node.ArbitrageKeyManager != nil)
	}
	if len(launcher.config.GenesisPeers) != 1 {
		t.Fatal("launcher did not retain the genesis trust manifest")
	}
	if launcher.config.GenesisPeers[0].AdvertiseAddr != "" || launcher.config.GenesisPeers[0].Port != "" {
		t.Fatal("replaceable endpoint metadata leaked into the immutable genesis trust records")
	}
	if len(launcher.config.BootstrapEndpoints) != 1 || launcher.config.BootstrapEndpoints[0].Endpoint != "validator.example:6007" {
		t.Fatal("launcher did not retain the structured bootstrap route")
	}
	if cached, ok := consensus.GetNodePublicKey(genesisPeer.Address); !ok || !bytes.Equal(cached, genesisPeer.SecpPublicKey) {
		t.Fatal("launcher did not seed the in-memory consensus key cache from the genesis manifest")
	}
	genesisPeer.BLSPublicKey[0] ^= 0xff
	if !bytes.Equal(launcher.config.GenesisPeers[0].BLSPublicKey, wantGenesisBLS) {
		t.Fatal("launcher did not retain an independent public genesis manifest")
	}
	wipeBytes(node.FrostWrapKey)
}

func TestNewNodeLauncherRejectsMismatchedArbitrageIdentity(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Arbitrage.Enabled = true
	_, other, err := deriveTestPeerIdentity(testPrivateKeyForIndex(21))
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewNodeLauncher(NodeConfig{
		KeyManager:          testRuntimeKey(t, "01"),
		ArbitrageKeyManager: testRuntimeKey(t, testPrivateKeyForIndex(20)),
		ArbitrageAddress:    other.Address,
		DataPath:            t.TempDir(),
		ResumeFromExisting:  true,
		Config:              cfg,
	})
	if err == nil || !strings.Contains(err.Error(), "not configured runtime address") {
		t.Fatalf("mismatched arbitrage identity error = %v", err)
	}
}

func TestNewNodeLauncherRejectsMinerKeyAsArbitrageKey(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Arbitrage.Enabled = true
	sharedKey := testPrivateKeyForIndex(24)
	_, sharedIdentity, err := deriveTestPeerIdentity(sharedKey)
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewNodeLauncher(NodeConfig{
		KeyManager:          testRuntimeKey(t, sharedKey),
		ArbitrageKeyManager: testRuntimeKey(t, sharedKey),
		ArbitrageAddress:    sharedIdentity.Address,
		DataPath:            t.TempDir(),
		ResumeFromExisting:  true,
		Config:              cfg,
	})
	if err == nil || !strings.Contains(err.Error(), "must differ from miner address") {
		t.Fatalf("shared miner/arbitrage key accepted: %v", err)
	}
}

func TestValidateNodeDataPathNeverDeletesExistingData(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "node-data")
	if err := os.Mkdir(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(dataDir, "frost-share.sentinel")
	if err := os.WriteFile(sentinel, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := validateNodeDataPath(dataDir, false); err == nil {
		t.Fatal("non-empty data directory was accepted without --resume")
	}
	if got, err := os.ReadFile(sentinel); err != nil || string(got) != "preserve" {
		t.Fatalf("existing data was changed: got=%q err=%v", got, err)
	}
	resolved, err := validateNodeDataPath(dataDir, true)
	if err != nil {
		t.Fatalf("resume rejected an existing node directory: %v", err)
	}
	if !filepath.IsAbs(resolved) {
		t.Fatalf("resolved data path is not absolute: %s", resolved)
	}

	emptyDir := filepath.Join(root, "empty")
	if err := os.Mkdir(emptyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := validateNodeDataPath(emptyDir, false); err != nil {
		t.Fatalf("empty data directory rejected: %v", err)
	}
	if _, err := validateNodeDataPath(filepath.Join(root, "new"), true); err != nil {
		t.Fatalf("new data directory rejected: %v", err)
	}
}

func TestInstallBootstrapRoutingNeedsNoAccountState(t *testing.T) {
	peer := testPeerWithIdentity(t, 9, true)
	endpoint := "validator.example:6009"
	cfg := config.DefaultConfig()
	logger := logs.NewNodeLogger(t.Name(), 0)
	dbManager, err := db.NewManager(t.TempDir(), logger)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	dbManager.InitWriteQueue(128, time.Hour)
	t.Cleanup(dbManager.Close)
	senderManager := sender.NewSenderManager(
		dbManager,
		"observer",
		"observer.example:7000",
		nil,
		0,
		logger,
		cfg,
	)
	t.Cleanup(senderManager.Stop)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	transport, ok := consensus.NewRealTransport("observer", dbManager, senderManager, ctx).(*consensus.RealTransport)
	if !ok {
		t.Fatal("NewRealTransport returned an unexpected transport implementation")
	}

	node := &NodeInstance{
		SenderManager: senderManager,
		ConsensusManager: &consensus.ConsensusNodeManager{
			Transport: transport,
		},
	}
	if err := installBootstrapRouting(node, []BootstrapEndpoint{{
		Address:  peer.Address,
		Endpoint: endpoint,
	}}); err != nil {
		t.Fatalf("installBootstrapRouting without DB state: %v", err)
	}
	peers := transport.GetAllPeers("observer")
	if len(peers) != 1 || string(peers[0]) != peer.Address {
		t.Fatalf("empty verified state did not expose bootstrap fallback: %v", peers)
	}
}

func TestInstallBootstrapRoutingRejectsAmbiguousEndpointBinding(t *testing.T) {
	first := testPeerWithIdentity(t, 10, true)
	second := testPeerWithIdentity(t, 11, true)
	cfg := config.DefaultConfig()
	senderManager := sender.NewSenderManager(
		nil,
		"observer",
		"observer.example:7000",
		nil,
		0,
		logs.NewNodeLogger(t.Name(), 0),
		cfg,
	)
	t.Cleanup(senderManager.Stop)
	node := &NodeInstance{
		SenderManager: senderManager,
		ConsensusManager: &consensus.ConsensusNodeManager{
			Transport: &consensus.RealTransport{},
		},
	}

	err := installBootstrapRouting(node, []BootstrapEndpoint{
		{Address: first.Address, Endpoint: "shared.example:6000"},
		{Address: second.Address, Endpoint: "shared.example:6000"},
	})
	if err == nil {
		t.Fatal("one endpoint was accepted for two bootstrap identities")
	}
}

func TestNodeLauncherStartFreshLateJoinSeedsTrustedGenesisBeforeNetworking(t *testing.T) {
	consensus.ClearNodePublicKeys()
	t.Cleanup(consensus.ClearNodePublicKeys)

	peers := make([]PeerInfo, 5)
	bootstrap := make([]BootstrapEndpoint, len(peers))
	for i := range peers {
		peers[i] = testPeerWithIdentity(t, i, true)
		peers[i].Port = fmt.Sprintf("%d", 6100+i)
		peers[i].AdvertiseAddr = net.JoinHostPort("127.0.0.1", peers[i].Port)
		bootstrap[i] = BootstrapEndpoint{Address: peers[i].Address, Endpoint: peers[i].AdvertiseAddr}
	}
	genesisCfg := config.DefaultGenesisConfig()
	genesisCfg.Alloc = map[string]config.GenesisAlloc{
		"default": {Balances: map[string]config.GenesisBalance{
			"FB": {Balance: "1000000000", MinerLockedBalance: vm.MinMinerStake.String()},
		}},
	}
	frostCfg := config.DefaultFrostConfig()
	frostCfg.Enabled = false
	frostCfg.Chains = map[string]config.ChainFrostConfig{}
	cfg := config.DefaultConfig()
	cfg.Frost = frostCfg
	cfg.Server.EnablePprof = false
	cfg.Server.EnableDebugAPI = false

	observerKey := testPrivateKeyForIndex(99)
	_, observerIdentity, err := deriveTestPeerIdentity(observerKey)
	if err != nil {
		t.Fatal(err)
	}
	launcher, err := NewNodeLauncher(NodeConfig{
		KeyManager:         testRuntimeKey(t, observerKey),
		Port:               "0",
		AdvertiseAddr:      "127.0.0.1:0",
		DataPath:           filepath.Join(t.TempDir(), "observer-data"),
		BootstrapEndpoints: bootstrap,
		GenesisPeers:       peers,
		Mode:               LateJoin,
		Config:             cfg,
		GenesisConfig:      genesisCfg,
		FrostConfig:        frostCfg,
	})
	if err != nil {
		t.Fatalf("NewNodeLauncher: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := launcher.Start(ctx, peers); err != nil {
		t.Fatalf("Start fresh latejoin: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		_ = launcher.Stop(stopCtx)
	})

	node := launcher.Instance()
	if raw, err := node.DBManager.Get(keys.KeyAccount(observerIdentity.Address)); err == nil && len(raw) > 0 {
		t.Fatal("fresh latejoin observer was granted a genesis account")
	}
	rs, ok := node.ConsensusManager.Node.GetBlockStore().(*consensus.RealBlockStore)
	if !ok || rs.GetExecutor() == nil || rs.GetExecutor().LivenessTracker == nil {
		t.Fatal("latejoin liveness tracker unavailable after Start")
	}
	snapshot, ok := rs.GetExecutor().LivenessTracker.(interface {
		GetConsensusVoterSnapshot() (uint64, int)
	})
	if !ok {
		t.Fatalf("unexpected liveness tracker %T", rs.GetExecutor().LivenessTracker)
	}
	if height, count := snapshot.GetConsensusVoterSnapshot(); height != 0 || count != len(peers) {
		t.Fatalf("latejoin voter snapshot=(%d,%d), want (0,%d)", height, count, len(peers))
	}
}
