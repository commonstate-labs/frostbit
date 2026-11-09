package vm

import (
	"bytes"
	"crypto/sha256"
	"math/big"
	"strconv"
	"strings"
	"testing"

	"dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/frost/chain/evm"
	"dex/frost/core/curve"
	"dex/keys"
	"dex/pb"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"google.golang.org/protobuf/proto"
)

func claimBTCWithdrawForTest(t *testing.T, sv StateView, asset string, template *btc.BTCTemplate) string {
	t.Helper()
	if _, exists, err := sv.Get(keys.KeyFrostVaultConfig("btc", 0)); err != nil {
		t.Fatalf("load BTC vault config: %v", err)
	} else if !exists {
		cfgData, err := proto.Marshal(&pb.FrostVaultConfig{
			Chain: "btc", VaultCount: 1, BtcNetwork: "mainnet", BtcFeeSatsPerVbyte: btcTestFeeRateSatsPerVByte, GuardianPubkey: btcDualTestGuardianPubkey(),
		})
		if err != nil {
			t.Fatalf("marshal BTC vault config: %v", err)
		}
		sv.Set(keys.KeyFrostVaultConfig("btc", 0), cfgData)
	}
	vaultData, exists, err := sv.Get(keys.KeyFrostVaultState("btc", template.VaultID))
	if err != nil || !exists {
		t.Fatalf("load BTC vault state: exists=%v err=%v", exists, err)
	}
	var vault pb.FrostVaultState
	if err := proto.Unmarshal(vaultData, &vault); err != nil {
		t.Fatalf("unmarshal BTC vault state: %v", err)
	}
	if len(vault.CommitteeMembers) == 0 {
		vault.CommitteeMembers = []string{"coordinator"}
		vaultData, _ = proto.Marshal(&vault)
		sv.Set(keys.KeyFrostVaultState("btc", template.VaultID), vaultData)
	}
	var totalInput uint64
	for i, input := range template.Inputs {
		data, found, err := sv.Get(keys.KeyFrostBtcUtxo(template.VaultID, input.TxID, input.Vout))
		if err != nil || !found {
			t.Fatalf("load BTC input %s:%d: found=%v err=%v", input.TxID, input.Vout, found, err)
		}
		var utxo pb.FrostUtxo
		if err := proto.Unmarshal(data, &utxo); err != nil {
			t.Fatalf("unmarshal BTC input: %v", err)
		}
		seq := uint64(i + 1)
		if err := setBTCSpendableUTXOIndex(sv, template.VaultID, seq, &utxo, data); err != nil {
			t.Fatalf("index BTC input: %v", err)
		}
		totalInput += input.Amount
	}
	if _, exists, _ := sv.Get(keys.KeyFrostVaultAvailableBalance("btc", asset, template.VaultID)); !exists {
		sv.Set(keys.KeyFrostVaultAvailableBalance("btc", asset, template.VaultID), []byte(strconv.FormatUint(totalInput, 10)))
	}
	firstData, exists, err := sv.Get(keys.KeyFrostWithdraw(template.WithdrawIDs[0]))
	if err != nil || !exists {
		t.Fatalf("load first BTC withdraw: exists=%v err=%v", exists, err)
	}
	first, err := unmarshalWithdrawRequest(firstData)
	if err != nil {
		t.Fatalf("unmarshal first BTC withdraw: %v", err)
	}
	templateData, _ := template.ToJSON()
	jobID := chain.ComputeWithdrawJobID("btc", asset, template.VaultID, first.Seq, template.TemplateHash(), template.KeyEpoch)
	claim := &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawClaimTx{FrostWithdrawClaimTx: &pb.FrostWithdrawClaimTx{
		Base:  &pb.BaseMessage{TxId: "claim_" + jobID, FromAddress: "coordinator", ExecutedHeight: 199},
		JobId: jobID, WithdrawIds: append([]string(nil), template.WithdrawIDs...), VaultId: template.VaultID,
		KeyEpoch: template.KeyEpoch, TemplateHash: template.TemplateHash(), Chain: "btc", Asset: asset, TemplateData: templateData,
	}}}
	if _, receipt, err := (&FrostWithdrawClaimTxHandler{}).DryRun(claim, sv); err != nil || receipt.Status != "SUCCEED" {
		t.Fatalf("BTC claim failed: err=%v receipt=%+v", err, receipt)
	}
	return jobID
}

