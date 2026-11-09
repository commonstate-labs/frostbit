                                               
                                                                                     

package workers

import (
	"encoding/hex"
	"fmt"
	"strings"

	"dex/frost/chain/btc"
	"dex/frost/core/frost"
	"dex/frost/runtime/planning"
	"dex/keys"
	"dex/pb"
	"dex/utils"

	"google.golang.org/protobuf/proto"
)

func isBTCChain(chainName string) bool {
	c := strings.ToLower(strings.TrimSpace(chainName))
	return c == "btc" || strings.HasPrefix(c, "btc_") || strings.HasPrefix(c, "btc-")
}

func (w *WithdrawWorker) parseBTCTemplate(job *planning.Job) (*btc.BTCTemplate, error) {
	if job == nil {
		return nil, fmt.Errorf("job is nil")
	}
	if len(job.TemplateData) == 0 {
		return nil, fmt.Errorf("missing template_data for BTC job %s", job.JobID)
	}
	template, err := btc.FromJSON(job.TemplateData)
	if err != nil {
		return nil, fmt.Errorf("decode BTC template_data: %w", err)
	}
	if len(template.Inputs) == 0 {
		return nil, fmt.Errorf("BTC template has no inputs for job %s", job.JobID)
	}
	return template, nil
}

func (w *WithdrawWorker) loadBTCScriptPubkeys(chainName string, vaultID uint32, template *btc.BTCTemplate) ([][]byte, error) {
	if template == nil {
		return nil, fmt.Errorf("template is nil")
	}
	stateKeys := make([]string, len(template.Inputs))
	for i, in := range template.Inputs {
		stateKeys[i] = keys.KeyFrostBtcUtxo(vaultID, in.TxID, in.Vout)
	}
	state, err := readBTCWorkerState(w.stateReader, stateKeys)
	if err != nil {
		return nil, err
	}
	scripts := make([][]byte, len(template.Inputs))
	for i, k := range stateKeys {
		var u pb.FrostUtxo
		if len(state[k]) == 0 || proto.Unmarshal(state[k], &u) != nil || len(u.ScriptPubkey) == 0 {
			return nil, fmt.Errorf("BTC input script unavailable")
		}
		scripts[i] = append([]byte(nil), u.ScriptPubkey...)
	}
	return scripts, nil
}

func (w *WithdrawWorker) loadUTXOScriptPubkey(vaultID uint32, txid string, vout uint32) ([]byte, error) {
	utxoKey := keys.KeyFrostBtcUtxo(vaultID, txid, vout)
	utxoData, exists, err := w.stateReader.Get(utxoKey)
	if err != nil {
		return nil, fmt.Errorf("read BTC utxo %s failed: %w", utxoKey, err)
	}
	if !exists || len(utxoData) == 0 {
		return nil, nil
	}

	var utxo pb.FrostUtxo
	if err := proto.Unmarshal(utxoData, &utxo); err != nil {
		return nil, fmt.Errorf("decode BTC utxo %s failed: %w", utxoKey, err)
	}
	if len(utxo.ScriptPubkey) == 0 {
		return nil, nil
	}
	return append([]byte(nil), utxo.ScriptPubkey...), nil
}

                                                                         
func (w *WithdrawWorker) loadBTCInputTweaks(job *planning.Job) [][]byte {
	template, err := w.parseBTCTemplate(job)
	if err != nil {
		return nil
	}
	stateKeys := make([]string, len(template.Inputs))
	for i, in := range template.Inputs {
		stateKeys[i] = keys.KeyFrostBtcUtxo(job.VaultID, in.TxID, in.Vout)
	}
	state, err := readBTCWorkerState(w.stateReader, stateKeys)
	if err != nil {
		return nil
	}
	tweaks := make([][]byte, len(template.Inputs))
	for i, k := range stateKeys {
		var u pb.FrostUtxo
		if len(state[k]) == 0 || proto.Unmarshal(state[k], &u) != nil {
			return nil
		}
		tweaks[i] = append([]byte(nil), u.Tweak...)
	}
	return tweaks
}

func (w *WithdrawWorker) loadVaultTaprootScriptPubkey(chainName string, vaultID uint32) ([]byte, error) {
	vaultKey := keys.KeyFrostVaultState(chainName, vaultID)
	vaultData, exists, err := w.stateReader.Get(vaultKey)
	if err != nil {
		return nil, fmt.Errorf("read vault state %s failed: %w", vaultKey, err)
	}
	if !exists || len(vaultData) == 0 {
		return nil, fmt.Errorf("vault state missing: key=%s", vaultKey)
	}

	var vault pb.FrostVaultState
	if err := proto.Unmarshal(vaultData, &vault); err != nil {
		return nil, fmt.Errorf("decode vault state %s failed: %w", vaultKey, err)
	}

	xOnly, err := utils.NormalizeSecp256k1XOnlyPubKey(vault.GroupPubkey)
	if err != nil {
		return nil, err
	}

	cfgData, found, err := w.stateReader.Get(keys.KeyFrostVaultConfig(chainName, 0))
	if err != nil || !found {
		return nil, fmt.Errorf("BTC guardian configuration unavailable")
	}
	var cfg pb.FrostVaultConfig
	if proto.Unmarshal(cfgData, &cfg) != nil {
		return nil, fmt.Errorf("invalid BTC guardian configuration")
	}
	script, _, _, err := utils.BuildDualCustodyTaproot(xOnly, cfg.GuardianPubkey)
	return script, err
}

