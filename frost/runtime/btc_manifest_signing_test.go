package runtime

import (
	"bytes"
	"testing"

	"dex/frost/chain"
	"dex/frost/guardian"
	"dex/frost/runtime/roast"
	"dex/keys"
	"dex/pb"
	"google.golang.org/protobuf/proto"
)

func newBTCManifestValidatorFixture(t *testing.T) (*migrationValidatorFixture, *pb.FrostVaultMigrationState) {
	t.Helper()
	f := newMigrationValidatorFixture(t, "btc", true)
	f.cfg.DepositDexChainId = 51025
	f.intent.BtcProtocolVersion = 3
	child := proto.Clone(f.intent).(*pb.FrostVaultMigrationState)
	child.ParentMigrationId = f.intent.MigrationId
	child.BtcChildSeq = 1
	var u pb.FrostUtxo
	for k, v := range f.reader.data {
		if k == keys.KeyFrostBtcUtxo(0, "abababababababababababababababababababababababababababababababab", 0) {
			if proto.Unmarshal(v, &u) != nil {
				t.Fatal("input")
			}
			break
		}
	}
	u.OriginId = chain.BTCOriginID(51025, "mainnet", u.Txid, 0, false)
	child.BtcOriginId = append([]byte(nil), u.OriginId...)
	child.IntentHash = chain.BTCMigrationChildIntentHash(51025, child.ParentMigrationId, 1, child.BtcOriginId, child.TemplateHash)
	child.MigrationId = chain.BTCMigrationChildID(child.ParentMigrationId, 1, child.IntentHash)
	f.put(t, keys.KeyFrostBtcUtxo(0, u.Txid, 0), &u)
	f.reader.data[keys.KeyFrostBtcLockedUtxo(0, u.Txid, 0)] = []byte(child.MigrationId)
	f.intent.TemplateData = nil
	f.intent.BtcChildCount = 1
	f.intent.BtcManifestHash = chain.BTCMigrationManifestAccumulate(nil, child.IntentHash)
	f.intent.TemplateHash = append([]byte(nil), f.intent.BtcManifestHash...)
	f.intent.BtcTotalInputAmount = "100000"
	f.intent.BtcTotalOutputAmount = "90000"
	f.intent.BtcTotalFeeAmount = "10000"
	f.intent.IntentHash = chain.BTCMigrationManifestIntentHash(51025, "mainnet", f.intent)
	f.tr.MigrationTemplateHash = f.intent.TemplateHash
	f.tr.MigrationIntentHash = f.intent.IntentHash
	f.save(t)
	f.put(t, keys.KeyFrostBtcMigrationChildState(child.MigrationId), child)
	f.reader.data[keys.KeyFrostBtcMigrationChild(f.intent.MigrationId, 1)] = []byte(child.MigrationId)
	f.reader.data[keys.KeyFrostBtcMigrationActiveChild(f.intent.MigrationId, 1)] = []byte(child.MigrationId)
	f.env.SessionID = child.MigrationId + "_r3"
	coordinator, e := chain.WithdrawCoordinator(f.env.SessionID, 1, f.tr.OldCommitteeMembers)
	if e != nil {
		t.Fatal(e)
	}
	f.env.From = roast.NodeID(coordinator)
	f.messages[len(f.messages)-1] = append([]byte(nil), f.intent.IntentHash...)
	f.reader.getCount = 0
	f.reader.batchCount = 0
	f.reader.scanCount = 0
	return f, child
}

func TestBTCManifestChildSignsOnlyItsInputAndSealedParent(t *testing.T) {
	f, child := newBTCManifestValidatorFixture(t)
	if e := newSigningRequestValidator(f.reader)(f.env, f.messages); e != nil {
		t.Fatal(e)
	}
	if f.reader.scanCount != 0 || f.reader.batchCount != 2 {
		t.Fatalf("child rescanned vault: scans=%d batches=%d", f.reader.scanCount, f.reader.batchCount)
	}
	intent, e := ResolveGuardianIntent(f.reader, guardian.Request{Chain: "btc", VaultID: 0, Epoch: 1, JobID: f.env.SessionID, TemplateHash: child.TemplateHash})
	if e != nil || !sameSigningMatrix(intent.Messages, f.messages) {
		t.Fatalf("guardian child authority: %v", e)
	}
}

func TestBTCManifestChildRejectsAuthorityAndMembershipMutation(t *testing.T) {
	for _, name := range []string{"missing-lock", "other-lock", "missing-member", "root", "parent-authority", "child-authority", "coordinator"} {
		t.Run(name, func(t *testing.T) {
			f, child := newBTCManifestValidatorFixture(t)
			switch name {
			case "missing-lock":
				delete(f.reader.data, keys.KeyFrostBtcLockedUtxo(0, "abababababababababababababababababababababababababababababababab", 0))
			case "other-lock":
				f.reader.data[keys.KeyFrostBtcLockedUtxo(0, "abababababababababababababababababababababababababababababababab", 0)] = []byte("other")
			case "missing-member":
				delete(f.reader.data, keys.KeyFrostBtcMigrationChild(f.intent.MigrationId, 1))
			case "root":
				child.BtcOriginId = bytes.Repeat([]byte{8}, 32)
				f.put(t, keys.KeyFrostBtcMigrationChildState(child.MigrationId), child)
			case "parent-authority":
				f.messages[len(f.messages)-1] = child.IntentHash
			case "child-authority":
				child.IntentHash = append([]byte(nil), f.intent.IntentHash...)
				f.put(t, keys.KeyFrostBtcMigrationChildState(child.MigrationId), child)
			case "coordinator":
				f.env.From = "outsider"
			}
			if e := newSigningRequestValidator(f.reader)(f.env, f.messages); e == nil {
				t.Fatal("tampered child accepted")
			}
		})
	}
}
