package vm

import (
	"strings"
	"testing"

	"dex/frost/chain/btc"
	"dex/keys"
	"dex/pb"
	statedb "dex/stateDB"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"google.golang.org/protobuf/proto"
)

const (
	testBTCTaprootAddress       = "tb1pdpd76eytdcdwnm62uuxve403srq0dtcls0uj488wjjy4z4lfm25sdwatf0"
	testBTCChangeTaprootAddress = "tb1pegupuqdwptus53zrs44mfjlm0ea5uxs46pugqulxurafw3jclp8q74sxd0"
)

func btcChangeAddressForTest(t *testing.T, scriptPubKey []byte) string {
	t.Helper()
	addr, err := btcutil.NewAddressTaproot(scriptPubKey[2:], &chaincfg.MainNetParams)
	if err != nil {
		t.Fatalf("build taproot change address: %v", err)
	}
	return addr.EncodeAddress()
}

func seedBTCChangeTestState(t *testing.T, sv StateView, vaultID uint32, withdrawID string, template *btc.BTCTemplate) {
	t.Helper()
	vaultCfgData, _ := proto.Marshal(&pb.FrostVaultConfig{
		GuardianPubkey: btcDualTestGuardianPubkey(), Chain: "btc", VaultCount: 1, BtcNetwork: "mainnet", BtcFeeSatsPerVbyte: btcTestFeeRateSatsPerVByte,
	})
	sv.Set(keys.KeyFrostVaultConfig("btc", 0), vaultCfgData)
	vaultData, _ := proto.Marshal(&pb.FrostVaultState{
		Chain:            "btc",
		VaultId:          vaultID,
		KeyEpoch:         1,
		Status:           "ACTIVE",
		SignAlgo:         pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		GroupPubkey:      btcDualTestOperatorPubkey(),
		CommitteeMembers: []string{"coordinator"},
	})
	sv.Set(keys.KeyFrostVaultState("btc", vaultID), vaultData)
	sv.Set(keys.KeyFrostVaultAvailableBalance("btc", "BTC", vaultID), []byte("100000"))
	sv.Set(keys.KeyFrostWithdrawFIFOHead("btc", "BTC"), []byte("1"))
	sv.Set(keys.KeyFrostWithdrawFIFOSeq("btc", "BTC"), []byte("1"))
	sv.Set(keys.KeyFrostWithdrawFIFOIndex("btc", "BTC", 1), []byte(withdrawID))

	withdrawData, _ := proto.Marshal(&pb.FrostWithdrawState{
		WithdrawId: withdrawID,
		Seq:        1,
		Chain:      "btc",
		Asset:      "BTC",
		To:         testBTCTaprootAddress,
		Amount:     "10000",
		Status:     "QUEUED",
		VaultId:    vaultID,
	})
	sv.Set(keys.KeyFrostWithdraw(withdrawID), withdrawData)

	source := &pb.FrostUtxo{
		Txid:         template.Inputs[0].TxID,
		Vout:         template.Inputs[0].Vout,
		Amount:       "100000",
		VaultId:      vaultID,
		ScriptPubkey: btcDualTestScript(t),
	}
	sourceData, _ := proto.Marshal(source)
	sv.Set(keys.KeyFrostBtcUtxo(vaultID, source.Txid, source.Vout), sourceData)
	sv.Set(keys.KeyFrostBtcUtxoFIFOHead(vaultID), []byte("1"))
	sv.Set(keys.KeyFrostBtcUtxoFIFOSeq(vaultID), []byte("1"))
	sv.Set(keys.KeyFrostBtcUtxoFIFOIndex(vaultID, 1), sourceData)
	if err := setBTCSpendableUTXOIndex(sv, vaultID, 1, source, sourceData); err != nil {
		t.Fatalf("index source BTC UTXO: %v", err)
	}
}

