package btc

import (
	"bytes"
	"testing"

	"dex/frost/chain"
	"dex/utils"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
)

func TestBTCTemplateRequiresIndependentDualCustodyKeys(t *testing.T) {
	params := testDualParams(chain.WithdrawTemplateParams{
		Inputs:      []chain.UTXO{{TxID: "1111111111111111111111111111111111111111111111111111111111111111", Amount: 100000}},
		Outputs:     []chain.WithdrawOutput{{WithdrawID: "one", To: "tb1pdpd76eytdcdwnm62uuxve403srq0dtcls0uj488wjjy4z4lfm25sdwatf0", Amount: "99000"}},
		WithdrawIDs: []string{"one"}, Fee: 1000,
	})
	for _, tc := range []struct {
		name   string
		mutate func(*chain.WithdrawTemplateParams)
	}{
		{"missing-guardian", func(p *chain.WithdrawTemplateParams) { p.GuardianPubkey = nil }},
		{"missing-operator", func(p *chain.WithdrawTemplateParams) { p.BTCInputPubkeys = nil }},
		{"same-key", func(p *chain.WithdrawTemplateParams) { p.BTCInputPubkeys = [][]byte{p.GuardianPubkey} }},
		{"legacy-keypath", func(p *chain.WithdrawTemplateParams) {
			p.Inputs[0].ScriptPubKey, _ = utils.BuildTaprootScriptPubKeyFromXOnly(p.BTCInputPubkeys[0])
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := params
			candidate.Inputs = append([]chain.UTXO(nil), params.Inputs...)
			tc.mutate(&candidate)
			if _, err := NewBTCTemplate(candidate); err == nil {
				t.Fatal("invalid custody policy was accepted")
			}
		})
	}
	guardian := schnorr.SerializePubKey(testGuardianKey().PubKey())
	if _, _, _, err := utils.BuildDualCustodyTaproot(append([]byte{3}, guardian...), guardian); err == nil {
		t.Fatal("opposite compressed parity must not bypass independent-key requirement")
	}
}

func TestBTCTemplateHashCommitsSpendPolicy(t *testing.T) {
	template := &BTCTemplate{Version: 2, Inputs: []TxInput{{TxID: "1111111111111111111111111111111111111111111111111111111111111111", Amount: 100000, Sequence: DefaultSequence}}}
	decorateTestTemplate(template)
	original := template.TemplateHash()
	template.Inputs[0].SpendScript[1] ^= 1
	if bytes.Equal(original, template.TemplateHash()) {
		t.Fatal("template commitment omitted spend script")
	}
	template.Inputs[0].SpendScript[1] ^= 1
	template.Inputs[0].ControlBlock[1] ^= 1
	if bytes.Equal(original, template.TemplateHash()) {
		t.Fatal("template commitment omitted control block")
	}
}
