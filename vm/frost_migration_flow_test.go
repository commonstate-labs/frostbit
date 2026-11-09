package vm

import (
	"bytes"
	"math/big"
	"strconv"
	"strings"
	"testing"

	chainpkg "dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/keys"
	"dex/pb"
	"dex/utils"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"google.golang.org/protobuf/proto"
)

func migrationTestKey(t *testing.T, scalar int64) (*btcec.PrivateKey, []byte) {
	t.Helper()
	priv, _ := btcec.PrivKeyFromBytes(big.NewInt(scalar).FillBytes(make([]byte, 32)))
	return priv, schnorr.SerializePubKey(priv.PubKey())
}

func putMigrationProto(t *testing.T, sv StateView, key string, value proto.Message) {
	t.Helper()
	data, err := proto.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(key, data)
}

func seedBTCMigrationIntent(t *testing.T, sv StateView, oldPub, newPub []byte, intent *pb.FrostVaultMigrationState) *pb.VaultTransitionState {
	t.Helper()
	transition := &pb.VaultTransitionState{
		Chain: "btc", VaultId: intent.VaultId, EpochId: intent.NewEpoch,
		SignAlgo:  pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		DkgStatus: DKGStatusKeyReady, Lifecycle: VaultLifecycleDraining,
		OldCommitteeMembers: []string{"old-member"}, NewCommitteeMembers: []string{"new-member"},
		OldGroupPubkey: oldPub, NewGroupPubkey: newPub,
		MigrationJobId: intent.MigrationId, MigrationStatus: migrationStatusIntentFinalized,
		MigrationTemplateHash: intent.TemplateHash, MigrationIntentHash: intent.IntentHash,
	}
	putMigrationProto(t, sv, keys.KeyFrostVaultTransition("btc", intent.VaultId, intent.NewEpoch), transition)
	putMigrationProto(t, sv, keys.KeyFrostVaultState("btc", intent.VaultId), &pb.FrostVaultState{
		Chain: "btc", VaultId: intent.VaultId, KeyEpoch: intent.OldEpoch,
		GroupPubkey: oldPub, SignAlgo: pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		Status: VaultStatusActive, CommitteeMembers: []string{"old-member"},
	})
	putMigrationProto(t, sv, keys.KeyFrostVaultMigration("btc", intent.VaultId, intent.NewEpoch), proto.Clone(intent).(*pb.FrostVaultMigrationState))
	putMigrationProto(t, sv, keys.KeyFrostVaultConfig("btc", 0), &pb.FrostVaultConfig{Chain: "btc", VaultCount: 1, BtcNetwork: "mainnet", BtcFeeSatsPerVbyte: btcTestFeeRateSatsPerVByte, GuardianPubkey: btcDualTestGuardianPubkey()})
	return transition
}

func TestProcessFrostMigrationIntentsCreatesConsensusNoFundsIntent(t *testing.T) {
	sv := NewMockStateView()
	_, oldPub := migrationTestKey(t, 2)
	_, newPub := migrationTestKey(t, 3)
	transition := &pb.VaultTransitionState{
		Chain: "btc", VaultId: 0, EpochId: 2, SignAlgo: pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		DkgStatus: DKGStatusKeyReady, Lifecycle: VaultLifecycleDraining,
		OldGroupPubkey: oldPub, NewGroupPubkey: newPub,
		OldCommitteeMembers: []string{"old-member"}, NewCommitteeMembers: []string{"new-member"},
	}
	transitionKey := keys.KeyFrostVaultTransition("btc", 0, 2)
	putMigrationProto(t, sv, transitionKey, transition)
	sv.Set(keys.KeyFrostVaultTransitionActive("btc", 0, 2), []byte(transitionKey))
	putMigrationProto(t, sv, keys.KeyFrostVaultState("btc", 0), &pb.FrostVaultState{
		Chain: "btc", VaultId: 0, KeyEpoch: 1, GroupPubkey: oldPub,
		SignAlgo: pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340, Status: VaultStatusActive,
	})
	putMigrationProto(t, sv, keys.KeyFrostVaultConfig("btc", 0), &pb.FrostVaultConfig{Chain: "btc", VaultCount: 1, BtcNetwork: "mainnet", BtcFeeSatsPerVbyte: btcTestFeeRateSatsPerVByte, GuardianPubkey: btcDualTestGuardianPubkey()})

	if err := (&Executor{}).processFrostMigrationIntents(&pb.Block{Header: &pb.BlockHeader{Height: 50}}, sv); err != nil {
		t.Fatal(err)
	}
	data, exists, err := sv.Get(keys.KeyFrostVaultMigration("btc", 0, 2))
	if err != nil || !exists {
		t.Fatalf("migration intent missing: exists=%v err=%v", exists, err)
	}
	var hot pb.FrostVaultMigrationState
	if err := proto.Unmarshal(data, &hot); err != nil {
		t.Fatal(err)
	}
	if !hot.NoFunds || hot.Status != migrationStatusIntentFinalized || len(hot.TemplateData) != 0 || len(hot.IntentHash) != 32 {
		t.Fatalf("unexpected hot intent: %+v", &hot)
	}
	if _, exists, _ := sv.Get(keys.KeyArchiveFrostMigration(hot.MigrationId)); exists {
		t.Fatal("unsigned intent must not depend on or create an archive copy")
	}
	if ref, exists, _ := sv.Get(keys.KeyFrostMigrationStateRef(hot.MigrationId)); !exists || string(ref) != keys.KeyFrostVaultMigration("btc", 0, 2) {
		t.Fatalf("migration state lookup missing: exists=%v ref=%q", exists, string(ref))
	}
}

