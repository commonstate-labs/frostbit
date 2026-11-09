package vm

import (
	"bytes"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/keys"
	"dex/pb"
	"dex/utils"

	"google.golang.org/protobuf/proto"
)

type validatedBTCWithdraw struct {
	template       *btc.BTCTemplate
	canonicalData  []byte
	vault          *pb.FrostVaultState
	utxos          []*pb.FrostUtxo
	scriptPubKeys  [][]byte
	totalInput     uint64
	guardianPubkey []byte
}

func addUint64(total, value uint64) (uint64, error) {
	if math.MaxUint64-total < value {
		return 0, fmt.Errorf("uint64 amount overflow")
	}
	return total + value, nil
}

func validateBTCLocalInputs(sv StateView, template *btc.BTCTemplate, vaultID uint32, keyEpoch uint64, requiredLockOwner string) (*pb.FrostVaultState, []chain.UTXO, []*pb.FrostUtxo, [][]byte, uint64, error) {
	vaultData, exists, err := sv.Get(keys.KeyFrostVaultState(chain.ChainBTC, vaultID))
	if err != nil || !exists || len(vaultData) == 0 {
		return nil, nil, nil, nil, 0, fmt.Errorf("BTC vault state not found")
	}
	var vault pb.FrostVaultState
	if err := unmarshalProtoCompat(vaultData, &vault); err != nil {
		return nil, nil, nil, nil, 0, fmt.Errorf("invalid BTC vault state: %w", err)
	}
	if vault.KeyEpoch != keyEpoch || len(vault.GroupPubkey) == 0 || len(vault.CommitteeMembers) == 0 {
		return nil, nil, nil, nil, 0, fmt.Errorf("BTC vault epoch/key/committee mismatch")
	}

	cfg, err := loadFrostVaultConfig(sv, chain.ChainBTC)
	if err != nil || len(cfg.GuardianPubkey) != 32 {
		return nil, nil, nil, nil, 0, fmt.Errorf("BTC guardian configuration is missing")
	}
	readKeys := make([]string, 0, len(template.Inputs)*2)
	for _, input := range template.Inputs {
		readKeys = append(readKeys, keys.KeyFrostBtcUtxo(vaultID, input.TxID, input.Vout), keys.KeyFrostBtcLockedUtxo(vaultID, input.TxID, input.Vout))
	}
	state, err := readBTCStateMany(sv, readKeys)
	if err != nil {
		return nil, nil, nil, nil, 0, err
	}
	inputs := make([]chain.UTXO, len(template.Inputs))
	utxos := make([]*pb.FrostUtxo, len(template.Inputs))
	scriptPubKeys := make([][]byte, len(template.Inputs))
	seen := make(map[string]struct{}, len(template.Inputs))
	var totalInput uint64
	for i, input := range template.Inputs {
		outpoint := input.TxID + ":" + strconv.FormatUint(uint64(input.Vout), 10)
		if strings.TrimSpace(input.TxID) == "" {
			return nil, nil, nil, nil, 0, fmt.Errorf("empty BTC input txid")
		}
		if _, duplicate := seen[outpoint]; duplicate {
			return nil, nil, nil, nil, 0, fmt.Errorf("duplicate BTC input: %s", outpoint)
		}
		seen[outpoint] = struct{}{}
		utxoData := state[keys.KeyFrostBtcUtxo(vaultID, input.TxID, input.Vout)]
		if len(utxoData) == 0 {
			return nil, nil, nil, nil, 0, fmt.Errorf("BTC input not found: %s", outpoint)
		}
		var utxo pb.FrostUtxo
		if err := proto.Unmarshal(utxoData, &utxo); err != nil {
			return nil, nil, nil, nil, 0, fmt.Errorf("invalid BTC input state: %s", outpoint)
		}
		amount, err := strconv.ParseUint(strings.TrimSpace(utxo.Amount), 10, 64)
		if err != nil || amount == 0 || amount != input.Amount || utxo.VaultId != vaultID || utxo.Txid != input.TxID || utxo.Vout != input.Vout {
			return nil, nil, nil, nil, 0, fmt.Errorf("BTC input state mismatch: %s", outpoint)
		}
		if len(utxo.ScriptPubkey) == 0 || len(utxo.Tweak) != 0 && len(utxo.Tweak) != 32 {
			return nil, nil, nil, nil, 0, fmt.Errorf("invalid BTC input script/tweak: %s", outpoint)
		}
		operator, err := btcInputOperatorPubkey(vault.GroupPubkey, utxo.Tweak)
		if err != nil {
			return nil, nil, nil, nil, 0, fmt.Errorf("BTC input[%d] operator key: %w", i, err)
		}
		if err := template.ValidateInputPolicy(i, operator, cfg.GuardianPubkey, utxo.ScriptPubkey); err != nil {
			return nil, nil, nil, nil, 0, err
		}
		lock := state[keys.KeyFrostBtcLockedUtxo(vaultID, input.TxID, input.Vout)]
		locked := len(lock) > 0

		if requiredLockOwner == "" {
			if locked && len(lock) > 0 {
				return nil, nil, nil, nil, 0, fmt.Errorf("BTC input already locked: %s", outpoint)
			}
		} else if !locked || string(lock) != requiredLockOwner {
			return nil, nil, nil, nil, 0, fmt.Errorf("BTC input is not reserved by job: %s", outpoint)
		}
		totalInput, err = addUint64(totalInput, amount)
		if err != nil {
			return nil, nil, nil, nil, 0, err
		}
		inputs[i] = chain.UTXO{TxID: utxo.Txid, Vout: utxo.Vout, Amount: amount, ScriptPubKey: append([]byte(nil), utxo.ScriptPubkey...), ConfirmHeight: utxo.FinalizeHeight}
		utxos[i] = &utxo
		scriptPubKeys[i] = append([]byte(nil), utxo.ScriptPubkey...)
	}
	return &vault, inputs, utxos, scriptPubKeys, totalInput, nil
}