func TestSetBTCSpendableUTXOIndexRejectsUnrepresentableAmount(t *testing.T) {
	for _, amount := range []string{"1.5", "18446744073709551616", "0"} {
		t.Run(amount, func(t *testing.T) {
			sv := newMemoryStateView(map[string][]byte{})
			utxo := &pb.FrostUtxo{Txid: strings.Repeat("1", 64), Amount: amount}
			data, err := proto.Marshal(utxo)
			if err != nil {
				t.Fatal(err)
			}
			if err := setBTCSpendableUTXOIndex(sv, 1, 1, utxo, data); err == nil {
				t.Fatalf("invalid BTC UTXO amount accepted: %q", amount)
			}
			if _, exists, _ := sv.Get(keys.KeyFrostBtcSpendableUtxoIndex(1, 1)); exists {
				t.Fatal("failed index validation left a spendable UTXO entry")
			}
		})
	}
}

func TestFrostWithdrawSignedRecordsBTCChangeAsAvailable(t *testing.T) {
	sv := newMemoryStateView(map[string][]byte{})
	vaultID := uint32(2)
	withdrawID := "wd_btc_change"
	inputSigs := [][]byte{make([]byte, 64)}
	scriptPubKey := btcDualTestScript(t)
	changeAddress := btcChangeAddressForTest(t, scriptPubKey)
	template := &btc.BTCTemplate{
		Version:  2,
		LockTime: 0,
		Inputs: []btc.TxInput{{
			TxID:     strings.Repeat("11", 32),
			Vout:     0,
			Amount:   100000,
			Sequence: btc.DefaultSequence,
		}},
		Outputs:     exactSingleBTCWithdrawOutputsForTest(t, 100000, 10000, 1, testBTCTaprootAddress, changeAddress),
		VaultID:     vaultID,
		KeyEpoch:    1,
		WithdrawIDs: []string{withdrawID},
	}
	btcDualDecorateTemplate(t, template)
	templateData, _ := template.ToJSON()
	seedBTCChangeTestState(t, sv, vaultID, withdrawID, template)
	jobID := claimBTCWithdrawForTest(t, sv, "BTC", template)

	oldVerify := verifySignatureFn
	verifySignatureFn = func(pb.SignAlgo, []byte, []byte, []byte) (bool, error) { return true, nil }
	defer func() { verifySignatureFn = oldVerify }()

	tx := &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: &pb.FrostWithdrawSignedTx{
		Base:               &pb.BaseMessage{TxId: "tx_signed_btc_change", ExecutedHeight: 101},
		JobId:              jobID,
		SignedPackageBytes: inputSigs[0],
		WithdrawIds:        []string{withdrawID},
		VaultId:            vaultID,
		KeyEpoch:           1,
		TemplateHash:       template.TemplateHash(),
		Chain:              "btc",
		Asset:              "BTC",
		TemplateData:       templateData,
		InputSigs:          inputSigs, GuardianSigs: [][]byte{make([]byte, 64)},
		ScriptPubkeys: [][]byte{scriptPubKey},
	}}}
	if _, receipt, err := (&FrostWithdrawSignedTxHandler{}).DryRun(tx, sv); err != nil || receipt.Status != "SUCCEED" {
		t.Fatalf("signed failed: err=%v receipt=%+v", err, receipt)
	}
	artifactID, found, err := sv.Get(keys.KeyFrostWithdrawArtifact(withdrawID))
	assertPermanentWithdrawalDelivery(t, sv, withdrawID, jobID)
	if err != nil || !found || len(artifactID) == 0 {
		t.Fatalf("BTC signed result lookup missing: %v", err)
	}
	if raw, found, err := sv.Get(keys.KeyArchiveFrostArtifact(string(artifactID))); err != nil || !found || len(raw) == 0 {
		t.Fatalf("BTC signed result does not resolve its archived raw transaction: %v", err)
	}

	available, _, _ := sv.Get(keys.KeyFrostVaultAvailableBalance("btc", "BTC", vaultID))
	if string(available) != "90000" {
		t.Fatalf("expected available change 90000, got %s", string(available))
	}
	head, _, _ := sv.Get(keys.KeyFrostBtcUtxoFIFOHead(vaultID))
	if string(head) != "2" {
		t.Fatalf("expected BTC UTXO head=2, got %s", string(head))
	}
	changeData, _, _ := sv.Get(keys.KeyFrostBtcUtxoFIFOIndex(vaultID, 2))
	var change pb.FrostUtxo
	if err := proto.Unmarshal(changeData, &change); err != nil {
		t.Fatalf("decode change utxo: %v", err)
	}
	if change.Amount != "90000" || change.Vout != 1 || change.VaultId != vaultID {
		t.Fatalf("unexpected change utxo: %+v", &change)
	}
	if _, exists, _ := sv.Get(keys.KeyFrostBtcSpendableUtxoIndex(vaultID, 1)); exists {
		t.Fatal("spent source UTXO should be removed from spendable index")
	}
	if _, exists, _ := sv.Get(keys.KeyFrostBtcUtxoSeqRef(vaultID, template.Inputs[0].TxID, template.Inputs[0].Vout)); exists {
		t.Fatal("spent source UTXO seq ref should be removed")
	}
	if indexedChange, exists, _ := sv.Get(keys.KeyFrostBtcSpendableUtxoIndex(vaultID, 2)); !exists || len(indexedChange) == 0 {
		t.Fatal("change UTXO should be added to spendable index")
	}
	if changeRef, exists, _ := sv.Get(keys.KeyFrostBtcUtxoSeqRef(vaultID, change.Txid, change.Vout)); !exists || string(changeRef) != "2" {
		t.Fatalf("change UTXO seq ref mismatch: exists=%v value=%q", exists, string(changeRef))
	}
}