func TestProcessFrostMigrationIntentsInvalidTRXConfigIsVisibleButDoesNotHaltBlocks(t *testing.T) {
	sv := NewMockStateView()
	oldPub := bytes.Repeat([]byte{1}, 64)
	newPub := bytes.Repeat([]byte{2}, 64)
	transition := &pb.VaultTransitionState{
		Chain: "trx", VaultId: 0, EpochId: 2, SignAlgo: pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1,
		DkgStatus: DKGStatusKeyReady, Lifecycle: VaultLifecycleDraining,
		OldGroupPubkey: oldPub, NewGroupPubkey: newPub,
	}
	transitionKey := keys.KeyFrostVaultTransition("trx", 0, 2)
	putMigrationProto(t, sv, transitionKey, transition)
	sv.Set(keys.KeyFrostVaultTransitionActive("trx", 0, 2), []byte(transitionKey))
	putMigrationProto(t, sv, keys.KeyFrostVaultState("trx", 0), &pb.FrostVaultState{
		Chain: "trx", VaultId: 0, KeyEpoch: 1, GroupPubkey: oldPub, Status: VaultStatusActive,
	})
	putMigrationProto(t, sv, keys.KeyFrostVaultConfig("trx", 0), &pb.FrostVaultConfig{Chain: "trx", VaultCount: 1})

	if err := (&Executor{}).processFrostMigrationIntents(&pb.Block{Header: &pb.BlockHeader{Height: 50}}, sv); err != nil {
		t.Fatalf("unsupported migration adapter halted block finalization: %v", err)
	}
	data, _, _ := sv.Get(transitionKey)
	var blocked pb.VaultTransitionState
	if err := proto.Unmarshal(data, &blocked); err != nil {
		t.Fatal(err)
	}
	if blocked.MigrationStatus != migrationStatusIntentBlocked || !strings.Contains(blocked.MigrationError, "invalid account migration signing algorithm") {
		t.Fatalf("invalid TRX configuration was not exposed as a retryable blocked intent: %+v", &blocked)
	}
	if _, exists, _ := sv.Get(keys.KeyFrostVaultMigration("trx", 0, 2)); exists {
		t.Fatal("blocked migration unexpectedly created a signable intent")
	}
}