func TestNormalizeBIP340PubKey(t *testing.T) {
	xOnly := make([]byte, 32)
	for i := range xOnly {
		xOnly[i] = byte(i + 1)
	}

	got32, err := normalizeBIP340PubKey(xOnly)
	if err != nil {
		t.Fatalf("normalize 32-byte pubkey failed: %v", err)
	}
	if len(got32) != 32 {
		t.Fatalf("unexpected normalized key length: got=%d want=32", len(got32))
	}

	compressed := append([]byte{0x02}, xOnly...)
	got33, err := normalizeBIP340PubKey(compressed)
	if err != nil {
		t.Fatalf("normalize 33-byte pubkey failed: %v", err)
	}
	if string(got33) != string(xOnly) {
		t.Fatal("normalized x-only pubkey mismatch")
	}

	_, err = normalizeBIP340PubKey(append([]byte{0x04}, xOnly...))
	if err == nil {
		t.Fatal("expected error for invalid compressed pubkey prefix")
	}
}

func TestVerifySignatureBIP340AcceptsCompressedPubKey(t *testing.T) {
	privKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatalf("new private key failed: %v", err)
	}

	msg := sha256.Sum256([]byte("bip340 compressed pubkey compatibility"))
	sig, err := schnorr.Sign(privKey, msg[:])
	if err != nil {
		t.Fatalf("schnorr sign failed: %v", err)
	}

	compressed := privKey.PubKey().SerializeCompressed()                  
	valid, err := verifySignature(pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340, compressed, msg[:], sig.Serialize())
	if err != nil {
		t.Fatalf("verifySignature returned error: %v", err)
	}
	if !valid {
		t.Fatal("expected signature to be valid")
	}
}

func TestVerifySignatureBIP340NoPubkeyLengthErrorForCompressed(t *testing.T) {
	pubKey := make([]byte, 33)
	pubKey[0] = 0x02
	msg := make([]byte, 32)
	sig := make([]byte, 64)

	_, err := verifySignature(pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340, pubKey, msg, sig)
	if err != nil && strings.Contains(err.Error(), "pubkey length") {
		t.Fatalf("unexpected pubkey length error for compressed key: %v", err)
	}
}

func TestVerifySignatureBN128UsesStrictAdapterValidation(t *testing.T) {
	grp := curve.NewBN256Group()
	pub := grp.SerializePoint(grp.ScalarBaseMult(big.NewInt(1)))

	_, err := verifySignature(pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128, pub, []byte("short"), make([]byte, evm.SchnorrSigSize))
	if err != evm.ErrInvalidMessage {
		t.Fatalf("err=%v want ErrInvalidMessage", err)
	}
}