func TestFrostWithdrawSignedRecordsBTCChangeWithStateDBNotFound(t *testing.T) {
	sv := newStateDBNotFoundStateView(map[string][]byte{})
	vaultID := uint32(4)
	withdrawID := "wd_btc_change_statedb"
	inputSigs := [][]byte{make([]byte, 64)}
	scriptPubKey := btcDualTestScript(t)
	changeAddress := btcChangeAddressForTest(t, scriptPubKey)
	template := &btc.BTCTemplate{
		Version:  2,
		LockTime: 0,
		Inputs: []btc.TxInput{{
			TxID:     strings.Repeat("44", 32),
			Vout:     0,
			Amount:   100000,
			Sequence: btc.DefaultSequence,
		}},
		Outputs:     exactSingleBTCWithdrawOutputsForTest(t, 100000, 10000, 1, testBTCTaprootAddress, changeAddress),
		VaultID:     vaultID,
		KeyEpoch:    1,
		WithdrawIDs: []string{withdrawID},
	}
	btcDualDecorateTemplate(t, template)
	templateData, _ := template.ToJSON()
	seedBTCChangeTestState(t, sv, vaultID, withdrawID, template)
	jobID := claimBTCWithdrawForTest(t, sv, "BTC", template)

	oldVerify := verifySignatureFn
	verifySignatureFn = func(pb.SignAlgo, []byte, []byte, []byte) (bool, error) { return true, nil }
	defer func() { verifySignatureFn = oldVerify }()

	tx := &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: &pb.FrostWithdrawSignedTx{
		Base:               &pb.BaseMessage{TxId: "tx_signed_btc_change_statedb", ExecutedHeight: 101},
		JobId:              jobID,
		SignedPackageBytes: inputSigs[0],
		WithdrawIds:        []string{withdrawID},
		VaultId:            vaultID,
		KeyEpoch:           1,
		TemplateHash:       template.TemplateHash(),
		Chain:              "btc",
		Asset:              "BTC",
		TemplateData:       templateData,
		InputSigs:          inputSigs, GuardianSigs: [][]byte{make([]byte, 64)},
		ScriptPubkeys: [][]byte{scriptPubKey},
	}}}
	if _, receipt, err := (&FrostWithdrawSignedTxHandler{}).DryRun(tx, sv); err != nil || receipt.Status != "SUCCEED" {
		t.Fatalf("signed should tolerate missing change/lock keys: err=%v receipt=%+v", err, receipt)
	}

	changeData, exists, err := sv.Get(keys.KeyFrostBtcUtxoFIFOIndex(vaultID, 2))
	if err != nil || !exists {
		t.Fatalf("change UTXO FIFO missing: exists=%v err=%v", exists, err)
	}
	var change pb.FrostUtxo
	if err := proto.Unmarshal(changeData, &change); err != nil {
		t.Fatalf("decode change utxo: %v", err)
	}
	if change.Amount != "90000" || change.Vout != 1 || change.VaultId != vaultID {
		t.Fatalf("unexpected change utxo: %+v", &change)
	}
}