func validateBTCWithdrawTemplate(
	sv StateView,
	chainName, asset, jobID string,
	vaultID uint32,
	keyEpoch uint64,
	templateData, templateHash []byte,
	withdraws []*FrostWithdrawRequest,
) (*validatedBTCWithdraw, error) {
	if chain.NormalizeChain(chainName) != chain.ChainBTC || len(withdraws) == 0 {
		return nil, fmt.Errorf("invalid BTC withdraw context")
	}
	template, err := btc.FromJSON(templateData)
	if err != nil {
		return nil, fmt.Errorf("invalid BTC template: %w", err)
	}
	if len(template.Inputs) == 0 || len(template.Outputs) == 0 {
		return nil, fmt.Errorf("BTC template inputs/outputs must not be empty")
	}
	if template.VaultID != vaultID || template.KeyEpoch != keyEpoch {
		return nil, fmt.Errorf("BTC template vault/key epoch mismatch")
	}
	if !bytes.Equal(template.TemplateHash(), templateHash) {
		return nil, fmt.Errorf("BTC template hash mismatch")
	}
	if len(template.WithdrawIDs) != len(withdraws) {
		return nil, fmt.Errorf("BTC template withdraw count mismatch")
	}
	vaultCfg, err := loadFrostVaultConfig(sv, chain.NormalizeChain(chainName))
	if err != nil {
		return nil, err
	}
	if vaultCfg.BtcFeeSatsPerVbyte == 0 {
		return nil, fmt.Errorf("BTC fee rate is not configured")
	}
	withdrawIDs := make([]string, len(withdraws))
	grossAmounts := make([]uint64, len(withdraws))
	recipients := make([]string, len(withdraws))
	for i, withdraw := range withdraws {
		if withdraw == nil || withdraw.WithdrawID == "" || withdraw.Status != WithdrawStatusQueued {
			return nil, fmt.Errorf("invalid BTC withdraw at index %d", i)
		}
		if chain.NormalizeChain(withdraw.Chain) != chain.ChainBTC || !strings.EqualFold(withdraw.Asset, asset) {
			return nil, fmt.Errorf("BTC withdraw chain/asset mismatch")
		}
		if template.WithdrawIDs[i] != withdraw.WithdrawID || i >= len(template.Outputs) {
			return nil, fmt.Errorf("BTC withdraw order mismatch")
		}
		amount, err := strconv.ParseUint(strings.TrimSpace(withdraw.Amount), 10, 64)
		if err != nil || amount == 0 {
			return nil, fmt.Errorf("invalid BTC withdraw amount: %s", withdraw.WithdrawID)
		}
		withdrawIDs[i] = withdraw.WithdrawID
		grossAmounts[i] = amount
		recipients[i] = withdraw.To
	}

	vault, inputs, utxos, scriptPubKeys, totalInput, err := validateBTCLocalInputs(sv, template, vaultID, keyEpoch, "")
	if err != nil {
		return nil, err
	}

	recipientScripts := make([][]byte, len(recipients))
	for i, recipient := range recipients {
		recipientScripts[i], err = btc.AddressScript(recipient)
		if err != nil {
			return nil, fmt.Errorf("invalid BTC recipient for withdraw %s: %w", withdrawIDs[i], err)
		}
	}
	netAmounts, fee, changeAmount, err := btc.ExpectedUserPaidWithdrawAmounts(
		totalInput,
		grossAmounts,
		len(inputs),
		recipientScripts,
		scriptPubKeys[0],
		vaultCfg.BtcFeeSatsPerVbyte,
	)
	if err != nil {
		return nil, err
	}
	outputs := make([]chain.WithdrawOutput, len(withdraws))
	for i, withdraw := range withdraws {
		if template.Outputs[i].Address != recipients[i] || template.Outputs[i].Amount != netAmounts[i] {
			return nil, fmt.Errorf("BTC withdraw output mismatch: %s", withdraw.WithdrawID)
		}
		outputs[i] = chain.WithdrawOutput{
			WithdrawID: withdraw.WithdrawID,
			To:         recipients[i],
			Amount:     strconv.FormatUint(netAmounts[i], 10),
		}
	}
	changeAddress := ""
	if changeAmount > 0 {
		if len(template.Outputs) != len(withdraws)+1 {
			return nil, fmt.Errorf("BTC change output count mismatch")
		}
		change := template.Outputs[len(withdraws)]
		if change.Amount != changeAmount {
			return nil, fmt.Errorf("BTC change amount mismatch")
		}
		changeScript, err := btc.AddressScript(change.Address)
		if err != nil || !bytes.Equal(changeScript, scriptPubKeys[0]) {
			return nil, fmt.Errorf("BTC change output is not controlled by source vault")
		}
		changeAddress = change.Address
	} else if len(template.Outputs) != len(withdraws) {
		return nil, fmt.Errorf("unexpected BTC change output")
	}

	canonical, err := btc.NewBTCTemplate(chain.WithdrawTemplateParams{
		Chain: chain.ChainBTC, Asset: asset, VaultID: vaultID, KeyEpoch: keyEpoch,
		WithdrawIDs: withdrawIDs, Outputs: outputs, Inputs: inputs,
		GuardianPubkey: vaultCfg.GuardianPubkey, BTCInputPubkeys: btcInputPubkeys(vault.GroupPubkey, utxos),
		Fee: fee, ChangeAmount: changeAmount, ChangeAddress: changeAddress,
	})
	if err != nil || !bytes.Equal(canonical.Serialize(), template.Serialize()) {
		return nil, fmt.Errorf("BTC template is not canonical")
	}
	canonicalData, err := canonical.ToJSON()
	if err != nil {
		return nil, err
	}
	expectedJobID := chain.ComputeWithdrawJobID(chainName, asset, vaultID, withdraws[0].Seq, canonical.TemplateHash(), keyEpoch)
	if withdraws[0].BtcProtocolVersion == 3 {
		if networkChainIDFromView(sv) == 0 || vaultCfg.DepositDexChainId != networkChainIDFromView(sv) {
			return nil, fmt.Errorf("BTC v3 vault configuration DEX network mismatch")
		}
		if len(withdraws) != 1 || len(utxos) != 1 || len(utxos[0].OriginId) != 32 {
			return nil, fmt.Errorf("invalid BTC v3 part context")
		}
		expectedJobID = chain.BTCWithdrawPartJobID(chain.BTCWithdrawPartIntent{
			Network: networkChainIDFromView(sv), BTCNetwork: vaultCfg.BtcNetwork,
			ParentID: withdraws[0].WithdrawID, PartSeq: withdraws[0].BtcNextPartSeq,
			VaultID: vaultID, KeyEpoch: keyEpoch, Gross: grossAmounts[0], Fee: fee,
			OriginID: utxos[0].OriginId, TemplateHash: canonical.TemplateHash(),
		})
	}
	if jobID != expectedJobID {
		return nil, fmt.Errorf("BTC job_id mismatch")
	}
	return &validatedBTCWithdraw{
		template: canonical, canonicalData: canonicalData, vault: vault, utxos: utxos,
		scriptPubKeys: scriptPubKeys, totalInput: totalInput, guardianPubkey: vaultCfg.GuardianPubkey,
	}, nil
}