func TestFrostWithdrawSignedDryRun_InvalidSignatureNoPanic(t *testing.T) {
	withdrawID := "withdraw_invalid_sig"
	jobID := "job_invalid_sig"
	vaultID := uint32(1)

	privKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatalf("new private key failed: %v", err)
	}

	template := &btc.BTCTemplate{
		Version:  2,
		LockTime: 0,
		Inputs: []btc.TxInput{
			{
				TxID:     strings.Repeat("11", 32),
				Vout:     0,
				Amount:   20000,
				Sequence: btc.DefaultSequence,
			},
		},
		Outputs:     exactSingleBTCWithdrawOutputsForTest(t, 20000, 20000, 1, testBTCTaprootAddress, ""),
		VaultID:     vaultID,
		KeyEpoch:    1,
		WithdrawIDs: []string{withdrawID},
	}
	scriptPubKey := btcDualDecorateTemplateKey(t, template, privKey.PubKey().SerializeCompressed())
	templateData, err := template.ToJSON()
	if err != nil {
		t.Fatalf("template json failed: %v", err)
	}

	wrongMsg := sha256.Sum256([]byte("wrong-message-for-invalid-signature"))
	wrongSig, err := schnorr.Sign(privKey, wrongMsg[:])
	if err != nil {
		t.Fatalf("sign wrong message failed: %v", err)
	}

	base := map[string][]byte{}
	readFn := func(key string) ([]byte, error) {
		v, ok := base[key]
		if !ok {
			return nil, nil
		}
		out := make([]byte, len(v))
		copy(out, v)
		return out, nil
	}
	scanFn := func(prefix string) (map[string][]byte, error) {
		return map[string][]byte{}, nil
	}
	sv := NewStateView(readFn, scanFn)

	withdraw := &pb.FrostWithdrawState{
		WithdrawId: withdrawID,
		TxId:       "tx_withdraw_invalid_sig",
		Seq:        1,
		Chain:      "btc",
		Asset:      "BTC",
		To:         testBTCTaprootAddress,
		Amount:     "20000",
		Status:     "QUEUED",
		VaultId:    vaultID,
	}
	withdrawData, err := proto.Marshal(withdraw)
	if err != nil {
		t.Fatalf("marshal withdraw failed: %v", err)
	}
	sv.Set(keys.KeyFrostWithdraw(withdrawID), withdrawData)
	sv.Set(keys.KeyFrostWithdrawFIFOHead("btc", "BTC"), []byte("1"))

	vaultState := &pb.FrostVaultState{
		VaultId:          vaultID,
		Chain:            "btc",
		KeyEpoch:         1,
		GroupPubkey:      privKey.PubKey().SerializeCompressed(),
		SignAlgo:         pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		Status:           "ACTIVE",
		CommitteeMembers: []string{"coordinator"},
	}
	vaultStateData, err := proto.Marshal(vaultState)
	if err != nil {
		t.Fatalf("marshal vault state failed: %v", err)
	}
	sv.Set(keys.KeyFrostVaultState("btc", vaultID), vaultStateData)
	utxoData, _ := proto.Marshal(&pb.FrostUtxo{Txid: template.Inputs[0].TxID, Vout: 0, Amount: "20000", VaultId: vaultID, ScriptPubkey: scriptPubKey})
	sv.Set(keys.KeyFrostBtcUtxo(vaultID, template.Inputs[0].TxID, 0), utxoData)
	jobID = claimBTCWithdrawForTest(t, sv, "BTC", template)

	_, guardianPrivate := btcDualTestKeys()
	validHashes, err := template.ComputeTaprootSighash([][]byte{scriptPubKey}, btc.SighashDefault)
	if err != nil {
		t.Fatal(err)
	}
	guardianSig, _ := schnorr.Sign(guardianPrivate, validHashes[0])
	tx := &pb.AnyTx{
		Content: &pb.AnyTx_FrostWithdrawSignedTx{
			FrostWithdrawSignedTx: &pb.FrostWithdrawSignedTx{
				Base: &pb.BaseMessage{
					TxId:           "tx_signed_invalid_sig",
					ExecutedHeight: 100,
					FromAddress:    "signer1",
				},
				JobId:              jobID,
				SignedPackageBytes: wrongSig.Serialize(),
				WithdrawIds:        []string{withdrawID},
				VaultId:            vaultID,
				KeyEpoch:           1,
				Chain:              "btc",
				Asset:              "BTC",
				TemplateData:       templateData,
				TemplateHash:       template.TemplateHash(),
				InputSigs:          [][]byte{wrongSig.Serialize()},
				GuardianSigs:       [][]byte{guardianSig.Serialize()},
				ScriptPubkeys:      [][]byte{scriptPubKey},
			},
		},
	}

	handler := &FrostWithdrawSignedTxHandler{}

	var receipt *Receipt
	var dryRunErr error
	panicked := false
	func() {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		_, receipt, dryRunErr = handler.DryRun(tx, sv)
	}()

	if panicked {
		t.Fatal("DryRun panicked on invalid signature")
	}
	if dryRunErr == nil {
		t.Fatal("expected DryRun error for invalid signature")
	}
	if receipt == nil {
		t.Fatal("expected receipt on invalid signature")
	}
	if receipt.Status != "FAILED" {
		t.Fatalf("unexpected receipt status: got=%s want=FAILED", receipt.Status)
	}
	if !strings.Contains(receipt.Error, "invalid signature") {
		t.Fatalf("unexpected receipt error: %s", receipt.Error)
	}
}