func (w *WithdrawWorker) resolveBTCInputSignatures(signedPkg *SignedPackage, inputCount int) ([][]byte, error) {
	if signedPkg == nil {
		return nil, fmt.Errorf("signed package is nil")
	}
	if inputCount <= 0 {
		return nil, fmt.Errorf("invalid BTC input count: %d", inputCount)
	}

	if len(signedPkg.Signatures) > 0 {
		if len(signedPkg.Signatures) != inputCount {
			return nil, fmt.Errorf("btc signature count mismatch: got=%d want=%d", len(signedPkg.Signatures), inputCount)
		}
		result := make([][]byte, inputCount)
		for i, sig := range signedPkg.Signatures {
			if len(sig) != 64 {
				return nil, fmt.Errorf("invalid btc signature length for input %d: %d", i, len(sig))
			}
			result[i] = append([]byte(nil), sig...)
		}
		return result, nil
	}

	if len(signedPkg.Signature) == 0 {
		return nil, fmt.Errorf("missing BTC signatures")
	}

	if inputCount == 1 {
		if len(signedPkg.Signature) != 64 {
			return nil, fmt.Errorf("invalid btc signature length: %d", len(signedPkg.Signature))
		}
		return [][]byte{append([]byte(nil), signedPkg.Signature...)}, nil
	}

	if len(signedPkg.Signature) != inputCount*64 {
		return nil, fmt.Errorf("invalid combined BTC signature length: got=%d want=%d", len(signedPkg.Signature), inputCount*64)
	}

	result := make([][]byte, inputCount)
	for i := 0; i < inputCount; i++ {
		start := i * 64
		result[i] = append([]byte(nil), signedPkg.Signature[start:start+64]...)
	}
	return result, nil
}

func isTaprootScriptPubKey(scriptPubkey []byte) bool {
	return len(scriptPubkey) == 34 && scriptPubkey[0] == 0x51 && scriptPubkey[1] == 0x20
}

func shortHexPanic(data []byte, n int) string {
	if len(data) == 0 {
		return ""
	}
	if n <= 0 || len(data) <= n {
		return hex.EncodeToString(data)
	}
	return hex.EncodeToString(data[:n])
}

                                                                                                               
func (w *WithdrawWorker) validateBTCSignaturePreSubmit(job *planning.Job, template *btc.BTCTemplate, scriptPubkeys, inputSigs, inputTweaks, guardianSigs [][]byte) error {
	if job == nil || template == nil || len(scriptPubkeys) != len(template.Inputs) || len(inputSigs) != len(template.Inputs) || len(guardianSigs) != len(template.Inputs) || len(inputTweaks) != len(template.Inputs) {
		return fmt.Errorf("BTC dual custody signature/input count mismatch")
	}
	group, err := w.vaultProvider.VaultGroupPubkey(job.Chain, job.VaultID, job.KeyEpoch)
	if err != nil {
		return err
	}
	cfgData, found, err := w.stateReader.Get(keys.KeyFrostVaultConfig(job.Chain, 0))
	if err != nil || !found {
		return fmt.Errorf("BTC guardian configuration unavailable")
	}
	var cfg pb.FrostVaultConfig
	if proto.Unmarshal(cfgData, &cfg) != nil || len(cfg.GuardianPubkey) != 32 {
		return fmt.Errorf("invalid BTC guardian configuration")
	}
	hashes, err := template.ComputeTaprootSighash(scriptPubkeys, btc.SighashDefault)
	if err != nil {
		return err
	}
	for i := range template.Inputs {
		operator, err := utils.NormalizeSecp256k1XOnlyPubKey(group)
		if len(inputTweaks[i]) == 32 {
			operator, err = utils.ComputeTweakedXOnlyPubkey(group, inputTweaks[i])
		} else if len(inputTweaks[i]) != 0 {
			return fmt.Errorf("invalid BTC input tweak")
		}
		if err != nil {
			return err
		}
		if err := template.ValidateInputPolicy(i, operator, cfg.GuardianPubkey, scriptPubkeys[i]); err != nil {
			return err
		}
		for _, side := range []struct{ key, sig []byte }{{operator, inputSigs[i]}, {cfg.GuardianPubkey, guardianSigs[i]}} {
			ok, e, p := verifySignatureWithRecover(pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340, side.key, hashes[i], side.sig)
			if e != nil || p != nil || !ok {
				return fmt.Errorf("BTC dual custody signature verification failed for input %d", i)
			}
		}
	}
	return nil
}

func readBTCWorkerState(reader StateReader, stateKeys []string) (map[string][]byte, error) {
	if bulk, ok := reader.(interface {
		GetMany([]string) (map[string][]byte, error)
	}); ok {
		return bulk.GetMany(stateKeys)
	}
	out := make(map[string][]byte, len(stateKeys))
	for _, key := range stateKeys {
		v, found, err := reader.Get(key)
		if err != nil {
			return nil, err
		}
		if found {
			out[key] = v
		}
	}
	return out, nil
}

func inputTweaksForLog(inputTweaks [][]byte, index int) []byte {
	if index < 0 || index >= len(inputTweaks) {
		return nil
	}
	return inputTweaks[index]
}

func verifySignatureWithRecover(signAlgo pb.SignAlgo, pubkey, msg, sig []byte) (valid bool, err error, panicVal any) {
	defer func() {
		if r := recover(); r != nil {
			panicVal = r
		}
	}()
	valid, err = frost.Verify(signAlgo, pubkey, msg, sig)
	return valid, err, nil
}
