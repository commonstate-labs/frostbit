package vm

import (
	"testing"

	"dex/frost/chain/btc"
	"dex/pb"
	"dex/utils"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
)

func btcDualTestKeys() (*btcec.PrivateKey, *btcec.PrivateKey) {
	op, _ := btcec.PrivKeyFromBytes([]byte{23})
	guard, _ := btcec.PrivKeyFromBytes([]byte{31})
	return op, guard
}

func btcDualTestGuardianPubkey() []byte {
	_, guard := btcDualTestKeys()
	return schnorr.SerializePubKey(guard.PubKey())
}

func btcDualTestOperatorPubkey() []byte {
	op, _ := btcDualTestKeys()
	return schnorr.SerializePubKey(op.PubKey())
}

func btcDualTestScript(t *testing.T) []byte {
	t.Helper()
	script, _, _, err := utils.BuildDualCustodyTaproot(btcDualTestOperatorPubkey(), btcDualTestGuardianPubkey())
	if err != nil {
		t.Fatal(err)
	}
	return script
}

func btcDualDecorateTemplate(t *testing.T, template *btc.BTCTemplate) {
	t.Helper()
	for i := range template.Inputs {
		_, leaf, control, err := utils.BuildDualCustodyTaproot(btcDualTestOperatorPubkey(), btcDualTestGuardianPubkey())
		if err != nil {
			t.Fatal(err)
		}
		template.Inputs[i].SpendScript, template.Inputs[i].ControlBlock = leaf, control
	}
}

                                                                              
func btcDualSignedFixture(t *testing.T) (StateView, *pb.FrostWithdrawSignedTx) {
	t.Helper()
	f := newBTCWithdrawValidationFixture(t)
	claimTx := f.claimTx(t, f.template)
	claim := claimTx.GetFrostWithdrawClaimTx()
	_, receipt, err := (&FrostWithdrawClaimTxHandler{}).DryRun(claimTx, f.sv)
	if err != nil || receipt == nil || receipt.Status != "SUCCEED" {
		t.Fatalf("dual custody fixture Claim: %v %+v", err, receipt)
	}
	hashes, err := f.template.ComputeTaprootSighash([][]byte{f.scriptPubKey}, btc.SighashDefault)
	if err != nil {
		t.Fatal(err)
	}
	op, guard := btcDualTestKeys()
	opSigs, guardSigs := make([][]byte, len(hashes)), make([][]byte, len(hashes))
	var combined []byte
	for i, h := range hashes {
		a, e := schnorr.Sign(op, h)
		if e != nil {
			t.Fatal(e)
		}
		b, e := schnorr.Sign(guard, h)
		if e != nil {
			t.Fatal(e)
		}
		opSigs[i], guardSigs[i] = a.Serialize(), b.Serialize()
		combined = append(combined, opSigs[i]...)
	}
	data, err := f.template.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	return f.sv, &pb.FrostWithdrawSignedTx{Base: &pb.BaseMessage{TxId: "dual-real-signed", FromAddress: claim.Base.FromAddress, ExecutedHeight: 201}, JobId: claim.JobId, Chain: "btc", Asset: f.asset, VaultId: f.vaultID, KeyEpoch: f.keyEpoch, WithdrawIds: []string{f.withdrawID}, TemplateHash: f.template.TemplateHash(), TemplateData: data, InputSigs: opSigs, GuardianSigs: guardSigs, ScriptPubkeys: [][]byte{f.scriptPubKey}, SignedPackageBytes: combined}
}

func btcDualDecorateTemplateKey(t *testing.T, template *btc.BTCTemplate, operator []byte) []byte {
	t.Helper()
	script, leaf, control, err := utils.BuildDualCustodyTaproot(operator, btcDualTestGuardianPubkey())
	if err != nil {
		t.Fatal(err)
	}
	for i := range template.Inputs {
		template.Inputs[i].SpendScript, template.Inputs[i].ControlBlock = leaf, control
	}
	return script
}
