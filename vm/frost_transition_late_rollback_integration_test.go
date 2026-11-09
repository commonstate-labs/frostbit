package vm_test

import (
	"bytes"
	"math/big"
	"strconv"
	"strings"
	"testing"

	bridgeasset "dex/frost/asset"
	chainpkg "dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/keys"
	"dex/pb"
	"dex/utils"
	"dex/vm"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/wire"
	"google.golang.org/protobuf/proto"
)

func TestFrostMigrationLateFailureRollsBackUTXOMutations(t *testing.T) {
	db := NewMockDB()
	oldPrivate, oldPublic := formalRollbackBTCKey(31)
	_, newPublic := formalRollbackBTCKey(37)
	guardianPrivate, guardianPublic := formalRollbackBTCKey(41)
	oldScript, _, _, err := utils.BuildDualCustodyTaproot(oldPublic, guardianPublic)
	if err != nil {
		t.Fatal(err)
	}
	const total = uint64(100000)
	utxo := chainpkg.UTXO{
		TxID: strings.Repeat("42", 32), Vout: 0, Amount: total,
		ScriptPubKey: oldScript, ConfirmHeight: 10,
	}
	templateResult, template, err := btc.BuildMigrationTemplate(
		"btc", 0, 1, 2, []chainpkg.UTXO{utxo}, newPublic, "mainnet", btcTestFeeRateSatsPerVByte, guardianPublic, [][]byte{oldPublic},
	)
	if err != nil {
		t.Fatal(err)
	}
	intent := &pb.FrostVaultMigrationState{
		MigrationId: chainpkg.MigrationID("btc", 0, 2), Chain: "btc", VaultId: 0,
		OldEpoch: 1, NewEpoch: 2, OldGroupPubkey: oldPublic, NewGroupPubkey: newPublic,
		TemplateHash: templateResult.TemplateHash, TemplateData: templateResult.TemplateData,
		Status: "INTENT_FINALIZED",
	}
	intent.IntentHash = chainpkg.MigrationIntentHash(
		"btc", 0, 1, 2, oldPublic, newPublic, intent.TemplateHash, false,
	)
	transition := &pb.VaultTransitionState{
		Chain: "btc", VaultId: 0, EpochId: 2,
		SignAlgo:  pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		DkgStatus: "KEY_READY", Lifecycle: "DRAINING",
		OldCommitteeMembers: []string{"old-member"}, NewCommitteeMembers: []string{"new-member"},
		OldGroupPubkey: oldPublic, NewGroupPubkey: newPublic,
		MigrationJobId: intent.MigrationId, MigrationStatus: "INTENT_FINALIZED",
		MigrationTemplateHash: intent.TemplateHash, MigrationIntentHash: intent.IntentHash,
	}
	previous := &pb.VaultTransitionState{
		Chain: "btc", VaultId: 0, EpochId: 1,
		SignAlgo:  pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		DkgStatus: "KEY_READY", Lifecycle: "ACTIVE",
		NewCommitteeMembers: []string{"old-member"}, NewGroupPubkey: oldPublic,
	}
	formalRollbackPutProto(t, db, keys.KeyFrostVaultTransition("btc", 0, 2), transition)
	formalRollbackPutProto(t, db, keys.KeyFrostVaultTransition("btc", 0, 1), previous)
	db.data[keys.KeyFrostVaultTransitionActive("btc", 0, 2)] = []byte(keys.KeyFrostVaultTransition("btc", 0, 2))
	formalRollbackPutProto(t, db, keys.KeyFrostVaultMigration("btc", 0, 2), intent)
	formalRollbackPutProto(t, db, keys.KeyFrostVaultState("btc", 0), &pb.FrostVaultState{
		Chain: "btc", VaultId: 0, KeyEpoch: 1, GroupPubkey: oldPublic,
		SignAlgo: pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		Status:   "ACTIVE", CommitteeMembers: []string{"old-member"},
	})
	formalRollbackPutProto(t, db, keys.KeyFrostVaultConfig("btc", 0), &pb.FrostVaultConfig{
		Chain: "btc", VaultCount: 1, BtcNetwork: "mainnet", BtcFeeSatsPerVbyte: btcTestFeeRateSatsPerVByte, GuardianPubkey: guardianPublic,
	})
	storedUTXO := &pb.FrostUtxo{
		Txid: utxo.TxID, Vout: utxo.Vout, Amount: strconv.FormatUint(total, 10),
		VaultId: 0, FinalizeHeight: 10, ScriptPubkey: oldScript,
	}
	storedUTXOBytes, err := proto.Marshal(storedUTXO)
	if err != nil {
		t.Fatal(err)
	}
	db.data[keys.KeyFrostBtcUtxo(0, utxo.TxID, utxo.Vout)] = storedUTXOBytes
	db.data[keys.KeyFrostBtcUtxoFIFOIndex(0, 1)] = storedUTXOBytes
	db.data[keys.KeyFrostBtcSpendableUtxoIndex(0, 1)] = storedUTXOBytes
	db.data[keys.KeyFrostBtcUtxoSeqRef(0, utxo.TxID, utxo.Vout)] = []byte("1")
	db.data[keys.KeyFrostBtcUtxoFIFOSeq(0)] = []byte("1")
	db.data[keys.KeyFrostBtcUtxoFIFOHead(0)] = []byte("1")
	btcAsset, err := bridgeasset.Resolve("btc", "")
	if err != nil {
		t.Fatal(err)
	}
	db.data[keys.KeyFrostVaultAvailableBalance("btc", btcAsset.ID, 0)] = []byte(strconv.FormatUint(total, 10))
	formalRollbackPutProto(t, db, keys.KeyBalance("old-member", vm.FeeToken), &pb.TokenBalanceRecord{
		Address: "old-member", Token: vm.FeeToken, Balance: &pb.TokenBalance{Balance: "10000"},
	})

	sighashes, err := template.ComputeTaprootSighash([][]byte{oldScript}, btc.SighashDefault)
	if err != nil {
		t.Fatal(err)
	}
	inputSignature, err := schnorr.Sign(oldPrivate, sighashes[0])
	if err != nil {
		t.Fatal(err)
	}
	authoritySignature, err := schnorr.Sign(oldPrivate, intent.IntentHash)
	if err != nil {
		t.Fatal(err)
	}
	guardianInput, _ := schnorr.Sign(guardianPrivate, sighashes[0])
	guardianIntent, _ := schnorr.Sign(guardianPrivate, intent.IntentHash)
	rawTx, err := template.BuildSignedRawTx([][]byte{inputSignature.Serialize()}, [][]byte{guardianInput.Serialize()})
	if err != nil {
		t.Fatal(err)
	}
	var signed wire.MsgTx
	if err := signed.Deserialize(bytes.NewReader(rawTx)); err != nil {
		t.Fatal(err)
	}
	newTxID := signed.TxHash().String()

	                                                                     
	                                                                        
	db.data[keys.KeyFrostArtifactMeta(newTxID)] = []byte(`{"version":1,"artifact_id":"conflict"}`)
	tx := &pb.AnyTx{Content: &pb.AnyTx_FrostVaultTransitionSignedTx{FrostVaultTransitionSignedTx: &pb.FrostVaultTransitionSignedTx{
		Base: &pb.BaseMessage{
			TxId: "formal-late-rollback", FromAddress: "old-member", Fee: vm.MinTxFee.String(),
			Status: pb.Status_PENDING, ExecutedHeight: 70,
		},
		Chain: "btc", OldVaultId: 0, NewVaultId: 0, EpochId: 2,
		MigrationId: intent.MigrationId, MsgHash: intent.IntentHash,
		Signature: authoritySignature.Serialize(), TemplateHash: intent.TemplateHash,
		TemplateData: intent.TemplateData, InputSigs: [][]byte{inputSignature.Serialize()},
		ScriptPubkeys: [][]byte{oldScript}, RawTx: rawTx, GuardianSigs: [][]byte{guardianInput.Serialize(), guardianIntent.Serialize()},
	}}}
	block := &pb.Block{
		BlockHash: "block-formal-late-rollback",
		Header:    &pb.BlockHeader{PrevBlockHash: "genesis", Height: 70, Timestamp: 700},
		Body:      []*pb.AnyTx{tx},
	}
	registry := vm.NewHandlerRegistry()
	if err := vm.RegisterDefaultHandlers(registry); err != nil {
		t.Fatal(err)
	}
	executor := vm.NewExecutor(db, registry, vm.NewSpecExecLRU(4))
	if err := executor.CommitFinalizedBlock(block); err != nil {
		t.Fatalf("late business failure invalidated the block: %v", err)
	}
	receipt := executor.TakeCommittedReceipts(block.BlockHash)[tx.GetTxId()]
	if receipt == nil || receipt.Status != "FAILED" || !strings.Contains(receipt.Error, "artifact ID already has different metadata") {
		t.Fatalf("unexpected late-failure receipt: %+v", receipt)
	}

	formalRollbackRequireKey(t, db, keys.KeyFrostBtcUtxo(0, utxo.TxID, utxo.Vout), storedUTXOBytes)
	formalRollbackRequireKey(t, db, keys.KeyFrostBtcSpendableUtxoIndex(0, 1), storedUTXOBytes)
	formalRollbackRequireKey(t, db, keys.KeyFrostBtcUtxoSeqRef(0, utxo.TxID, utxo.Vout), []byte("1"))
	formalRollbackRequireKey(t, db, keys.KeyFrostBtcUtxoFIFOSeq(0), []byte("1"))
	formalRollbackRequireKey(t, db, keys.KeyFrostVaultAvailableBalance("btc", btcAsset.ID, 0), []byte(strconv.FormatUint(total, 10)))
	formalRollbackRequireAbsent(t, db, keys.KeyFrostBtcUtxo(0, newTxID, 0))
	formalRollbackRequireAbsent(t, db, keys.KeyFrostBtcUtxoArtifact(0, newTxID, 0))
	formalRollbackRequireAbsent(t, db, keys.KeyArchiveFrostArtifact(newTxID))

	var vault pb.FrostVaultState
	formalRollbackGetProto(t, db, keys.KeyFrostVaultState("btc", 0), &vault)
	if vault.KeyEpoch != 1 || !bytes.Equal(vault.GroupPubkey, oldPublic) {
		t.Fatalf("failed migration switched vault authority: %+v", &vault)
	}
	var current pb.VaultTransitionState
	formalRollbackGetProto(t, db, keys.KeyFrostVaultTransition("btc", 0, 2), &current)
	if current.Lifecycle != "DRAINING" || current.MigrationStatus != "INTENT_FINALIZED" {
		t.Fatalf("failed migration advanced transition: %+v", &current)
	}
	var old pb.VaultTransitionState
	formalRollbackGetProto(t, db, keys.KeyFrostVaultTransition("btc", 0, 1), &old)
	if old.Lifecycle != "ACTIVE" {
		t.Fatalf("failed migration retired old transition: %+v", &old)
	}
}

