package main

import (
	"bytes"
	"dex/config"
	"dex/db"
	bridgeasset "dex/frost/asset"
	"dex/frost/core/curve"
	"dex/keys"
	"dex/liveness"
	"dex/logs"
	"dex/pb"
	"dex/vm"
	"encoding/hex"
	"fmt"
	"math/big"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
)

func TestGenesisBootstrapIndexesCanonicalBridgeAsset(t *testing.T) {
	identity, err := bridgeasset.Resolve("eth", "0xdAC17F958D2ee523a2206206994597C13D831ec7")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := db.NewManager(filepath.Join(t.TempDir(), "db"), logs.NewNodeLogger("genesis-asset-index", 0))
	if err != nil {
		t.Fatal(err)
	}
	manager.InitWriteQueue(128, time.Hour)
	t.Cleanup(manager.Close)
	if err := initGenesisTokens(manager, config.DefaultGenesisConfig()); err != nil {
		t.Fatalf("init genesis tokens: %v", err)
	}
	if err := manager.ForceFlush(); err != nil {
		t.Fatal(err)
	}
	raw, err := manager.Get(keys.KeyToken(identity.ID))
	if err != nil {
		t.Fatalf("read genesis ETH.USDT: %v", err)
	}
	var token pb.Token
	if err := proto.Unmarshal(raw, &token); err != nil {
		t.Fatal(err)
	}
	if token.Address != identity.ID || token.NativeChain != identity.Chain || token.NativeContract != identity.Contract || token.Type != pb.TokenType_TOKEN_BRIDGED {
		t.Fatalf("genesis ETH.USDT identity mismatch: %+v want %+v", &token, identity)
	}
	indexed, err := manager.Get(keys.KeyNativeTokenIndex(identity.Chain, identity.Contract))
	if err != nil || string(indexed) != identity.ID {
		t.Fatalf("genesis ETH.USDT native index=%q err=%v want=%s", indexed, err, identity.ID)
	}
}

func testPeerWithIdentity(t *testing.T, index int, isMiner bool) PeerInfo {
	t.Helper()
	_, peer, err := deriveTestPeerIdentity(testPrivateKeyForIndex(index))
	if err != nil {
		t.Fatalf("derive peer %d: %v", index, err)
	}
	peer.Index = index
	peer.IsMiner = isMiner
	peer.RewardAddress = peer.Address
	return peer
}

func testPrivateKeyForIndex(index int) string {
	return fmt.Sprintf("%064x", index+1)
}

