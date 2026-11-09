package main

import (
	"bytes"
	"dex/config"
	"dex/db"
	"dex/keys"
	"dex/logs"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFreshFrostUnequalGenesisStakeFailsBeforeStateWrites(t *testing.T) {
	peers := make([]PeerInfo, 5)
	for i := range peers {
		peers[i] = testPeerWithIdentity(t, i, true)
		peers[i].AdvertiseAddr = "validator.invalid:6000"
	}
	genesis := config.DefaultGenesisConfig()
	genesis.ChainID = 51025
	genesis.Alloc["default"] = config.GenesisAlloc{Balances: map[string]config.GenesisBalance{"FB": {MinerLockedBalance: "3200000000"}}}
	genesis.Alloc[peers[4].Address] = config.GenesisAlloc{Balances: map[string]config.GenesisBalance{"FB": {MinerLockedBalance: "3200000001"}}}
	frost := testDualCustodyFrostConfig()
	frost.Chains = map[string]config.ChainFrostConfig{"btc": configuredBootstrapChain("btc")}
	manager, err := db.NewManager(filepath.Join(t.TempDir(), "db"), logs.NewNodeLogger("fresh-genesis-ranking", 0))
	if err != nil {
		t.Fatal(err)
	}
	manager.InitWriteQueue(128, time.Hour)
	t.Cleanup(manager.Close)
	node := &NodeInstance{Address: peers[0].Address, DBManager: manager, NetworkChainID: 51025}
	before := bytes.Clone(manager.GetStateFingerprint())
	err = seedTrustedGenesisState(node, peers, genesis, frost, true)
	if err == nil || !strings.Contains(err.Error(), "requires equal FB miner_locked_balance") {
		t.Fatalf("unequal genesis stake error=%v", err)
	}
	if err := manager.ForceFlush(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, manager.GetStateFingerprint()) {
		t.Fatal("rejected genesis changed StateDB fingerprint")
	}
	for _, key := range []string{keys.KeyAccount(peers[0].Address), keys.KeyMinerBLSPublicKeyOwner(peers[0].BLSPublicKey), keys.KeyFrostActiveMiners(), keys.KeyFrostConfiguredGenesisMarker()} {
		if raw, err := manager.Get(key); err == nil && len(raw) != 0 {
			t.Fatalf("rejected genesis wrote %s", key)
		}
	}
}
