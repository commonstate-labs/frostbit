package btc

import (
	"crypto/sha256"
	"fmt"

	"dex/frost/chain"
	"dex/utils"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
)

func testGuardianKey() *btcec.PrivateKey {
	key, _ := btcec.PrivKeyFromBytes([]byte{7})
	return key
}

func testInputKey(txid string, vout uint32) *btcec.PrivateKey {
	h := sha256.Sum256([]byte(fmt.Sprintf("BTC dual custody unit fixture:%s:%d", txid, vout)))
	key, _ := btcec.PrivKeyFromBytes(h[:])
	return key
}

func testDualParams(params chain.WithdrawTemplateParams) chain.WithdrawTemplateParams {
	params.Inputs = append([]chain.UTXO(nil), params.Inputs...)
	params.GuardianPubkey = schnorr.SerializePubKey(testGuardianKey().PubKey())
	params.BTCInputPubkeys = make([][]byte, len(params.Inputs))
	for i := range params.Inputs {
		u := &params.Inputs[i]
		params.BTCInputPubkeys[i] = schnorr.SerializePubKey(testInputKey(u.TxID, u.Vout).PubKey())
		u.ScriptPubKey, _, _, _ = utils.BuildDualCustodyTaproot(params.BTCInputPubkeys[i], params.GuardianPubkey)
	}
	return params
}

func newTestBTCTemplate(params chain.WithdrawTemplateParams) (*BTCTemplate, error) {
	return NewBTCTemplate(testDualParams(params))
}

func decorateTestTemplate(template *BTCTemplate) {
	for i := range template.Inputs {
		in := &template.Inputs[i]
		_, in.SpendScript, in.ControlBlock, _ = utils.BuildDualCustodyTaproot(schnorr.SerializePubKey(testInputKey(in.TxID, in.Vout).PubKey()), schnorr.SerializePubKey(testGuardianKey().PubKey()))
	}
}

func testTemplatePrevScripts(template *BTCTemplate) [][]byte {
	out := make([][]byte, len(template.Inputs))
	for i, in := range template.Inputs {
		out[i], _, _, _ = utils.BuildDualCustodyTaproot(in.SpendScript[1:33], in.SpendScript[35:67])
	}
	return out
}