func validateBTCClaimedWithdrawTemplate(sv StateView, jobID string, job *pb.FrostWithdrawJobState) (*validatedBTCWithdraw, error) {
	if job == nil || job.Status != frostWithdrawJobStatusClaimed || job.JobId != jobID {
		return nil, fmt.Errorf("invalid BTC withdraw claim")
	}
	template, err := btc.FromJSON(job.TemplateData)
	if err != nil || len(template.Inputs) == 0 || len(template.Outputs) == 0 || template.VaultID != job.VaultId || template.KeyEpoch != job.KeyEpoch || !bytes.Equal(template.TemplateHash(), job.TemplateHash) {
		return nil, fmt.Errorf("invalid claimed BTC template")
	}
	vault, _, utxos, scriptPubKeys, totalInput, err := validateBTCLocalInputs(sv, template, job.VaultId, job.KeyEpoch, jobID)
	if err != nil {
		return nil, err
	}
	cfg, err := loadFrostVaultConfig(sv, chain.ChainBTC)
	if err != nil {
		return nil, err
	}
	return &validatedBTCWithdraw{template: template, vault: vault, utxos: utxos, scriptPubKeys: scriptPubKeys, totalInput: totalInput, guardianPubkey: cfg.GuardianPubkey}, nil
}

func validateBTCWithdrawCoordinator(validated *validatedBTCWithdraw, jobID string, keyEpoch uint64, submitter string) error {
	if validated == nil || validated.vault == nil {
		return fmt.Errorf("missing validated BTC vault")
	}
	expected, err := chain.WithdrawCoordinator(jobID, keyEpoch, validated.vault.CommitteeMembers)
	if err != nil || submitter != expected {
		return fmt.Errorf("BTC withdraw submitter is not coordinator")
	}
	return nil
}