func formalRollbackBTCKey(scalar int64) (*btcec.PrivateKey, []byte) {
	private, _ := btcec.PrivKeyFromBytes(big.NewInt(scalar).FillBytes(make([]byte, 32)))
	return private, schnorr.SerializePubKey(private.PubKey())
}

func formalRollbackPutProto(t *testing.T, db *MockDB, key string, value proto.Message) {
	t.Helper()
	raw, err := proto.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	db.data[key] = raw
}

func formalRollbackGetProto(t *testing.T, db *MockDB, key string, value proto.Message) {
	t.Helper()
	raw, err := db.Get(key)
	if err != nil || len(raw) == 0 {
		t.Fatalf("missing %s: %v", key, err)
	}
	if err := proto.Unmarshal(raw, value); err != nil {
		t.Fatal(err)
	}
}

func formalRollbackRequireKey(t *testing.T, db *MockDB, key string, want []byte) {
	t.Helper()
	got, err := db.Get(key)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("state changed at %s: got=%x want=%x err=%v", key, got, want, err)
	}
}

func formalRollbackRequireAbsent(t *testing.T, db *MockDB, key string) {
	t.Helper()
	got, err := db.Get(key)
	if err != nil || len(got) != 0 {
		t.Fatalf("rolled-back state survived at %s: value=%x err=%v", key, got, err)
	}
}