func TestBuildTrustedGenesisCertMinerSetUsesOnlyConfiguredGenesisMiners(t *testing.T) {
	miners := []PeerInfo{
		testPeerWithIdentity(t, 2, true),
		testPeerWithIdentity(t, 0, true),
		testPeerWithIdentity(t, 1, false),
	}
	cfg := config.DefaultGenesisConfig()
	cfg.Alloc["default"] = config.GenesisAlloc{Balances: map[string]config.GenesisBalance{
		"FB": {Balance: "1000000", MinerLockedBalance: "3200000000"},
	}}
	cfg.Alloc[miners[0].Address] = config.GenesisAlloc{Balances: map[string]config.GenesisBalance{
		"FB": {Balance: "1000000", MinerLockedBalance: "3200000900"},
	}}

	got, err := buildTrustedGenesisCertMinerSet(miners, cfg)
	if err != nil {
		t.Fatalf("build trusted genesis set: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("genesis validator count = %d, want 2", len(got))
	}
	if got[0].Index != 0 || got[0].EffectiveWeight != 3200000000 {
		t.Fatalf("validator[0] = %+v", got[0])
	}
	if got[1].Index != 2 || got[1].EffectiveWeight != 3200000900 {
		t.Fatalf("validator[1] = %+v", got[1])
	}
}

func TestBuildTrustedGenesisCertMinerSetFailsClosedWithoutManifestOrStake(t *testing.T) {
	cfg := config.DefaultGenesisConfig()
	if _, err := buildTrustedGenesisCertMinerSet(nil, cfg); err == nil {
		t.Fatal("missing local genesis validator manifest was accepted")
	}

	miner := testPeerWithIdentity(t, 0, true)
	cfg.Alloc["default"] = config.GenesisAlloc{Balances: map[string]config.GenesisBalance{
		"FB": {Balance: "1000000", MinerLockedBalance: "0"},
	}}
	if _, err := buildTrustedGenesisCertMinerSet([]PeerInfo{miner}, cfg); err == nil {
		t.Fatal("zero genesis validator stake was accepted")
	}
}

func TestFreshLateJoinReconstructsGenesisStateWithTrustedEndpoints(t *testing.T) {
	peers := make([]PeerInfo, 5)
	for i := range peers {
		peers[i] = testPeerWithIdentity(t, i, true)
		peers[i].Port = fmt.Sprintf("%d", 6000+i)
		peers[i].AdvertiseAddr = fmt.Sprintf("validator-%d.invalid:%d", i, 6000+i)
	}
	observer := testPeerWithIdentity(t, 99, true).Address

	genesisCfg := config.DefaultGenesisConfig()
	genesisCfg.Alloc = map[string]config.GenesisAlloc{
		"default": {Balances: map[string]config.GenesisBalance{
			"FB": {Balance: "1000000000", MinerLockedBalance: vm.MinMinerStake.String()},
		}},
	}
	frostCfg := testDualCustodyFrostConfig()
	frostCfg.Enabled = false
	frostCfg.Chains = map[string]config.ChainFrostConfig{}

	newNode := func(name, address string) *NodeInstance {
		t.Helper()
		logger := logs.NewNodeLogger(name, 0)
		manager, err := db.NewManager(filepath.Join(t.TempDir(), "db"), logger)
		if err != nil {
			t.Fatalf("NewManager(%s): %v", name, err)
		}
		manager.InitWriteQueue(128, time.Hour)
		t.Cleanup(manager.Close)
		return &NodeInstance{
			Address:       address,
			Port:          "7000",
			AdvertiseAddr: "observer.invalid:7000",
			DBManager:     manager,
		}
	}

	genesisNode := newNode("genesis-state-source", peers[0].Address)
	lateJoinNode := newNode("latejoin-state-source", observer)
	if err := registerPeersLocally(genesisNode, peers, genesisCfg, frostCfg); err != nil {
		t.Fatalf("genesis state seed: %v", err)
	}
	if err := seedTrustedGenesisState(lateJoinNode, peers, genesisCfg, frostCfg, false); err != nil {
		t.Fatalf("latejoin state seed: %v", err)
	}
	if raw, err := lateJoinNode.DBManager.Get(keys.KeyAccount(observer)); err == nil && len(raw) > 0 {
		t.Fatal("latejoin observer was incorrectly inserted into genesis state")
	}
	if !bytes.Equal(genesisNode.DBManager.GetStateFingerprint(), lateJoinNode.DBManager.GetStateFingerprint()) {
		t.Fatal("genesis and latejoin nodes built different height-0 StateDB from the same trusted manifest")
	}
	changedPeers := append([]PeerInfo(nil), peers...)
	changedPeers[0].AdvertiseAddr = "changed-validator-0.invalid:9000"
	changedManifestNode := newNode("changed-genesis-manifest", observer)
	if err := seedTrustedGenesisState(changedManifestNode, changedPeers, genesisCfg, frostCfg, false); err != nil {
		t.Fatalf("changed manifest state seed: %v", err)
	}
	if bytes.Equal(genesisNode.DBManager.GetStateFingerprint(), changedManifestNode.DBManager.GetStateFingerprint()) {
		t.Fatal("changing a trusted genesis endpoint did not change height-0 StateDB")
	}
	for _, manager := range []*db.Manager{genesisNode.DBManager, lateJoinNode.DBManager} {
		for _, peer := range peers {
			account, err := manager.GetAccount(peer.Address)
			if err != nil {
				t.Fatalf("get genesis validator %s: %v", peer.Address, err)
			}
			if account == nil || account.Ip != peer.AdvertiseAddr {
				t.Fatalf("genesis validator %s endpoint = %q, want %q", peer.Address, account.GetIp(), peer.AdvertiseAddr)
			}
		}
		nodes, err := manager.GetAllNodeInfos()
		if err != nil {
			t.Fatalf("get genesis node infos: %v", err)
		}
		if len(nodes) != len(peers) {
			t.Fatalf("genesis NodeInfo count = %d, want %d", len(nodes), len(peers))
		}
		wantEndpoints := make(map[string]struct{}, len(peers))
		for _, peer := range peers {
			wantEndpoints[peer.AdvertiseAddr] = struct{}{}
		}
		for _, nodeInfo := range nodes {
			if _, exists := wantEndpoints[nodeInfo.GetIp()]; !exists {
				t.Fatalf("unexpected genesis NodeInfo endpoint %q", nodeInfo.GetIp())
			}
			delete(wantEndpoints, nodeInfo.GetIp())
		}
		if len(wantEndpoints) != 0 {
			t.Fatalf("missing genesis NodeInfo endpoints: %v", wantEndpoints)
		}
		gossipIPs, err := manager.GetRandomGossipIPs(len(peers), peers[0].AdvertiseAddr)
		if err != nil {
			t.Fatalf("sample genesis gossip endpoints: %v", err)
		}
		wantGossip := make(map[string]struct{}, len(peers)-1)
		for _, peer := range peers[1:] {
			wantGossip[peer.AdvertiseAddr] = struct{}{}
		}
		for _, endpoint := range gossipIPs {
			delete(wantGossip, endpoint)
		}
		if len(wantGossip) != 0 {
			t.Fatalf("genesis miner gossip cache is missing endpoints: %v", wantGossip)
		}
	}

	genesisTracker := liveness.NewTracker(liveness.DefaultConfig())
	genesisTracker.SetStore(genesisNode.DBManager)
	lateJoinTracker := liveness.NewTracker(liveness.DefaultConfig())
	lateJoinTracker.SetStore(lateJoinNode.DBManager)
	if err := bootstrapMinersToTracker(genesisTracker, genesisNode.DBManager); err != nil {
		t.Fatalf("bootstrap genesis liveness: %v", err)
	}
	if err := bootstrapMinersToTracker(lateJoinTracker, lateJoinNode.DBManager); err != nil {
		t.Fatalf("bootstrap latejoin liveness: %v", err)
	}
	if err := genesisNode.DBManager.ForceFlush(); err != nil {
		t.Fatal(err)
	}
	if err := lateJoinNode.DBManager.ForceFlush(); err != nil {
		t.Fatal(err)
	}
	if height, count := lateJoinTracker.GetConsensusVoterSnapshot(); height != 0 || count != len(peers) {
		t.Fatalf("latejoin genesis voter snapshot=(height=%d,count=%d), want (0,%d)", height, count, len(peers))
	}
	if !bytes.Equal(genesisNode.DBManager.GetStateFingerprint(), lateJoinNode.DBManager.GetStateFingerprint()) {
		t.Fatal("latejoin liveness bootstrap produced a different height-0 state")
	}
}

func TestSeedTrustedGenesisStateMaterializesOrdinaryAddressAllocations(t *testing.T) {
	peer := testPeerWithIdentity(t, 0, true)
	peer.AdvertiseAddr = "validator.invalid:6000"
	ordinary := testPeerWithIdentity(t, 40, false).Address
	genesisCfg := config.DefaultGenesisConfig()
	genesisCfg.Alloc = map[string]config.GenesisAlloc{
		peer.Address: {Balances: map[string]config.GenesisBalance{
			"FB": {Balance: "1000000000", MinerLockedBalance: vm.MinMinerStake.String()},
		}},
		ordinary: {Balances: map[string]config.GenesisBalance{
			"FB": {Balance: "123456"},
		}},
	}
	frostCfg := testDualCustodyFrostConfig()
	frostCfg.Enabled = false
	frostCfg.Chains = map[string]config.ChainFrostConfig{}
	logger := logs.NewNodeLogger("ordinary-genesis-account", 0)
	manager, err := db.NewManager(filepath.Join(t.TempDir(), "db"), logger)
	if err != nil {
		t.Fatal(err)
	}
	manager.InitWriteQueue(128, time.Hour)
	t.Cleanup(manager.Close)
	node := &NodeInstance{Address: peer.Address, Port: "6000", AdvertiseAddr: peer.AdvertiseAddr, DBManager: manager}
	if err := registerPeersLocally(node, []PeerInfo{peer}, genesisCfg, frostCfg); err != nil {
		t.Fatalf("seed genesis: %v", err)
	}
	account, err := manager.GetAccount(ordinary)
	if err != nil {
		t.Fatalf("ordinary account was not materialized: %v", err)
	}
	if account.Address != ordinary || account.IsMiner {
		t.Fatalf("unexpected ordinary account: %#v", account)
	}
	for token, want := range map[string]string{"FB": "123456"} {
		raw, err := manager.Get(keys.KeyBalance(ordinary, token))
		if err != nil {
			t.Fatalf("read %s balance: %v", token, err)
		}
		var record pb.TokenBalanceRecord
		if err := proto.Unmarshal(raw, &record); err != nil {
			t.Fatal(err)
		}
		if record.Balance == nil || record.Balance.Balance != want || record.Balance.MinerLockedBalance != "" {
			t.Fatalf("ordinary %s balance=%#v want available=%s", token, record.Balance, want)
		}
	}
}

func TestSeedTrustedGenesisStateValidatesEveryOrdinaryAllocationBeforeWriting(t *testing.T) {
	peer := testPeerWithIdentity(t, 0, true)
	peer.AdvertiseAddr = "validator.invalid:6000"
	firstOrdinary := testPeerWithIdentity(t, 40, false).Address
	lastOrdinary := testPeerWithIdentity(t, 41, false).Address
	if firstOrdinary > lastOrdinary {
		firstOrdinary, lastOrdinary = lastOrdinary, firstOrdinary
	}

	genesisCfg := config.DefaultGenesisConfig()
	genesisCfg.Alloc = map[string]config.GenesisAlloc{
		peer.Address: {Balances: map[string]config.GenesisBalance{
			"FB": {Balance: "1000000000", MinerLockedBalance: vm.MinMinerStake.String()},
		}},
		firstOrdinary: {Balances: map[string]config.GenesisBalance{
			"FB": {Balance: "123456"},
		}},
		lastOrdinary: {Balances: map[string]config.GenesisBalance{
			"UNREGISTERED": {Balance: "1"},
		}},
	}
	frostCfg := testDualCustodyFrostConfig()
	frostCfg.Enabled = false
	frostCfg.Chains = map[string]config.ChainFrostConfig{}

	logger := logs.NewNodeLogger("ordinary-genesis-validation-atomicity", 0)
	manager, err := db.NewManager(filepath.Join(t.TempDir(), "db"), logger)
	if err != nil {
		t.Fatal(err)
	}
	manager.InitWriteQueue(128, time.Hour)
	t.Cleanup(manager.Close)
	node := &NodeInstance{Address: peer.Address, Port: "6000", AdvertiseAddr: peer.AdvertiseAddr, DBManager: manager}
	before := append([]byte(nil), manager.GetStateFingerprint()...)

	err = registerPeersLocally(node, []PeerInfo{peer}, genesisCfg, frostCfg)
	if err == nil || !strings.Contains(err.Error(), `references unregistered token "UNREGISTERED"`) {
		t.Fatalf("invalid later ordinary allocation error = %v", err)
	}
	if err := manager.ForceFlush(); err != nil {
		t.Fatalf("flush after rejected genesis: %v", err)
	}
	if after := manager.GetStateFingerprint(); !bytes.Equal(before, after) {
		t.Fatal("rejected ordinary allocation left a partially written height-0 state")
	}
	for _, key := range []string{
		keys.KeyAccount(peer.Address),
		keys.KeyAccount(firstOrdinary),
		keys.KeyBalance(firstOrdinary, "FB"),
		keys.KeyMinerBLSPublicKeyOwner(peer.BLSPublicKey),
	} {
		if raw, err := manager.Get(key); err == nil && len(raw) > 0 {
			t.Fatalf("rejected genesis unexpectedly persisted key %q", key)
		}
	}
}

func TestSeedTrustedGenesisStateRejectsAggregateAllocationAboveSupplyBeforeWriting(t *testing.T) {
	peer := testPeerWithIdentity(t, 0, true)
	peer.AdvertiseAddr = "validator.invalid:6000"
	ordinary := testPeerWithIdentity(t, 42, false).Address
	genesisCfg := config.DefaultGenesisConfig()
	fbSupply := new(big.Int).Add(new(big.Int).Set(vm.MinMinerStake), big.NewInt(100))
	foundFB := false
	for i := range genesisCfg.Tokens {
		if genesisCfg.Tokens[i].Address == "FB" {
			genesisCfg.Tokens[i].TotalSupply = fbSupply.String()
			foundFB = true
			break
		}
	}
	if !foundFB {
		t.Fatal("default genesis token registry is missing FB")
	}
	genesisCfg.Alloc = map[string]config.GenesisAlloc{
		peer.Address: {Balances: map[string]config.GenesisBalance{
			"FB": {Balance: "50", MinerLockedBalance: vm.MinMinerStake.String()},
		}},
		ordinary: {Balances: map[string]config.GenesisBalance{
			"FB": {Balance: "51"},
		}},
	}
	frostCfg := testDualCustodyFrostConfig()
	frostCfg.Enabled = false
	frostCfg.Chains = map[string]config.ChainFrostConfig{}

	manager, err := db.NewManager(filepath.Join(t.TempDir(), "db"), logs.NewNodeLogger("genesis-supply-atomicity", 0))
	if err != nil {
		t.Fatal(err)
	}
	manager.InitWriteQueue(128, time.Hour)
	t.Cleanup(manager.Close)
	node := &NodeInstance{Address: peer.Address, Port: "6000", AdvertiseAddr: peer.AdvertiseAddr, DBManager: manager}
	before := append([]byte(nil), manager.GetStateFingerprint()...)

	err = registerPeersLocally(node, []PeerInfo{peer}, genesisCfg, frostCfg)
	if err == nil || !strings.Contains(err.Error(), "token FB allocations total") || !strings.Contains(err.Error(), "exceeds total_supply") {
		t.Fatalf("aggregate genesis supply error = %v", err)
	}
	if err := manager.ForceFlush(); err != nil {
		t.Fatalf("flush after rejected over-allocation: %v", err)
	}
	if after := manager.GetStateFingerprint(); !bytes.Equal(before, after) {
		t.Fatal("over-allocated genesis left a partially written height-0 state")
	}
	for _, key := range []string{
		keys.KeyAccount(peer.Address),
		keys.KeyAccount(ordinary),
		keys.KeyBalance(ordinary, "FB"),
		keys.KeyMinerBLSPublicKeyOwner(peer.BLSPublicKey),
	} {
		if raw, err := manager.Get(key); err == nil && len(raw) > 0 {
			t.Fatalf("rejected over-allocation unexpectedly persisted key %q", key)
		}
	}
}

func TestGenesisBootstrapAndFreshLateJoinOrdinaryAllocationsHaveIdenticalHeightZeroState(t *testing.T) {
	peers := make([]PeerInfo, 3)
	for i := range peers {
		peers[i] = testPeerWithIdentity(t, i, true)
		peers[i].Port = fmt.Sprintf("%d", 6000+i)
		peers[i].AdvertiseAddr = fmt.Sprintf("validator-%d.invalid:%d", i, 6000+i)
	}
	ordinaryA := testPeerWithIdentity(t, 50, false).Address
	ordinaryB := testPeerWithIdentity(t, 51, false).Address
	observer := testPeerWithIdentity(t, 99, false).Address

	genesisCfg := config.DefaultGenesisConfig()
	genesisCfg.Alloc = map[string]config.GenesisAlloc{
		"default": {Balances: map[string]config.GenesisBalance{
			"FB": {Balance: "1000000000", MinerLockedBalance: vm.MinMinerStake.String()},
		}},
		ordinaryA: {Balances: map[string]config.GenesisBalance{
			"FB": {Balance: "111111"},
		}},
		ordinaryB: {Balances: map[string]config.GenesisBalance{
			"FB": {Balance: "444444"},
		}},
	}
	frostCfg := testDualCustodyFrostConfig()
	frostCfg.Enabled = false
	frostCfg.Chains = map[string]config.ChainFrostConfig{}

	newNode := func(name, address string) *NodeInstance {
		t.Helper()
		manager, err := db.NewManager(filepath.Join(t.TempDir(), "db"), logs.NewNodeLogger(name, 0))
		if err != nil {
			t.Fatalf("NewManager(%s): %v", name, err)
		}
		manager.InitWriteQueue(128, time.Hour)
		t.Cleanup(manager.Close)
		return &NodeInstance{
			Address:       address,
			Port:          "7000",
			AdvertiseAddr: "observer.invalid:7000",
			DBManager:     manager,
		}
	}

	genesisNode := newNode("ordinary-genesis-bootstrap", peers[0].Address)
	lateJoinNode := newNode("ordinary-fresh-latejoin", observer)
	if err := registerPeersLocally(genesisNode, peers, genesisCfg, frostCfg); err != nil {
		t.Fatalf("GenesisBootstrap seed: %v", err)
	}
	if err := seedTrustedGenesisState(lateJoinNode, peers, genesisCfg, frostCfg, false); err != nil {
		t.Fatalf("fresh LateJoin seed: %v", err)
	}
	if !bytes.Equal(genesisNode.DBManager.GetStateFingerprint(), lateJoinNode.DBManager.GetStateFingerprint()) {
		t.Fatal("GenesisBootstrap and fresh LateJoin built different height-0 state with ordinary allocations")
	}
	for _, manager := range []*db.Manager{genesisNode.DBManager, lateJoinNode.DBManager} {
		for _, address := range []string{ordinaryA, ordinaryB} {
			account, err := manager.GetAccount(address)
			if err != nil || account == nil || account.Address != address || account.IsMiner {
				t.Fatalf("ordinary account %s was not reproduced identically: account=%#v err=%v", address, account, err)
			}
		}
		if raw, err := manager.Get(keys.KeyAccount(observer)); err == nil && len(raw) > 0 {
			t.Fatal("fresh LateJoin observer was inserted into height-0 state")
		}
	}
}

func TestSeedTrustedGenesisStateRejectsMissingEndpoint(t *testing.T) {
	peer := testPeerWithIdentity(t, 0, true)
	genesisCfg := config.DefaultGenesisConfig()
	genesisCfg.Alloc = map[string]config.GenesisAlloc{
		"default": {Balances: map[string]config.GenesisBalance{
			"FB": {Balance: "1000000000", MinerLockedBalance: vm.MinMinerStake.String()},
		}},
	}
	frostCfg := testDualCustodyFrostConfig()
	frostCfg.Enabled = false
	frostCfg.Chains = map[string]config.ChainFrostConfig{}

	logger := logs.NewNodeLogger("missing-genesis-endpoint", 0)
	manager, err := db.NewManager(filepath.Join(t.TempDir(), "db"), logger)
	if err != nil {
		t.Fatal(err)
	}
	manager.InitWriteQueue(128, time.Hour)
	t.Cleanup(manager.Close)
	node := &NodeInstance{Address: peer.Address, Port: "6000", DBManager: manager}

	if err := registerPeersLocally(node, []PeerInfo{peer}, genesisCfg, frostCfg); err == nil || !strings.Contains(err.Error(), "requires advertise_addr") {
		t.Fatalf("missing trusted genesis endpoint error = %v", err)
	}
}

func newMapBackedStateView() (vm.StateView, map[string][]byte) {
	store := make(map[string][]byte)
	readFn := func(key string) ([]byte, error) {
		if val, ok := store[key]; ok {
			return append([]byte(nil), val...), nil
		}
		return nil, nil
	}
	scanFn := func(prefix string) (map[string][]byte, error) {
		out := make(map[string][]byte)
		for key, val := range store {
			if strings.HasPrefix(key, prefix) {
				out[key] = append([]byte(nil), val...)
			}
		}
		return out, nil
	}
	return vm.NewStateView(readFn, scanFn), store
}

func TestSelectFrostCandidatePeers_TopNAndDefaultK(t *testing.T) {
	peers := make([]PeerInfo, 0, 120)
	for i := 119; i >= 0; i-- {
		peer := testPeerWithIdentity(t, i, true)
		peers = append(peers, peer)
	}
	cfg := testDualCustodyFrostConfig()
	cfg.Committee.TopN = 100
	cfg.Vault.DefaultK = 20

	selected, err := selectFrostCandidatePeers(peers, cfg)
	if err != nil {
		t.Fatalf("selectFrostCandidatePeers failed: %v", err)
	}
	if len(selected) != 100 {
		t.Fatalf("selected candidates = %d, want 100", len(selected))
	}
	for i, p := range selected {
		if p.Index != i {
			t.Fatalf("selected[%d].Index = %d, want %d", i, p.Index, i)
		}
	}

	topK, err := buildFrostActiveMinersFromPeers(selected)
	if err != nil {
		t.Fatalf("buildFrostActiveMinersFromPeers failed: %v", err)
	}
	if len(topK.Indices) != 100 {
		t.Fatalf("topK size = %d, want 100", len(topK.Indices))
	}
	if topK.Indices[0] != 0 || topK.Indices[99] != 99 {
		t.Fatalf("topK indices = [%d..%d], want [0..99]", topK.Indices[0], topK.Indices[99])
	}
	if !bytes.Equal(topK.PublicKeys[0], selected[0].SecpPublicKey) {
		t.Fatal("FROST active miner did not use the configured public-only secp key")
	}
}

func TestSelectFrostCandidatePeers_RequiresDefaultK(t *testing.T) {
	peers := make([]PeerInfo, 0, 19)
	for i := 0; i < 19; i++ {
		peers = append(peers, PeerInfo{Index: i, Address: "addr", IsMiner: true})
	}
	cfg := testDualCustodyFrostConfig()
	cfg.Committee.TopN = 100
	cfg.Vault.DefaultK = 20

	if _, err := selectFrostCandidatePeers(peers, cfg); err == nil {
		t.Fatal("selectFrostCandidatePeers succeeded with 19 miners, want error")
	}
}

func TestValidateFrostStartupMinerSetDisabledSkipsCommitteeMinimum(t *testing.T) {
	cfg := testDualCustodyFrostConfig()
	cfg.Enabled = false
	cfg.Vault.DefaultK = 20

	peers := []PeerInfo{{Index: 0, Address: "local-debug-validator", IsMiner: true}}
	if err := validateFrostStartupMinerSet(peers, cfg); err != nil {
		t.Fatalf("disabled FROST rejected a one-validator startup manifest: %v", err)
	}
}

func TestApplyChainFrostBootstrapConfiguredEVMGenesis(t *testing.T) {
	sv, _ := newMapBackedStateView()
	activeMiners := configuredBootstrapTop("0x0001", "0x0002", "0x0003", "0x0004")
	activeMinersData, err := proto.Marshal(activeMiners)
	if err != nil {
		t.Fatalf("marshal active miners failed: %v", err)
	}
	sv.Set(keys.KeyFrostActiveMiners(), activeMinersData)

	group := curve.NewBN256Group()
	expectedPubkeys := [][]byte{group.SerializePoint(group.ScalarBaseMult(big.NewInt(1)))}
	chainCfg := config.ChainFrostConfig{
		SignAlgo:          "SCHNORR_ALT_BN128",
		FrostVariant:      "frost-bn128",
		AuthorityProtocol: 3, DepositProtocol: 2, DepositDexChainID: 771,
		VaultsPerChain:      1,
		VaultRefs:           []string{"0x1111111111111111111111111111111111111111"},
		InitialGroupPubkeys: []string{hex.EncodeToString(expectedPubkeys[0])},
	}
	vaultCfg := configuredBootstrapVaultPolicy("eth", pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128)
	vaultCfg.VaultRefs = append([]string(nil), chainCfg.VaultRefs...)

	if err := applyChainFrostBootstrap(sv, "eth", chainCfg, vaultCfg, 1, initialDkgBootstrapGraceBlocks, 5, 5, 5); err != nil {
		t.Fatalf("applyChainFrostBootstrap failed: %v", err)
	}

	cfgData, exists, err := sv.Get(keys.KeyFrostVaultConfig("eth", 0))
	if err != nil || !exists {
		t.Fatalf("vault config missing: exists=%t err=%v", exists, err)
	}
	var storedCfg pb.FrostVaultConfig
	if err := proto.Unmarshal(cfgData, &storedCfg); err != nil {
		t.Fatalf("unmarshal stored vault config failed: %v", err)
	}
	if len(storedCfg.VaultRefs) != 1 {
		t.Fatalf("stored VaultRefs count = %d, want 1", len(storedCfg.VaultRefs))
	}
	if storedCfg.VaultRefs[0] != chainCfg.VaultRefs[0] {
		t.Fatalf("stored VaultRefs[0] = %s", storedCfg.VaultRefs[0])
	}

	for vaultID := uint32(0); vaultID < 1; vaultID++ {
		stateKey := keys.KeyFrostVaultState("eth", vaultID)
		stateData, exists, err := sv.Get(stateKey)
		if err != nil || !exists {
			t.Fatalf("vault state missing for vault %d: exists=%t err=%v", vaultID, exists, err)
		}

		var state pb.FrostVaultState
		if err := proto.Unmarshal(stateData, &state); err != nil {
			t.Fatalf("unmarshal vault state failed for vault %d: %v", vaultID, err)
		}
		if state.Status != vm.VaultStatusActive {
			t.Fatalf("vault %d status = %s, want ACTIVE", vaultID, state.Status)
		}
		if state.KeyEpoch != 1 {
			t.Fatalf("vault %d key epoch = %d, want 1", vaultID, state.KeyEpoch)
		}
		if state.SignAlgo != pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128 {
			t.Fatalf("vault %d sign algo = %v", vaultID, state.SignAlgo)
		}
		if !bytes.Equal(state.GroupPubkey, expectedPubkeys[vaultID]) {
			t.Fatalf("vault %d group pubkey = %x, want %x", vaultID, state.GroupPubkey, expectedPubkeys[vaultID])
		}
		if len(state.CommitteeMembers) != 4 || state.CommitteeThreshold != 3 {
			t.Fatalf("vault %d committee policy = %d-of-%d, want 3-of-4", vaultID, state.CommitteeThreshold, len(state.CommitteeMembers))
		}

		transitionKey := keys.KeyFrostVaultTransition("eth", vaultID, 1)
		if transitionData, transitionExists, err := sv.Get(transitionKey); err != nil {
			t.Fatalf("read transition failed for vault %d: %v", vaultID, err)
		} else if transitionExists && len(transitionData) > 0 {
			t.Fatalf("unexpected genesis transition found for vault %d", vaultID)
		}
	}
}

func TestApplyChainFrostBootstrapInitialDkgUsesFutureTrigger(t *testing.T) {
	sv, _ := newMapBackedStateView()
	activeMiners := configuredBootstrapTop("miner-a", "miner-b", "miner-c", "miner-d")
	activeData, err := proto.Marshal(activeMiners)
	if err != nil {
		t.Fatalf("marshal active miners: %v", err)
	}
	sv.Set(keys.KeyFrostActiveMiners(), activeData)

	chainCfg := config.ChainFrostConfig{
		SignAlgo:       "SCHNORR_SECP256K1_BIP340",
		FrostVariant:   "frost-secp256k1",
		VaultsPerChain: 1,
	}
	vaultCfg := configuredBootstrapVaultPolicy("btc", pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340)
	trigger := initialDkgTriggerHeight(5)
	if err := applyChainFrostBootstrap(sv, "btc", chainCfg, vaultCfg, 1, trigger, 5, 5, 5); err != nil {
		t.Fatalf("applyChainFrostBootstrap failed: %v", err)
	}

	raw, exists, err := sv.Get(keys.KeyFrostVaultTransition("btc", 0, 1))
	if err != nil || !exists {
		t.Fatalf("initial transition missing: exists=%t err=%v", exists, err)
	}
	var transition pb.VaultTransitionState
	if err := proto.Unmarshal(raw, &transition); err != nil {
		t.Fatalf("unmarshal initial transition: %v", err)
	}
	if transition.TriggerHeight != trigger || transition.DkgCommitDeadline != trigger+4 ||
		transition.DkgSharingDeadline != trigger+9 || transition.DkgDisputeDeadline != trigger+14 {
		t.Fatalf("initial DKG did not retain a full post-grace window: trigger=%d commit=%d sharing=%d dispute=%d",
			transition.TriggerHeight, transition.DkgCommitDeadline, transition.DkgSharingDeadline, transition.DkgDisputeDeadline)
	}
}

func TestInitialDkgTriggerHeightPreservesFullCommitWindow(t *testing.T) {
	tests := []struct {
		name         string
		commitWindow uint64
		wantTrigger  uint64
	}{
		{name: "short local window", commitWindow: 5, wantTrigger: initialDkgBootstrapGraceBlocks},
		{name: "default production window", commitWindow: 20, wantTrigger: initialDkgBootstrapGraceBlocks},
		{name: "long configured window", commitWindow: 50, wantTrigger: 50},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := initialDkgTriggerHeight(test.commitWindow); got != test.wantTrigger {
				t.Fatalf("initialDkgTriggerHeight(%d) = %d, want %d", test.commitWindow, got, test.wantTrigger)
			}
		})
	}
}