func reserveBTCWithdrawInputs(sv StateView, validated *validatedBTCWithdraw, chainName, asset, jobID string) ([]WriteOp, error) {
	if validated == nil || len(validated.utxos) == 0 {
		return nil, fmt.Errorf("missing BTC inputs to reserve")
	}
	available, err := getVaultAvailableBalance(sv, chainName, asset, validated.template.VaultID)
	if err != nil || available.Cmp(new(big.Int).SetUint64(validated.totalInput)) < 0 {
		return nil, fmt.Errorf("insufficient BTC vault available balance")
	}
	remaining := new(big.Int).Sub(available, new(big.Int).SetUint64(validated.totalInput)) // safe-math-ok: 前面已校验 available >= totalInput，扣款不下溢
	ops := make([]WriteOp, 0, len(validated.utxos)*3+1)
	for _, utxo := range validated.utxos {
		refKey := keys.KeyFrostBtcUtxoSeqRef(validated.template.VaultID, utxo.Txid, utxo.Vout)
		seqData, exists, err := sv.Get(refKey)
		if err != nil || !exists || len(seqData) == 0 {
			return nil, fmt.Errorf("BTC spendable index ref missing: %s:%d", utxo.Txid, utxo.Vout)
		}
		seq, err := strconv.ParseUint(string(seqData), 10, 64)
		if err != nil || seq == 0 {
			return nil, fmt.Errorf("invalid BTC spendable index ref")
		}
		spendableKey := keys.KeyFrostBtcSpendableUtxoIndex(validated.template.VaultID, seq)
		spendableData, exists, err := sv.Get(spendableKey)
		if err != nil || !exists || len(spendableData) == 0 {
			return nil, fmt.Errorf("BTC input is not spendable: %s:%d", utxo.Txid, utxo.Vout)
		}
		var indexed pb.FrostUtxo
		if err := proto.Unmarshal(spendableData, &indexed); err != nil || !proto.Equal(&indexed, utxo) {
			return nil, fmt.Errorf("BTC spendable index mismatch: %s:%d", utxo.Txid, utxo.Vout)
		}
		ops = append(ops,
			WriteOp{Key: keys.KeyFrostBtcLockedUtxo(validated.template.VaultID, utxo.Txid, utxo.Vout), Value: []byte(jobID), Category: "frost_btc_utxo_lock"},
			deleteWriteOp(spendableKey, "frost_btc_spendable_prune"),
			deleteWriteOp(refKey, "frost_btc_utxo_ref_prune"),
		)
	}
	ops = append(ops, WriteOp{
		Key:   keys.KeyFrostVaultAvailableBalance(chainName, asset, validated.template.VaultID),
		Value: []byte(remaining.String()), Category: "frost_vault_available",
	})
	return ops, nil
}