func TestTransitionSignedNoFundsBTCActivatesAndIsIdempotent(t *testing.T) {
	sv := NewMockStateView()
	oldPriv, oldPub := migrationTestKey(t, 2)
	_, newPub := migrationTestKey(t, 3)
	templateHash := chainpkg.EmptyMigrationTemplateHash()
	intent := &pb.FrostVaultMigrationState{
		MigrationId: chainpkg.MigrationID("btc", 0, 2), Chain: "btc", VaultId: 0,
		OldEpoch: 1, NewEpoch: 2, OldGroupPubkey: oldPub, NewGroupPubkey: newPub,
		TemplateHash: templateHash, NoFunds: true, Status: migrationStatusIntentFinalized, IntentHeight: 40,
	}
	intent.IntentHash = chainpkg.MigrationIntentHash("btc", 0, 1, 2, oldPub, newPub, templateHash, true)
	seedBTCMigrationIntent(t, sv, oldPub, newPub, intent)
	authorization, err := schnorr.Sign(oldPriv, intent.IntentHash)
	if err != nil {
		t.Fatal(err)
	}
	req := &pb.FrostVaultTransitionSignedTx{
		Base:  &pb.BaseMessage{TxId: "migration-signed", FromAddress: "old-member", ExecutedHeight: 60},
		Chain: "btc", OldVaultId: 0, NewVaultId: 0, EpochId: 2,
		MigrationId: intent.MigrationId, MsgHash: intent.IntentHash, Signature: authorization.Serialize(), TemplateHash: templateHash,
	}
	_, guardianPriv := btcDualTestKeys()
	guardianSig, _ := schnorr.Sign(guardianPriv, intent.IntentHash)
	req.GuardianSigs = [][]byte{guardianSig.Serialize()}
	tx := &pb.AnyTx{Content: &pb.AnyTx_FrostVaultTransitionSignedTx{FrostVaultTransitionSignedTx: req}}
	if ops, receipt, err := (&FrostVaultTransitionSignedTxHandler{}).DryRun(tx, sv); err != nil || receipt.Status != "SUCCEED" || len(ops) != 0 {
		t.Fatalf("no-funds migration failed: ops=%d receipt=%+v err=%v", len(ops), receipt, err)
	}
	assertPermanentMigrationDelivery(t, sv, intent.MigrationId)
	vaultData, _, _ := sv.Get(keys.KeyFrostVaultState("btc", 0))
	var vault pb.FrostVaultState
	_ = proto.Unmarshal(vaultData, &vault)
	if vault.KeyEpoch != 2 || !bytes.Equal(vault.GroupPubkey, newPub) || vault.Status != VaultStatusActive {
		t.Fatalf("vault not activated with new key: %+v", &vault)
	}

	duplicate := proto.Clone(req).(*pb.FrostVaultTransitionSignedTx)
	duplicate.Base.TxId = "migration-signed-duplicate"
	dupTx := &pb.AnyTx{Content: &pb.AnyTx_FrostVaultTransitionSignedTx{FrostVaultTransitionSignedTx: duplicate}}
	if _, receipt, err := (&FrostVaultTransitionSignedTxHandler{}).DryRun(dupTx, sv); err != nil || receipt.Status != "SUCCEED" || receipt.WriteCount != 0 {
		t.Fatalf("identical duplicate was not idempotent: receipt=%+v err=%v", receipt, err)
	}

	                                                                          
	                                                                    
	var aux [32]byte
	aux[0] = 7
	alternateSignature, err := schnorr.Sign(oldPriv, intent.IntentHash, schnorr.CustomNonce(aux))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(alternateSignature.Serialize(), req.Signature) {
		t.Fatal("custom nonce did not produce a distinct valid signature")
	}
	alternate := proto.Clone(req).(*pb.FrostVaultTransitionSignedTx)
	alternate.Base.TxId = "migration-signed-other-member"
	alternate.Signature = alternateSignature.Serialize()
	alternateTx := &pb.AnyTx{Content: &pb.AnyTx_FrostVaultTransitionSignedTx{FrostVaultTransitionSignedTx: alternate}}
	if _, receipt, err := (&FrostVaultTransitionSignedTxHandler{}).DryRun(alternateTx, sv); err == nil || receipt.Status != "FAILED" {
		t.Fatalf("different signed package was not rejected: receipt=%+v err=%v", receipt, err)
	}
}

func TestTransitionSignedBTCCompressedDKGKey(t *testing.T) {
	sv := NewMockStateView()
	oldPriv, _ := migrationTestKey(t, 2)
	newPriv, _ := migrationTestKey(t, 3)
	oldPub, newPub := oldPriv.PubKey().SerializeCompressed(), newPriv.PubKey().SerializeCompressed()
	intent := &pb.FrostVaultMigrationState{MigrationId: chainpkg.MigrationID("btc", 0, 2), Chain: "btc", VaultId: 0, OldEpoch: 1, NewEpoch: 2, OldGroupPubkey: oldPub, NewGroupPubkey: newPub, NoFunds: true, TemplateHash: chainpkg.EmptyMigrationTemplateHash(), Status: migrationStatusIntentFinalized}
	intent.IntentHash = chainpkg.MigrationIntentHash("btc", 0, 1, 2, oldPub, newPub, intent.TemplateHash, true)
	seedBTCMigrationIntent(t, sv, oldPub, newPub, intent)
	signature, err := schnorr.Sign(oldPriv, intent.IntentHash)
	if err != nil {
		t.Fatal(err)
	}
	req := &pb.FrostVaultTransitionSignedTx{Base: &pb.BaseMessage{TxId: "compressed-key-migration", FromAddress: "old-member", ExecutedHeight: 100}, Chain: "btc", EpochId: 2, MigrationId: intent.MigrationId, MsgHash: intent.IntentHash, Signature: signature.Serialize(), TemplateHash: intent.TemplateHash}
	_, guardianPriv := btcDualTestKeys()
	guardianSig, _ := schnorr.Sign(guardianPriv, intent.IntentHash)
	req.GuardianSigs = [][]byte{guardianSig.Serialize()}
	if _, receipt, err := (&FrostVaultTransitionSignedTxHandler{}).DryRun(&pb.AnyTx{Content: &pb.AnyTx_FrostVaultTransitionSignedTx{FrostVaultTransitionSignedTx: req}}, sv); err != nil || receipt.Status != "SUCCEED" {
		t.Fatalf("real compressed DKG key failed migration: %+v %v", receipt, err)
	}
}

