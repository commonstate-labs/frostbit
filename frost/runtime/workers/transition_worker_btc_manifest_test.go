package workers

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/keys"
	"dex/pb"
	"dex/utils"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
)

type manifestSigningCapture struct {
	testSigningService
	params *SigningSessionParams
}

func (s *manifestSigningCapture) StartSigningSession(_ context.Context, p *SigningSessionParams) (string, error) {
	s.params = p
	return p.JobID, nil
}

func TestBTCMigrationWorkerSubmitsChildArtifactWithParentAuthorization(t *testing.T) {
	op, _ := btcec.PrivKeyFromBytes([]byte{23})
	gp, _ := btcec.PrivKeyFromBytes([]byte{31})
	np, _ := btcec.PrivKeyFromBytes([]byte{19})
	operator, guardian, next := schnorr.SerializePubKey(op.PubKey()), schnorr.SerializePubKey(gp.PubKey()), schnorr.SerializePubKey(np.PubKey())
	script, _, _, _ := utils.BuildDualCustodyTaproot(operator, guardian)
	in := chain.UTXO{TxID: strings.Repeat("ab", 32), Amount: 100000, ScriptPubKey: script}
	canonical, _, e := btc.BuildMigrationTemplate("btc", 0, 1, 2, []chain.UTXO{in}, next, "regtest", 1, guardian, [][]byte{operator})
	if e != nil {
		t.Fatal(e)
	}
	parent := &pb.FrostVaultMigrationState{MigrationId: chain.MigrationID("btc", 0, 2), Chain: "btc", OldEpoch: 1, NewEpoch: 2, OldGroupPubkey: operator, NewGroupPubkey: next, IntentHash: bytes.Repeat([]byte{9}, 32)}
	origin := chain.BTCOriginID(51025, "regtest", in.TxID, 0, false)
	childHash := chain.BTCMigrationChildIntentHash(51025, parent.MigrationId, 1, origin, canonical.TemplateHash)
	child := &pb.FrostVaultMigrationState{MigrationId: chain.BTCMigrationChildID(parent.MigrationId, 1, childHash), Chain: "btc", OldEpoch: 1, NewEpoch: 2, OldGroupPubkey: operator, NewGroupPubkey: next, ParentMigrationId: parent.MigrationId, BtcProtocolVersion: 3, BtcChildSeq: 1, BtcOriginId: origin, TemplateData: canonical.TemplateData, TemplateHash: canonical.TemplateHash, IntentHash: childHash}
	r := newTestStateReader()
	r.data[keys.KeyFrostVaultConfig("btc", 0)] = mustMarshalMsg(t, &pb.FrostVaultConfig{Chain: "btc", BtcNetwork: "regtest", BtcFeeSatsPerVbyte: 1, GuardianPubkey: guardian})
	r.data[keys.KeyFrostBtcUtxo(0, in.TxID, 0)] = mustMarshalMsg(t, &pb.FrostUtxo{Txid: in.TxID, Amount: "100000", ScriptPubkey: script, OriginId: origin})
	r.data[keys.KeyFrostBtcLockedUtxo(0, in.TxID, 0)] = []byte(child.MigrationId)
	sig := bytes.Repeat([]byte{1}, 64)
	signing := &manifestSigningCapture{testSigningService: testSigningService{result: &SignedPackage{Signatures: [][]byte{sig, sig}, GuardianSignatures: [][]byte{sig, sig}}}}
	submitter := &transitionCaptureSubmitter{}
	w := &TransitionWorker{stateReader: r, txSubmitter: submitter, signingService: signing, localAddress: "coordinator", localIdentity: testLocalIdentity(t, strings.Repeat("11", 32))}
	if e := w.signBTCManifestChild(context.Background(), child.MigrationId+"_r0", &pb.VaultTransitionState{}, parent, child, 1); e != nil {
		t.Fatal(e)
	}
	tx := submitter.transitionTx
	if tx == nil || tx.MigrationId != child.MigrationId || !bytes.Equal(tx.MsgHash, parent.IntentHash) || bytes.Equal(tx.MsgHash, child.IntentHash) || len(tx.InputSigs) != 1 || len(tx.GuardianSigs) != 2 || len(tx.RawTx) == 0 || len(signing.params.Messages) != 2 || !bytes.Equal(signing.params.Messages[1], parent.IntentHash) {
		t.Fatalf("child authorization conflated with manifest membership: tx=%+v", tx)
	}
}
