package utils

import (
	"bytes"
	"encoding/hex"
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
)

                                                                           
                                                                       
const DualCustodyNUMSX = "50929b74c1a04954b78b4b6035e97a5e078a5a0f28ec96d547bfee9ace803ac0"

                                                                              
                                                                            
func BuildDualCustodyTaproot(operator, guardian []byte) (scriptPubKey, spendScript, controlBlock []byte, err error) {
	op, err := NormalizeSecp256k1XOnlyPubKey(operator)
	if err != nil {
		return nil, nil, nil, err
	}
	guard, err := NormalizeSecp256k1XOnlyPubKey(guardian)
	if err != nil {
		return nil, nil, nil, err
	}
	if _, err = schnorr.ParsePubKey(op); err != nil {
		return nil, nil, nil, fmt.Errorf("invalid operator public key: %w", err)
	}
	if _, err = schnorr.ParsePubKey(guard); err != nil {
		return nil, nil, nil, fmt.Errorf("invalid guardian public key: %w", err)
	}
	if bytes.Equal(op, guard) {
		return nil, nil, nil, fmt.Errorf("operator and guardian public keys must differ")
	}
	spendScript, err = txscript.NewScriptBuilder().AddData(op).AddOp(txscript.OP_CHECKSIGVERIFY).AddData(guard).AddOp(txscript.OP_CHECKSIG).Script()
	if err != nil {
		return nil, nil, nil, err
	}
	nums, _ := hex.DecodeString(DualCustodyNUMSX)
	internal, err := schnorr.ParsePubKey(nums)
	if err != nil {
		return nil, nil, nil, err
	}
	leaf := txscript.NewBaseTapLeaf(spendScript)
	root := leaf.TapHash()
	output := txscript.ComputeTaprootOutputKey(internal, root[:])
	scriptPubKey, err = txscript.PayToTaprootScript(output)
	if err != nil {
		return nil, nil, nil, err
	}
	control := txscript.ControlBlock{InternalKey: internal, OutputKeyYIsOdd: output.SerializeCompressed()[0] == 0x03, LeafVersion: txscript.BaseLeafVersion}
	controlBlock, err = control.ToBytes()
	return scriptPubKey, spendScript, controlBlock, err
}

func BuildTweakedDualCustodyTaprootScriptPubKey(groupPubkey []byte, receiver string, guardian []byte) ([]byte, error) {
	tweak, err := ComputeUserTweakForPubKey(groupPubkey, receiver)
	if err != nil {
		return nil, err
	}
	operator, err := ComputeTweakedXOnlyPubkey(groupPubkey, tweak)
	if err != nil {
		return nil, err
	}
	script, _, _, err := BuildDualCustodyTaproot(operator, guardian)
	return script, err
}

func DualCustodyTaprootAddress(operator, guardian []byte, params *chaincfg.Params) (string, error) {
	script, _, _, err := BuildDualCustodyTaproot(operator, guardian)
	if err != nil {
		return "", err
	}
	return TaprootAddressFromXOnly(script[2:], params)
}