func TestTransitionSignedRejectsNonMemberBeforeSignatureVerification(t *testing.T) {
	sv := NewMockStateView()
	_, oldPub := migrationTestKey(t, 2)
	_, newPub := migrationTestKey(t, 3)
	intent := &pb.FrostVaultMigrationState{
		MigrationId: chainpkg.MigrationID("btc", 0, 2), Chain: "btc", VaultId: 0,
		OldEpoch: 1, NewEpoch: 2, OldGroupPubkey: oldPub, NewGroupPubkey: newPub,
		TemplateHash: chainpkg.EmptyMigrationTemplateHash(), NoFunds: true, Status: migrationStatusIntentFinalized,
	}
	intent.IntentHash = chainpkg.MigrationIntentHash("btc", 0, 1, 2, oldPub, newPub, intent.TemplateHash, true)
	seedBTCMigrationIntent(t, sv, oldPub, newPub, intent)
	req := &pb.FrostVaultTransitionSignedTx{
		Base:  &pb.BaseMessage{TxId: "nonmember", FromAddress: "outsider", ExecutedHeight: 60},
		Chain: "btc", OldVaultId: 0, NewVaultId: 0, EpochId: 2, MigrationId: intent.MigrationId,
	}
	_, receipt, err := (&FrostVaultTransitionSignedTxHandler{}).DryRun(&pb.AnyTx{Content: &pb.AnyTx_FrostVaultTransitionSignedTx{FrostVaultTransitionSignedTx: req}}, sv)
	if err == nil || receipt.Error != "submitter is not an old vault member" {
		t.Fatalf("nonmember was not rejected: receipt=%+v err=%v", receipt, err)
	}
}

