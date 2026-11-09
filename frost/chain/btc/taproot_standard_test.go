package btc

import (
	"bytes"
	"dex/utils"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
)

func TestTaprootSighashAndSignedRawTxPassStandardEngine(t *testing.T) {
	t.Parallel()

	inputPrivKeys := make([]*btcec.PrivateKey, 2)
	inputScripts := make([][]byte, 2)
	for i := range inputPrivKeys {
		privKey, err := btcec.NewPrivateKey()
		if err != nil {
			t.Fatalf("new input[%d] private key: %v", i, err)
		}
		inputPrivKeys[i] = privKey
		inputScripts[i], _, _, err = utils.BuildDualCustodyTaproot(schnorr.SerializePubKey(privKey.PubKey()), schnorr.SerializePubKey(testGuardianKey().PubKey()))
		if err != nil {
			t.Fatal(err)
		}
	}

	destinationPrivKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatalf("new destination private key: %v", err)
	}
	changePrivKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatalf("new change private key: %v", err)
	}

	template := &BTCTemplate{
		Version:  2,
		LockTime: 42,
		Inputs: []TxInput{
			{TxID: txid1, Vout: 3, Amount: 80_000, Sequence: 0},
			{TxID: txid2, Vout: 7, Amount: 70_000, Sequence: DefaultSequence},
		},
		Outputs: []TxOutput{
			{Address: taprootAddressForPubKey(t, destinationPrivKey.PubKey()), Amount: 100_000},
			{Address: taprootAddressForPubKey(t, changePrivKey.PubKey()), Amount: 49_000},
		},
		VaultID:     9,
		KeyEpoch:    4,
		WithdrawIDs: []string{"withdraw-standard-engine"},
	}

	for i := range template.Inputs {
		_, template.Inputs[i].SpendScript, template.Inputs[i].ControlBlock, err = utils.BuildDualCustodyTaproot(schnorr.SerializePubKey(inputPrivKeys[i].PubKey()), schnorr.SerializePubKey(testGuardianKey().PubKey()))
		if err != nil {
			t.Fatal(err)
		}
	}
	messages, err := template.ComputeTaprootSighash(inputScripts, SighashDefault)
	if err != nil {
		t.Fatalf("compute taproot sighashes: %v", err)
	}
	if len(messages) != len(inputPrivKeys) {
		t.Fatalf("sighash count=%d want=%d", len(messages), len(inputPrivKeys))
	}

	signatures := make([][]byte, len(messages))
	guardianSigs := make([][]byte, len(messages))
	for i, message := range messages {
		sig, err := schnorr.Sign(inputPrivKeys[i], message)
		if err != nil {
			t.Fatalf("sign input[%d]: %v", i, err)
		}
		signatures[i] = sig.Serialize()
		guard, err := schnorr.Sign(testGuardianKey(), message)
		if err != nil {
			t.Fatal(err)
		}
		guardianSigs[i] = guard.Serialize()
	}

	rawTx, err := template.BuildSignedRawTx(signatures, guardianSigs)
	if err != nil {
		t.Fatalf("build signed raw transaction: %v", err)
	}
	var signedTx wire.MsgTx
	if err := signedTx.Deserialize(bytes.NewReader(rawTx)); err != nil {
		t.Fatalf("deserialize signed raw transaction: %v", err)
	}
	if got := signedTx.TxIn[0].Sequence; got != 0 {
		t.Fatalf("input[0] sequence=%d want=0", got)
	}

	prevOutFetcher := txscript.NewMultiPrevOutFetcher(nil)
	for i, txIn := range signedTx.TxIn {
		prevOutFetcher.AddPrevOut(
			txIn.PreviousOutPoint,
			wire.NewTxOut(int64(template.Inputs[i].Amount), inputScripts[i]),
		)
	}
	sigHashes := txscript.NewTxSigHashes(&signedTx, prevOutFetcher)

	for i := range signedTx.TxIn {
		standardMessage, err := txscript.CalcTapscriptSignaturehash(
			sigHashes,
			txscript.SigHashDefault,
			&signedTx,
			i,
			prevOutFetcher,
			txscript.NewBaseTapLeaf(template.Inputs[i].SpendScript),
		)
		if err != nil {
			t.Fatalf("standard sighash input[%d]: %v", i, err)
		}
		if !bytes.Equal(messages[i], standardMessage) {
			t.Fatalf("input[%d] signing message differs from serialized transaction: got=%x want=%x", i, messages[i], standardMessage)
		}

		engine, err := txscript.NewEngine(
			inputScripts[i],
			&signedTx,
			i,
			txscript.StandardVerifyFlags,
			nil,
			sigHashes,
			int64(template.Inputs[i].Amount),
			prevOutFetcher,
		)
		if err != nil {
			t.Fatalf("create standard script engine for input[%d]: %v", i, err)
		}
		if err := engine.Execute(); err != nil {
			t.Fatalf("standard script verification failed for input[%d]: %v", i, err)
		}
	}
}

func TestComputeTaprootSighashRejectsNonTaprootPrevOut(t *testing.T) {
	t.Parallel()

	template := &BTCTemplate{
		Version: 2,
		Inputs: []TxInput{{
			TxID: txid1, Amount: 1_000, Sequence: DefaultSequence,
		}},
		Outputs: []TxOutput{{
			Address: testTaprootOutputAddress, Amount: 900,
		}},
	}

	p2wpkhScript := append([]byte{0x00, 0x14}, bytes.Repeat([]byte{0x11}, 20)...)
	if _, err := template.ComputeTaprootSighash([][]byte{p2wpkhScript}, SighashDefault); err == nil {
		t.Fatal("non-Taproot prevout script was accepted")
	}
}

func TestComputeTaprootSighashRejectsDuplicateOutpoint(t *testing.T) {
	t.Parallel()

	template := &BTCTemplate{
		Version: 2,
		Inputs: []TxInput{
			{TxID: txid1, Vout: 1, Amount: 1_000, Sequence: DefaultSequence},
			{TxID: txid1, Vout: 1, Amount: 1_000, Sequence: DefaultSequence},
		},
		Outputs: []TxOutput{{
			Address: testTaprootOutputAddress, Amount: 1_900,
		}},
	}
	script := testTaprootPrevOutScript(0x11)
	if _, err := template.ComputeTaprootSighash([][]byte{script, script}, SighashDefault); err == nil {
		t.Fatal("duplicate input outpoint was accepted")
	}
}

func taprootAddressForPubKey(t *testing.T, pubKey *btcec.PublicKey) string {
	t.Helper()

	address, err := btcutil.NewAddressTaproot(
		schnorr.SerializePubKey(pubKey),
		&chaincfg.RegressionNetParams,
	)
	if err != nil {
		t.Fatalf("create taproot address: %v", err)
	}
	return address.EncodeAddress()
}

func taprootScriptForPubKey(t *testing.T, pubKey *btcec.PublicKey) []byte {
	t.Helper()

	script, err := AddressScriptForNetwork(
		taprootAddressForPubKey(t, pubKey),
		"regtest",
	)
	if err != nil {
		t.Fatalf("create taproot script: %v", err)
	}
	return script
}