func TestDiagnoseSignatureMismatch_InputOrderMismatchHint(t *testing.T) {
	privKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatalf("new private key failed: %v", err)
	}

	msg1 := sha256.Sum256([]byte("msg-1"))
	msg2 := sha256.Sum256([]byte("msg-2"))
	sig2, err := schnorr.Sign(privKey, msg2[:])
	if err != nil {
		t.Fatalf("sign msg2 failed: %v", err)
	}

	pubKey := privKey.PubKey().SerializeCompressed()
	sighashes := [][]byte{msg1[:], msg2[:]}
	detail := diagnoseSignatureMismatch(
		pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		pubKey,
		sighashes,
		0,
		sig2.Serialize(),
	)

	if !strings.Contains(detail, "input order mismatch") {
		t.Fatalf("expected input order mismatch hint, got: %s", detail)
	}
}

func TestDiagnoseSignatureMismatch_AllZeroSignatureHint(t *testing.T) {
	msg := sha256.Sum256([]byte("msg"))
	pubKey := append([]byte{0x02}, make([]byte, 32)...)
	sig := make([]byte, 64)

	detail := diagnoseSignatureMismatch(
		pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		pubKey,
		[][]byte{msg[:]},
		0,
		sig,
	)

	if !strings.Contains(detail, "all zero") {
		t.Fatalf("expected all-zero hint, got: %s", detail)
	}
}