func TestTransitionSignedBTCSweepReplacesOldInputsWithNewKeyOutput(t *testing.T) {
	sv := NewMockStateView()
	oldPriv, oldPub := migrationTestKey(t, 2)
	_, newPub := migrationTestKey(t, 3)
	oldScript, _, _, err := utils.BuildDualCustodyTaproot(oldPub, btcDualTestGuardianPubkey())
	if err != nil {
		t.Fatal(err)
	}
	const total = uint64(100000)
	utxo := chainpkg.UTXO{TxID: strings.Repeat("11", 32), Vout: 0, Amount: total, ScriptPubKey: oldScript, ConfirmHeight: 10}
	result, template, err := btc.BuildMigrationTemplate("btc", 0, 1, 2, []chainpkg.UTXO{utxo}, newPub, "mainnet", btcTestFeeRateSatsPerVByte, btcDualTestGuardianPubkey(), [][]byte{oldPub})
	if err != nil {
		t.Fatal(err)
	}
	intent := &pb.FrostVaultMigrationState{
		MigrationId: chainpkg.MigrationID("btc", 0, 2), Chain: "btc", VaultId: 0,
		OldEpoch: 1, NewEpoch: 2, OldGroupPubkey: oldPub, NewGroupPubkey: newPub,
		TemplateHash: result.TemplateHash, TemplateData: result.TemplateData, Status: migrationStatusIntentFinalized,
	}
	intent.IntentHash = chainpkg.MigrationIntentHash("btc", 0, 1, 2, oldPub, newPub, result.TemplateHash, false)
	seedBTCMigrationIntent(t, sv, oldPub, newPub, intent)
	stored := &pb.FrostUtxo{Txid: utxo.TxID, Vout: 0, Amount: strconv.FormatUint(total, 10), VaultId: 0, FinalizeHeight: 10, ScriptPubkey: oldScript}
	storedData, _ := proto.Marshal(stored)
	sv.Set(keys.KeyFrostBtcUtxo(0, utxo.TxID, 0), storedData)
	sv.Set(keys.KeyFrostBtcUtxoFIFOIndex(0, 1), storedData)
	sv.Set(keys.KeyFrostBtcSpendableUtxoIndex(0, 1), storedData)
	sv.Set(keys.KeyFrostBtcUtxoSeqRef(0, utxo.TxID, 0), []byte("1"))
	sv.Set(keys.KeyFrostBtcUtxoFIFOSeq(0), []byte("1"))
	sv.Set(keys.KeyFrostBtcUtxoFIFOHead(0), []byte("1"))
	btcAsset := testBridgeAsset(t, "btc", "")
	sv.Set(keys.KeyFrostVaultAvailableBalance("btc", btcAsset.ID, 0), []byte(strconv.FormatUint(total, 10)))

	sighashes, err := template.ComputeTaprootSighash([][]byte{oldScript}, btc.SighashDefault)
	if err != nil {
		t.Fatal(err)
	}
	inputSig, _ := schnorr.Sign(oldPriv, sighashes[0])
	authSig, _ := schnorr.Sign(oldPriv, intent.IntentHash)
	_, guardianPriv := btcDualTestKeys()
	guardianInput, _ := schnorr.Sign(guardianPriv, sighashes[0])
	guardianIntent, _ := schnorr.Sign(guardianPriv, intent.IntentHash)
	rawTx, _ := template.BuildSignedRawTx([][]byte{inputSig.Serialize()}, [][]byte{guardianInput.Serialize()})
	req := &pb.FrostVaultTransitionSignedTx{
		Base:  &pb.BaseMessage{TxId: "btc-sweep", FromAddress: "old-member", ExecutedHeight: 70},
		Chain: "btc", OldVaultId: 0, NewVaultId: 0, EpochId: 2, MigrationId: intent.MigrationId,
		MsgHash: intent.IntentHash, Signature: authSig.Serialize(), TemplateHash: intent.TemplateHash, TemplateData: intent.TemplateData,
		InputSigs: [][]byte{inputSig.Serialize()}, GuardianSigs: [][]byte{guardianInput.Serialize(), guardianIntent.Serialize()}, ScriptPubkeys: [][]byte{oldScript}, RawTx: rawTx,
	}
	if _, receipt, err := (&FrostVaultTransitionSignedTxHandler{}).DryRun(&pb.AnyTx{Content: &pb.AnyTx_FrostVaultTransitionSignedTx{FrostVaultTransitionSignedTx: req}}, sv); err != nil || receipt.Status != "SUCCEED" {
		t.Fatalf("BTC sweep migration failed: receipt=%+v err=%v", receipt, err)
	}
	assertPermanentMigrationDelivery(t, sv, intent.MigrationId)
	if _, exists, _ := sv.Get(keys.KeyFrostBtcLockedUtxo(0, utxo.TxID, 0)); exists {
		t.Fatal("spent migration input kept a permanent lock record")
	}
	if _, exists, _ := sv.Get(keys.KeyFrostBtcUtxo(0, utxo.TxID, 0)); exists {
		t.Fatal("spent migration input kept its full UTXO record")
	}
	if _, exists, _ := sv.Get(keys.KeyFrostBtcSpendableUtxoIndex(0, 1)); exists {
		t.Fatal("old migration input remains spendable")
	}
	txid, _ := btcSignedTemplateTxID(template, [][]byte{inputSig.Serialize()})
	newData, exists, _ := sv.Get(keys.KeyFrostBtcUtxo(0, txid, 0))
	var newUTXO pb.FrostUtxo
	if !exists || proto.Unmarshal(newData, &newUTXO) != nil {
		t.Fatal("new-key migration output was not recorded")
	}
	controlled, err := btcUTXOControlledBy(newPub, &newUTXO, btcDualTestGuardianPubkey())
	if err != nil || !controlled {
		t.Fatalf("migration output is not controlled by new key: %v", err)
	}
	available, _ := getVaultAvailableBalance(sv, "btc", btcAsset.ID, 0)
	if available.Cmp(new(big.Int).SetUint64(template.Outputs[0].Amount)) != 0 {
		t.Fatalf("available balance=%s want=%d", available, template.Outputs[0].Amount)
	}
}
