package vm

import (
	"strconv"
	"testing"

	"dex/keys"
	"dex/pb"
	"dex/protocol"

	"google.golang.org/protobuf/proto"
)

func protocolMigrationView(t *testing.T, fee uint64) StateView {
	t.Helper()
	cfg, err := proto.Marshal(&pb.FrostVaultConfig{
		Chain: "btc", VaultCount: 1, BtcNetwork: "mainnet", BtcFeeSatsPerVbyte: fee,
	})
	if err != nil {
		t.Fatal(err)
	}
	return newMemoryStateView(map[string][]byte{keys.KeyFrostVaultConfig("btc", 0): cfg})
}

func TestFresh51025HasNoScheduledMigration(t *testing.T) {
	sv := protocolMigrationView(t, protocol.BTCFeeSatsPerVByteV2)
	executor := &Executor{networkChainID: 51025}
	for _, height := range []uint64{1, 42599, 42600, 1000000} {
		if err := executor.applyProtocolMigrations(sv, height); err != nil {
			t.Fatalf("height %d: %v", height, err)
		}
		if _, exists, _ := sv.Get(keys.KeyProtocolVersion()); exists {
			t.Fatalf("height %d wrote protocol version", height)
		}
		if _, exists, _ := sv.Get(keys.KeyProtocolUpgradeReceipt(2)); exists {
			t.Fatalf("height %d wrote v2 receipt", height)
		}
	}
	if diff := sv.Diff(); len(diff) != 0 {
		t.Fatalf("fresh chain migrations wrote %d keys", len(diff))
	}
}

func TestGenericBTCFeeMigrationStillWorks(t *testing.T) {
	sv := protocolMigrationView(t, 0)
	upgrade := protocol.Upgrade{Version: 2, ActivationHeight: 42600, ID: protocol.UpgradeBTCFeeConfigV2}
	if err := applyProtocolUpgrade(sv, upgrade); err != nil {
		t.Fatal(err)
	}
	if err := applyProtocolUpgrade(sv, upgrade); err != nil {
		t.Fatalf("idempotent migration: %v", err)
	}
	raw, exists, err := sv.Get(keys.KeyFrostVaultConfig("btc", 0))
	if err != nil || !exists {
		t.Fatalf("load migrated config: exists=%v err=%v", exists, err)
	}
	var cfg pb.FrostVaultConfig
	if err := proto.Unmarshal(raw, &cfg); err != nil || cfg.BtcFeeSatsPerVbyte != protocol.BTCFeeSatsPerVByteV2 {
		t.Fatalf("migrated config=%+v err=%v", &cfg, err)
	}
}

func TestGenericBTCFeeMigrationRejectsUnexpectedFee(t *testing.T) {
	sv := protocolMigrationView(t, 99)
	if err := migrateBTCFeeConfigV2(sv); err == nil {
		t.Fatal("unexpected pre-upgrade BTC fee was accepted")
	}
	if diff := sv.Diff(); len(diff) != 0 {
		t.Fatalf("failed migration wrote %d keys", len(diff))
	}
}

func TestProtocolMigrationRejectsNewerState(t *testing.T) {
	sv := protocolMigrationView(t, protocol.BTCFeeSatsPerVByteV2)
	sv.Set(keys.KeyProtocolVersion(), []byte(strconv.FormatUint(protocol.CurrentVersion+1, 10)))
	if err := (&Executor{networkChainID: 51025}).applyProtocolMigrations(sv, 42601); err == nil {
		t.Fatal("newer state protocol version was accepted")
	}
}
