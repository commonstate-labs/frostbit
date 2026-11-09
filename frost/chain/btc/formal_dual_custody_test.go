package btc

import (
	"bytes"
	"encoding/hex"
	"os"
	"testing"

	"dex/formal/itftrace"
	"dex/utils"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
)

                                                                               
                                                                          
func formalDualCustodyBTCFixture(t *testing.T) (*BTCTemplate, *wire.MsgTx, []byte) {
	t.Helper()
	key := func(n byte) *btcec.PrivateKey {
		raw := make([]byte, 32)
		raw[31] = n
		priv, _ := btcec.PrivKeyFromBytes(raw)
		return priv
	}
	op, guardian := key(23), key(31)
	spk, leaf, control, err := utils.BuildDualCustodyTaproot(
		schnorr.SerializePubKey(op.PubKey()), schnorr.SerializePubKey(guardian.PubKey()))
	if err != nil {
		t.Fatal(err)
	}
	nums, _ := hex.DecodeString("50929b74c1a04954b78b4b6035e97a5e078a5a0f28ec96d547bfee9ace803ac0")
	if len(control) != 33 || !bytes.Equal(control[1:], nums) {
		t.Fatal("dual custody has an alternative internal key or a non-single-leaf proof")
	}
	to, err := utils.TaprootAddressFromPubKey(key(43).PubKey().SerializeCompressed(), &chaincfg.RegressionNetParams)
	if err != nil {
		t.Fatal(err)
	}
	template := &BTCTemplate{Version: 2, VaultID: 0, KeyEpoch: 1,
		Inputs: []TxInput{{TxID: "0101010101010101010101010101010101010101010101010101010101010101", Vout: 0,
			Amount: 100000, Sequence: DefaultSequence, SpendScript: leaf, ControlBlock: control}},
		Outputs: []TxOutput{{Address: to, Amount: 99000}}, WithdrawIDs: []string{"formal-dual-btc"}}
	messages, err := template.ComputeTaprootSighash([][]byte{spk}, SighashDefault)
	if err != nil {
		t.Fatal(err)
	}
	opSig, err := schnorr.Sign(op, messages[0])
	if err != nil {
		t.Fatal(err)
	}
	guardSig, err := schnorr.Sign(guardian, messages[0])
	if err != nil {
		t.Fatal(err)
	}
	raw, err := template.BuildSignedRawTx([][]byte{opSig.Serialize()}, [][]byte{guardSig.Serialize()})
	if err != nil {
		t.Fatal(err)
	}
	var tx wire.MsgTx
	if err := tx.Deserialize(bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	return template, &tx, spk
}

func formalDualCustodyBTCExec(template *BTCTemplate, tx *wire.MsgTx, spk []byte) error {
	prev := txscript.NewMultiPrevOutFetcher(nil)
	prev.AddPrevOut(tx.TxIn[0].PreviousOutPoint, wire.NewTxOut(int64(template.Inputs[0].Amount), spk))
	hashes := txscript.NewTxSigHashes(tx, prev)
	engine, err := txscript.NewEngine(spk, tx, 0, txscript.StandardVerifyFlags, nil, hashes,
		int64(template.Inputs[0].Amount), prev)
	if err != nil {
		return err
	}
	return engine.Execute()
}

func TestFormalDualCustodyBTCScriptAuthorization(t *testing.T) {
	template, baseline, spk := formalDualCustodyBTCFixture(t)
	cases := []struct {
		name string
		edit func(*wire.MsgTx)
		want bool
	}{
		{"both", func(*wire.MsgTx) {}, true},
		{"operator-only", func(tx *wire.MsgTx) { tx.TxIn[0].Witness[0] = nil }, false},
		{"guardian-only", func(tx *wire.MsgTx) { tx.TxIn[0].Witness[1] = nil }, false},
		{"neither", func(tx *wire.MsgTx) { tx.TxIn[0].Witness[0], tx.TxIn[0].Witness[1] = nil, nil }, false},
		{"economic-output", func(tx *wire.MsgTx) { tx.TxOut[0].Value-- }, false},
		{"different-input", func(tx *wire.MsgTx) { tx.TxIn[0].PreviousOutPoint.Index++ }, false},
		{"guardian-bit-flip", func(tx *wire.MsgTx) { tx.TxIn[0].Witness[0][0] ^= 1 }, false},
		{"control-block", func(tx *wire.MsgTx) { tx.TxIn[0].Witness[3][1] ^= 1 }, false},
		{"swap-roles", func(tx *wire.MsgTx) {
			tx.TxIn[0].Witness[0], tx.TxIn[0].Witness[1] = tx.TxIn[0].Witness[1], tx.TxIn[0].Witness[0]
		}, false},
		{"operator-key-path", func(tx *wire.MsgTx) { tx.TxIn[0].Witness = wire.TxWitness{tx.TxIn[0].Witness[1]} }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx := baseline.Copy()
			tc.edit(tx)
			if got := formalDualCustodyBTCExec(template, tx, spk) == nil; got != tc.want {
				t.Fatalf("Bitcoin Script acceptance=%v, abstract authorization=%v", got, tc.want)
			}
		})
	}
}

func TestFormalDualCustodyBTCScriptITFDifferential(t *testing.T) {
	path := os.Getenv("DEX_FORMAL_ITF_DUAL_CUSTODY")
	if path == "" {
		t.Skip("formal/formal_dual_custody.ps1 supplies the Quint trace")
	}
	trace, err := itftrace.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(trace.States) != 10 {
		t.Fatalf("trace states=%d, want 10", len(trace.States))
	}
	template, baseline, spk := formalDualCustodyBTCFixture(t)
	checked := 0
	for _, state := range trace.States[1:] {
		kind, err := state.Int("kind")
		if err != nil {
			t.Fatal(err)
		}
		if kind != 0 {
			continue
		}
		op, err := state.Bool("operator")
		if err != nil {
			t.Fatal(err)
		}
		guard, err := state.Bool("guardian")
		if err != nil {
			t.Fatal(err)
		}
		context, err := state.Int("context")
		if err != nil {
			t.Fatal(err)
		}
		keyPath, err := state.Bool("operatorKeyPath")
		if err != nil {
			t.Fatal(err)
		}
		accepted, err := state.Bool("accepted")
		if err != nil {
			t.Fatal(err)
		}
		tx := baseline.Copy()
		if !op {
			tx.TxIn[0].Witness[1] = nil
		}
		if !guard {
			tx.TxIn[0].Witness[0] = nil
		}
		if context != 0 {
			tx.TxOut[0].Value--
		}
		if keyPath {
			tx.TxIn[0].Witness = wire.TxWitness{tx.TxIn[0].Witness[1]}
		}
		if got := formalDualCustodyBTCExec(template, tx, spk) == nil; got != accepted {
			t.Fatalf("edge %d: Bitcoin Script acceptance=%v, Quint=%v", checked, got, accepted)
		}
		checked++
	}
	if checked != 5 {
		t.Fatalf("compared %d BTC cuts, want 5", checked)
	}
}
