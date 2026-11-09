package custodyevidence

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"dex/frost/artifact"
	"dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/keys"
	"dex/pb"
	"dex/utils"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"google.golang.org/protobuf/proto"
)

func TestBTCIsolatedMigrationEvidenceUsesPermanentChildAndSealedManifest(t *testing.T) {
	rows := map[string][]byte{}
	put := func(k string, m proto.Message) {
		raw, e := proto.Marshal(m)
		if e != nil {
			t.Fatal(e)
		}
		rows[k] = raw
	}
	get := func(k string) ([]byte, bool, error) { v, ok := rows[k]; return v, ok, nil }
	op, _ := btcec.PrivKeyFromBytes([]byte{23})
	gp, _ := btcec.PrivKeyFromBytes([]byte{31})
	np, _ := btcec.PrivKeyFromBytes([]byte{19})
	operator, guardian, next := schnorr.SerializePubKey(op.PubKey()), schnorr.SerializePubKey(gp.PubKey()), schnorr.SerializePubKey(np.PubKey())
	script, _, _, _ := utils.BuildDualCustodyTaproot(operator, guardian)
	input := chain.UTXO{TxID: strings.Repeat("ab", 32), Amount: 100000, ScriptPubKey: script}
	canonical, template, e := btc.BuildMigrationTemplate("btc", 0, 1, 2, []chain.UTXO{input}, next, "regtest", 1, guardian, [][]byte{operator})
	if e != nil {
		t.Fatal(e)
	}
	inputSigs := [][]byte{bytes.Repeat([]byte{1}, 64)}
	guardianSigs := [][]byte{bytes.Repeat([]byte{2}, 64), bytes.Repeat([]byte{3}, 64)}
	raw, e := template.BuildSignedRawTx(inputSigs, guardianSigs[:1])
	if e != nil {
		t.Fatal(e)
	}
	txid, e := template.TxID()
	if e != nil {
		t.Fatal(e)
	}
	parent := &pb.FrostVaultMigrationState{MigrationId: chain.MigrationID("btc", 0, 2), Chain: "btc", VaultId: 0, OldEpoch: 1, NewEpoch: 2, OldGroupPubkey: operator, NewGroupPubkey: next, Status: "INTENT_FINALIZED", BtcProtocolVersion: 3, BtcChildCount: 1, BtcTotalInputAmount: "100000", BtcTotalOutputAmount: fmt.Sprint(template.Outputs[0].Amount), BtcTotalFeeAmount: fmt.Sprint(100000 - template.Outputs[0].Amount)}
	origin := chain.BTCOriginID(51025, "regtest", input.TxID, 0, false)
	childHash := chain.BTCMigrationChildIntentHash(51025, parent.MigrationId, 1, origin, canonical.TemplateHash)
	childID := chain.BTCMigrationChildID(parent.MigrationId, 1, childHash)
	parent.BtcManifestHash = chain.BTCMigrationManifestAccumulate(nil, childHash)
	parent.TemplateHash = parent.BtcManifestHash
	parent.IntentHash = chain.BTCMigrationManifestIntentHash(51025, "regtest", parent)
	child := proto.Clone(parent).(*pb.FrostVaultMigrationState)
	child.ParentMigrationId = parent.MigrationId
	child.MigrationId = childID
	child.BtcChildSeq = 1
	child.BtcOriginId = origin
	child.TemplateHash = canonical.TemplateHash
	child.TemplateData = canonical.TemplateData
	child.IntentHash = childHash
	child.Status = "SIGNED"
	put(keys.KeyFrostPermanentMigration(parent.MigrationId), &pb.FrostVaultMigrationArchive{State: parent})
	archive := &pb.FrostVaultMigrationArchive{State: child, InputSigs: inputSigs, GuardianSigs: guardianSigs}
	put(keys.KeyFrostPermanentMigration(childID), archive)
	put(keys.KeyFrostVaultConfig("btc", 0), &pb.FrostVaultConfig{Chain: "btc", DepositDexChainId: 51025, BtcNetwork: "regtest"})
	rows[keys.KeyFrostBtcMigrationChild(parent.MigrationId, 1)] = []byte(childID)
	record, e := artifact.NewRecord(artifact.Metadata{ArtifactID: txid, Kind: artifact.KindMigration, Chain: "BTC", VaultID: 0, Epoch: 2, SourceID: childID}, raw)
	if e != nil {
		t.Fatal(e)
	}
	rows[keys.KeyFrostPermanentArtifact(txid)], _ = artifact.MarshalRecord(record)
	rows[keys.KeyFrostArtifactMeta(txid)], _ = artifact.MarshalMetadata(record.Metadata)
	result, e := InternalBTCOutputs(get, txid)
	if e != nil || !result.KnownArtifact || len(result.InternalVouts) != 1 || result.InternalVouts[0] != 0 {
		t.Fatalf("evidence=%+v err=%v", result, e)
	}
	batches := 0
	_, e = InternalBTCOutputsMany(get, func(ks []string) (map[string][]byte, error) {
		batches++
		values := map[string][]byte{}
		for _, k := range ks {
			if v, ok := rows[k]; ok {
				values[k] = v
			}
		}
		return values, nil
	}, []string{txid, txid})
	if e != nil || batches != 3 {
		t.Fatalf("deduplicated batches=%d err=%v", batches, e)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*pb.FrostVaultMigrationArchive)
	}{
		{"missing input signature", func(a *pb.FrostVaultMigrationArchive) { a.InputSigs = nil }},
		{"missing Guardian signature", func(a *pb.FrostVaultMigrationArchive) { a.GuardianSigs = nil }},
		{"missing intent signature", func(a *pb.FrostVaultMigrationArchive) { a.GuardianSigs = a.GuardianSigs[:1] }},
		{"extra Guardian signature", func(a *pb.FrostVaultMigrationArchive) { a.GuardianSigs = append(a.GuardianSigs, make([]byte, 64)) }},
		{"short input signature", func(a *pb.FrostVaultMigrationArchive) { a.InputSigs[0] = []byte{1} }},
		{"changed witness", func(a *pb.FrostVaultMigrationArchive) { a.InputSigs[0][0] ^= 1 }},
		{"changed Guardian witness", func(a *pb.FrostVaultMigrationArchive) { a.GuardianSigs[0][0] ^= 1 }},
		{"historical raw mismatch", func(a *pb.FrostVaultMigrationArchive) { a.RawTx = []byte{1} }},
		{"child identity mismatch", func(a *pb.FrostVaultMigrationArchive) { a.State.MigrationId = "wrong-child" }},
		{"manifest identity mismatch", func(a *pb.FrostVaultMigrationArchive) { a.State.ParentMigrationId = "wrong-parent" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			altered := proto.Clone(archive).(*pb.FrostVaultMigrationArchive)
			tc.mutate(altered)
			put(keys.KeyFrostPermanentMigration(childID), altered)
			if _, err := InternalBTCOutputs(get, txid); err == nil {
				t.Fatal("corrupt migration evidence accepted")
			}
		})
	}
	archive.RawTx = append([]byte(nil), raw...)
	put(keys.KeyFrostPermanentMigration(childID), archive)
	if _, err := InternalBTCOutputs(get, txid); err != nil {
		t.Fatalf("matching historical raw rejected: %v", err)
	}
	put(keys.KeyFrostVaultMigration("btc", 0, 2), parent)
	rows[keys.KeyFrostPermanentMigration(parent.MigrationId)] = []byte("corrupt")
	if _, e := InternalBTCOutputs(get, txid); e == nil {
		t.Fatal("corrupt permanent manifest hidden by live state")
	}
}
