package chain

import (
	"bytes"
	"errors"
	"testing"

	"dex/keys"
	"dex/pb"
	"dex/protocol"
)

func TestBTCIsolationRequiresFinalizedVersionAndReceipt(t *testing.T) {
	valid := protocol.UpgradeReceiptValue(protocol.Upgrade{Version: 3, ActivationHeight: 123, ID: protocol.UpgradeBTCIsolationV3})
	for _, tc := range []struct {
		name, version, receipt string
		active, wantErr        bool
	}{
		{"absent", "", "", false, false}, {"legacy", "1", valid, false, false},
		{"v2", "2", valid, false, false}, {"missing receipt", "3", "", false, true},
		{"valid", "3", valid, true, false}, {"noncanonical version", "03", valid, false, true},
		{"future", "4", valid, false, true}, {"wrong upgrade", "3", "version=3;activation_height=123;id=other", false, true},
		{"zero height", "3", "version=3;activation_height=0;id=" + protocol.UpgradeBTCIsolationV3, false, true},
		{"noncanonical height", "3", "version=3;activation_height=0123;id=" + protocol.UpgradeBTCIsolationV3, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			read := func(key string) ([]byte, bool, error) {
				value := tc.receipt
				if key == keys.KeyProtocolVersion() {
					value = tc.version
				}
				return []byte(value), value != "", nil
			}
			active, err := BTCIsolationActive(read)
			if active != tc.active || (err != nil) != tc.wantErr {
				t.Fatalf("active=%v err=%v", active, err)
			}
		})
	}
	_, err := BTCIsolationActive(func(string) ([]byte, bool, error) { return nil, false, errors.New("unavailable") })
	if err == nil {
		t.Fatal("storage error must fail closed")
	}
}

func TestBTCIsolationIntentsPreventCrossContextReplay(t *testing.T) {
	base := BTCWithdrawPartIntent{Network: 51025, BTCNetwork: "testnet3", ParentID: "parent-A", PartSeq: 1, VaultID: 2, KeyEpoch: 7, Gross: 100000, Fee: 1200, OriginID: bytes.Repeat([]byte{1}, 32), TemplateHash: bytes.Repeat([]byte{2}, 32)}
	want := BTCWithdrawPartJobID(base)
	mutations := []func(*BTCWithdrawPartIntent){
		func(v *BTCWithdrawPartIntent) { v.Network++ }, func(v *BTCWithdrawPartIntent) { v.BTCNetwork = "mainnet" },
		func(v *BTCWithdrawPartIntent) { v.ParentID = "parent-B" }, func(v *BTCWithdrawPartIntent) { v.PartSeq++ },
		func(v *BTCWithdrawPartIntent) { v.VaultID++ }, func(v *BTCWithdrawPartIntent) { v.KeyEpoch++ },
		func(v *BTCWithdrawPartIntent) { v.Gross++ }, func(v *BTCWithdrawPartIntent) { v.Fee++ },
		func(v *BTCWithdrawPartIntent) { v.OriginID = bytes.Repeat([]byte{3}, 32) }, func(v *BTCWithdrawPartIntent) { v.TemplateHash = bytes.Repeat([]byte{4}, 32) },
	}
	for i, mutate := range mutations {
		other := base
		mutate(&other)
		if BTCWithdrawPartJobID(other) == want {
			t.Fatalf("intent mutation %d permitted replay", i)
		}
	}
	origin := BTCOriginID(51025, "testnet3", "tx", 1, false)
	if bytes.Equal(origin, BTCOriginID(51025, "testnet3", "tx", 1, true)) {
		t.Fatal("legacy boundary misrepresented as original deposit")
	}
	if bytes.Equal(origin, BTCOriginID(51025, "mainnet", "tx", 1, false)) {
		t.Fatal("cross network origin reuse")
	}
	parent := &pb.FrostVaultMigrationState{MigrationId: "migration", VaultId: 2, OldEpoch: 7, NewEpoch: 8, BtcProtocolVersion: 3, BtcChildCount: 2, BtcManifestHash: BTCMigrationManifestAccumulate(nil, []byte("A")), BtcTotalInputAmount: "200000", BtcTotalOutputAmount: "198000", BtcTotalFeeAmount: "2000"}
	hash := BTCMigrationManifestIntentHash(51025, "testnet3", parent)
	parent.BtcManifestHash = BTCMigrationManifestAccumulate(nil, []byte("B"))
	if bytes.Equal(hash, BTCMigrationManifestIntentHash(51025, "testnet3", parent)) {
		t.Fatal("changed manifest retained authorization")
	}
	if bytes.Equal(BTCMigrationManifestAccumulate([]byte("A"), []byte("B")), BTCMigrationManifestAccumulate([]byte("B"), []byte("A"))) {
		t.Fatal("manifest order not committed")
	}
}