func TestFrostWithdrawSignedDryRun_MultiInput_AllValid(t *testing.T) {
	fixture := buildBTCTwoInputFixture(t)
	fixture.sv.Set(keys.KeyFrostVaultAvailableBalance("btc", "BTC", fixture.vaultID), []byte("50000"))

	tx := fixture.newTx([][]byte{
		fixture.sigForInput[0],
		fixture.sigForInput[1],
	})

	handler := &FrostWithdrawSignedTxHandler{}
	ops, receipt, err := handler.DryRun(tx, fixture.sv)
	if err != nil {
		t.Fatalf("expected success, got err: %v, receipt=%+v", err, receipt)
	}
	if receipt == nil || receipt.Status != "SUCCEED" {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	if len(ops) == 0 {
		t.Fatal("expected write ops for successful multi-input signed tx")
	}
}

func TestFrostWithdrawSignedDryRun_MultiInput_SecondSignatureInvalid(t *testing.T) {
	fixture := buildBTCTwoInputFixture(t)
	wrongMsg := sha256.Sum256([]byte("wrong-msg-for-second-input"))
	wrongSig, err := schnorr.Sign(fixture.privKey, wrongMsg[:])
	if err != nil {
		t.Fatalf("sign wrong message failed: %v", err)
	}

	tx := fixture.newTx([][]byte{
		fixture.sigForInput[0],
		wrongSig.Serialize(),
	})

	handler := &FrostWithdrawSignedTxHandler{}
	_, receipt, dryRunErr := handler.DryRun(tx, fixture.sv)
	if dryRunErr == nil {
		t.Fatal("expected DryRun error for invalid second input signature")
	}
	if receipt == nil {
		t.Fatal("expected receipt on signature verification failure")
	}
	if receipt.Status != "FAILED" {
		t.Fatalf("unexpected receipt status: got=%s want=FAILED", receipt.Status)
	}
	if !strings.Contains(receipt.Error, "input[1]") {
		t.Fatalf("expected failure at input[1], got: %s", receipt.Error)
	}
	if !strings.Contains(receipt.Error, "invalid signature") {
		t.Fatalf("expected invalid signature detail, got: %s", receipt.Error)
	}
}

func TestFrostWithdrawSignedDryRun_MultiInput_OrderMismatchHint(t *testing.T) {
	fixture := buildBTCTwoInputFixture(t)
	tx := fixture.newTx([][]byte{
		fixture.sigForInput[1],           
		fixture.sigForInput[0],           
	})

	handler := &FrostWithdrawSignedTxHandler{}
	_, receipt, dryRunErr := handler.DryRun(tx, fixture.sv)
	if dryRunErr == nil {
		t.Fatal("expected DryRun error for swapped multi-input signatures")
	}
	if receipt == nil {
		t.Fatal("expected receipt on signature verification failure")
	}
	if receipt.Status != "FAILED" {
		t.Fatalf("unexpected receipt status: got=%s want=FAILED", receipt.Status)
	}
	if !strings.Contains(receipt.Error, "input[0]") {
		t.Fatalf("expected failure at input[0], got: %s", receipt.Error)
	}
	if !strings.Contains(receipt.Error, "input order mismatch") {
		t.Fatalf("expected input order mismatch hint, got: %s", receipt.Error)
	}
}

func TestFrostWithdrawSignedDryRun_MultiInput_VerifyErrorIncludesInputIndex(t *testing.T) {
	fixture := buildBTCTwoInputFixture(t)

	                                                                                 
	badVaultState := &pb.FrostVaultState{
		VaultId:          fixture.vaultID,
		Chain:            "btc",
		KeyEpoch:         1,
		GroupPubkey:      append([]byte{0x04}, make([]byte, 32)...),
		SignAlgo:         pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		Status:           "ACTIVE",
		CommitteeMembers: []string{"coordinator"},
	}
	badVaultStateData, err := proto.Marshal(badVaultState)
	if err != nil {
		t.Fatalf("marshal bad vault state failed: %v", err)
	}
	fixture.sv.Set(keys.KeyFrostVaultState("btc", fixture.vaultID), badVaultStateData)

	tx := fixture.newTx([][]byte{
		fixture.sigForInput[0],
		fixture.sigForInput[1],
	})

	handler := &FrostWithdrawSignedTxHandler{}
	_, receipt, dryRunErr := handler.DryRun(tx, fixture.sv)
	if dryRunErr == nil {
		t.Fatal("expected DryRun error for verifySignature error path")
	}
	if receipt == nil {
		t.Fatal("expected receipt on signature verification failure")
	}
	if receipt.Status != "FAILED" {
		t.Fatalf("unexpected receipt status: got=%s want=FAILED", receipt.Status)
	}
	if !strings.Contains(receipt.Error, "input[0]") {
		t.Fatalf("expected failure at input[0], got: %s", receipt.Error)
	}
	if !strings.Contains(receipt.Error, "invalid compressed pubkey prefix") {
		t.Fatalf("expected verify error detail, got: %s", receipt.Error)
	}
}

type btcTwoInputFixture struct {
	sv           StateView
	privKey      *btcec.PrivateKey
	templateData []byte
	templateHash []byte
	scriptPubKey []byte
	sigForInput  [][]byte
	guardianSigs [][]byte
	vaultID      uint32
	withdrawID   string
	jobID        string
}

func (f *btcTwoInputFixture) newTx(inputSigs [][]byte) *pb.AnyTx {
	inputSpks := [][]byte{
		append([]byte(nil), f.scriptPubKey...),
		append([]byte(nil), f.scriptPubKey...),
	}

	return &pb.AnyTx{
		Content: &pb.AnyTx_FrostWithdrawSignedTx{
			FrostWithdrawSignedTx: &pb.FrostWithdrawSignedTx{
				Base: &pb.BaseMessage{
					TxId:           "tx_signed_multi_input",
					ExecutedHeight: 200,
					FromAddress:    "signer1",
				},
				JobId:              f.jobID,
				SignedPackageBytes: bytes.Join(inputSigs, nil),
				WithdrawIds:        []string{f.withdrawID},
				VaultId:            f.vaultID,
				KeyEpoch:           1,
				Chain:              "btc",
				Asset:              "BTC",
				TemplateData:       f.templateData,
				TemplateHash:       append([]byte(nil), f.templateHash...),
				InputSigs:          inputSigs,
				GuardianSigs:       f.guardianSigs,
				ScriptPubkeys:      inputSpks,
			},
		},
	}
}

func buildBTCTwoInputFixture(t *testing.T) *btcTwoInputFixture {
	t.Helper()

	withdrawID := "withdraw_multi_input"
	vaultID := uint32(1)

	privKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatalf("new private key failed: %v", err)
	}

	template := &btc.BTCTemplate{
		Version:  2,
		LockTime: 0,
		Inputs: []btc.TxInput{
			{
				TxID:     strings.Repeat("11", 32),
				Vout:     0,
				Amount:   20000,
				Sequence: btc.DefaultSequence,
			},
			{
				TxID:     strings.Repeat("22", 32),
				Vout:     1,
				Amount:   30000,
				Sequence: btc.DefaultSequence,
			},
		},
		Outputs:     exactSingleBTCWithdrawOutputsForTest(t, 50000, 50000, 2, testBTCTaprootAddress, ""),
		VaultID:     vaultID,
		KeyEpoch:    1,
		WithdrawIDs: []string{withdrawID},
	}
	scriptPubKey := btcDualDecorateTemplateKey(t, template, privKey.PubKey().SerializeCompressed())
	templateData, err := template.ToJSON()
	if err != nil {
		t.Fatalf("template json failed: %v", err)
	}

	scriptPubkeys := [][]byte{
		append([]byte(nil), scriptPubKey...),
		append([]byte(nil), scriptPubKey...),
	}

	sighashes, err := template.ComputeTaprootSighash(scriptPubkeys, btc.SighashDefault)
	if err != nil {
		t.Fatalf("compute sighashes failed: %v", err)
	}
	if len(sighashes) != 2 {
		t.Fatalf("unexpected sighash count: got=%d want=2", len(sighashes))
	}

	sig0, err := schnorr.Sign(privKey, sighashes[0])
	if err != nil {
		t.Fatalf("sign input0 failed: %v", err)
	}
	sig1, err := schnorr.Sign(privKey, sighashes[1])
	if err != nil {
		t.Fatalf("sign input1 failed: %v", err)
	}

	base := map[string][]byte{}
	readFn := func(key string) ([]byte, error) {
		v, ok := base[key]
		if !ok {
			return nil, nil
		}
		out := make([]byte, len(v))
		copy(out, v)
		return out, nil
	}
	scanFn := func(prefix string) (map[string][]byte, error) {
		return map[string][]byte{}, nil
	}
	sv := NewStateView(readFn, scanFn)

	withdraw := &pb.FrostWithdrawState{
		WithdrawId: withdrawID,
		TxId:       "tx_withdraw_multi_input",
		Seq:        1,
		Chain:      "btc",
		Asset:      "BTC",
		To:         testBTCTaprootAddress,
		Amount:     "50000",
		Status:     "QUEUED",
		VaultId:    vaultID,
	}
	withdrawData, err := proto.Marshal(withdraw)
	if err != nil {
		t.Fatalf("marshal withdraw failed: %v", err)
	}
	sv.Set(keys.KeyFrostWithdraw(withdrawID), withdrawData)
	sv.Set(keys.KeyFrostWithdrawFIFOHead("btc", "BTC"), []byte("1"))

	vaultState := &pb.FrostVaultState{
		VaultId:          vaultID,
		Chain:            "btc",
		KeyEpoch:         1,
		GroupPubkey:      privKey.PubKey().SerializeCompressed(),
		SignAlgo:         pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		Status:           "ACTIVE",
		CommitteeMembers: []string{"coordinator"},
	}
	vaultStateData, err := proto.Marshal(vaultState)
	if err != nil {
		t.Fatalf("marshal vault state failed: %v", err)
	}
	sv.Set(keys.KeyFrostVaultState("btc", vaultID), vaultStateData)
	for _, input := range template.Inputs {
		utxoData, _ := proto.Marshal(&pb.FrostUtxo{Txid: input.TxID, Vout: input.Vout, Amount: strconv.FormatUint(input.Amount, 10), VaultId: vaultID, ScriptPubkey: scriptPubKey})
		sv.Set(keys.KeyFrostBtcUtxo(vaultID, input.TxID, input.Vout), utxoData)
	}
	jobID := claimBTCWithdrawForTest(t, sv, "BTC", template)

	_, guardianPrivate := btcDualTestKeys()
	guardian0, _ := schnorr.Sign(guardianPrivate, sighashes[0])
	guardian1, _ := schnorr.Sign(guardianPrivate, sighashes[1])
	return &btcTwoInputFixture{
		sv:           sv,
		privKey:      privKey,
		templateData: templateData,
		templateHash: template.TemplateHash(),
		scriptPubKey: scriptPubKey,
		sigForInput: [][]byte{
			sig0.Serialize(),
			sig1.Serialize(),
		},
		guardianSigs: [][]byte{guardian0.Serialize(), guardian1.Serialize()},
		vaultID:      vaultID,
		withdrawID:   withdrawID,
		jobID:        jobID,
	}
}