func TestApplyFrostBtcChangeOutputsBackfillsSignedJob(t *testing.T) {
	sv := newMemoryStateView(map[string][]byte{})
	vaultID := uint32(3)
	withdrawID := "wd_btc_change_repair"
	template := &btc.BTCTemplate{
		Version:  2,
		LockTime: 0,
		Inputs: []btc.TxInput{{
			TxID:     strings.Repeat("22", 32),
			Vout:     0,
			Amount:   100000,
			Sequence: btc.DefaultSequence,
		}},
		Outputs: []btc.TxOutput{
			{Address: testBTCTaprootAddress, Amount: 10000},
			{Address: testBTCChangeTaprootAddress, Amount: 89000},
		},
		VaultID:     vaultID,
		KeyEpoch:    1,
		WithdrawIDs: []string{withdrawID},
	}
	btcDualDecorateTemplate(t, template)
	templateData, _ := template.ToJSON()
	seedBTCChangeTestState(t, sv, vaultID, withdrawID, template)
	sv.Set(keys.KeyFrostBtcLockedUtxo(vaultID, template.Inputs[0].TxID, template.Inputs[0].Vout), []byte("job_btc_change_repair"))
	sv.Set(keys.KeyFrostVaultAvailableBalance("btc", "BTC", vaultID), []byte("0"))

	jobData, _ := proto.Marshal(&pb.FrostWithdrawJobState{
		JobId:        "job_btc_change_repair",
		Chain:        "btc",
		Asset:        "BTC",
		VaultId:      vaultID,
		KeyEpoch:     1,
		WithdrawIds:  []string{withdrawID},
		TemplateData: templateData,
		Status:       frostWithdrawJobStatusSigned,
		SignedTxId:   "tx_signed_repair",
	})
	sv.Set(keys.KeyFrostWithdrawJob("job_btc_change_repair"), jobData)
	pkgData, _ := proto.Marshal(&pb.FrostSignedPackage{
		JobId:              "job_btc_change_repair",
		Idx:                0,
		SignedPackageBytes: make([]byte, 64),
	})
	sv.Set(keys.KeyFrostSignedPackage("job_btc_change_repair", 0), pkgData)

	applyFrostBtcChangeOutputs(sv, 102)
	available, _, _ := sv.Get(keys.KeyFrostVaultAvailableBalance("btc", "BTC", vaultID))
	if string(available) != "89000" {
		t.Fatalf("expected repaired available 89000, got %s", string(available))
	}
}

func newStateDBNotFoundStateView(base map[string][]byte) StateView {
	readFn := func(key string) ([]byte, error) {
		v, ok := base[key]
		if !ok {
			return nil, statedb.ErrNotFound
		}
		out := make([]byte, len(v))
		copy(out, v)
		return out, nil
	}
	scanFn := func(prefix string) (map[string][]byte, error) {
		out := make(map[string][]byte)
		for k, v := range base {
			if strings.HasPrefix(k, prefix) {
				copied := make([]byte, len(v))
				copy(copied, v)
				out[k] = copied
			}
		}
		return out, nil
	}
	return NewStateView(readFn, scanFn)
}