func equalByteSlices(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

func signedPackageMatchesInputSigs(pkg []byte, sigs [][]byte) bool {
	offset := 0
	for _, sig := range sigs {
		if len(pkg)-offset < len(sig) || !bytes.Equal(pkg[offset:offset+len(sig)], sig) {
			return false
		}
		offset += len(sig)
	}
	return offset == len(pkg)
}

func btcInputOperatorPubkey(group, tweak []byte) ([]byte, error) {
	if len(tweak) == 0 {
		return utils.NormalizeSecp256k1XOnlyPubKey(group)
	}
	if len(tweak) != 32 {
		return nil, fmt.Errorf("invalid BTC input tweak")
	}
	return utils.ComputeTweakedXOnlyPubkey(group, tweak)
}
func btcInputPubkeys(group []byte, utxos []*pb.FrostUtxo) [][]byte {
	out := make([][]byte, len(utxos))
	for i, u := range utxos {
		out[i], _ = btcInputOperatorPubkey(group, u.Tweak)
	}
	return out
}
func readBTCStateMany(sv StateView, stateKeys []string) (map[string][]byte, error) {
	if bulk, ok := sv.(interface {
		GetMany([]string) (map[string][]byte, error)
	}); ok {
		return bulk.GetMany(stateKeys)
	}
	out := make(map[string][]byte, len(stateKeys))
	for _, key := range stateKeys {
		v, found, err := sv.Get(key)
		if err != nil && !isStateViewNotFound(err) {
			return nil, err
		}
		if found {
			out[key] = v
		}
	}
	return out, nil
}
