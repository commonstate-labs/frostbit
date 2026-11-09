package btc

import (
	"bytes"
	"crypto/sha256"
	"strings"
	"testing"

	"dex/frost/chain"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/chaincfg"
)

func TestBTCAdapterVerifySignatureAcceptsCompressedPubkey(t *testing.T) {
	adapter := NewBTCAdapter("regtest")

	privKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatalf("new private key failed: %v", err)
	}

	msg := sha256.Sum256([]byte("adapter verify compressed key"))
	sig, err := schnorr.Sign(privKey, msg[:])
	if err != nil {
		t.Fatalf("schnorr sign failed: %v", err)
	}

	compressed := privKey.PubKey().SerializeCompressed()                          
	valid, err := adapter.VerifySignature(compressed, msg[:], sig.Serialize())
	if err != nil {
		t.Fatalf("VerifySignature returned error: %v", err)
	}
	if !valid {
		t.Fatal("expected signature to be valid")
	}
}

func TestBTCAdapterVerifySignatureRejectsInvalidCompressedPrefix(t *testing.T) {
	adapter := NewBTCAdapter("regtest")

	msg := make([]byte, 32)
	sig := make([]byte, 64)
	pub := make([]byte, 33)
	pub[0] = 0x04

	_, err := adapter.VerifySignature(pub, msg, sig)
	if err == nil {
		t.Fatal("expected error for invalid compressed pubkey prefix")
	}
	if !strings.Contains(err.Error(), "prefix") {
		t.Fatalf("expected prefix-related error, got: %v", err)
	}
}

func TestBTCAdapterBuildTemplateAlignsScriptPubKeysAfterInputSort(t *testing.T) {
	adapter := NewBTCAdapter("regtest")
	scriptA := append([]byte{0x51, 0x20}, bytes.Repeat([]byte{0xaa}, 32)...)
	scriptB := append([]byte{0x51, 0x20}, bytes.Repeat([]byte{0xbb}, 32)...)
	params := chain.WithdrawTemplateParams{
		Chain:       "btc",
		Asset:       "BTC",
		VaultID:     1,
		KeyEpoch:    1,
		WithdrawIDs: []string{"wd"},
		Outputs: []chain.WithdrawOutput{{
			WithdrawID: "wd",
			To:         mustTaprootAddress(t, &chaincfg.RegressionNetParams).EncodeAddress(),
			Amount:     "1000",
		}},
		Inputs: []chain.UTXO{
			{TxID: strings.Repeat("bb", 32), Vout: 0, Amount: 1000, ScriptPubKey: scriptB},
			{TxID: strings.Repeat("aa", 32), Vout: 0, Amount: 1000, ScriptPubKey: scriptA},
		},
		Fee:          500,
		ChangeAmount: 500,
	}

	params = testDualParams(params)
	scriptB, scriptA = params.Inputs[0].ScriptPubKey, params.Inputs[1].ScriptPubKey
	result, err := adapter.BuildWithdrawTemplate(params)
	if err != nil {
		t.Fatalf("BuildWithdrawTemplate: %v", err)
	}
	template, err := FromJSON(result.TemplateData)
	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}
	want, err := template.ComputeTaprootSighash([][]byte{scriptA, scriptB}, SighashDefault)
	if err != nil {
		t.Fatalf("expected sighash: %v", err)
	}
	if _, err := template.ComputeTaprootSighash([][]byte{scriptB, scriptA}, SighashDefault); err == nil {
		t.Fatal("unsorted prevouts must fail the policy commitment check")
	}

	if len(result.SigHashes) != 2 || !bytes.Equal(result.SigHashes[0], want[0]) || !bytes.Equal(result.SigHashes[1], want[1]) {
		t.Fatalf("sighashes not aligned with sorted inputs")
	}
}
