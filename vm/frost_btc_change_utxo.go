package vm

import (
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/keys"
	"dex/logs"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

func splitBTCSignedPackageBytes(data []byte, inputCount int) ([][]byte, error) {
	if inputCount <= 0 {
		return nil, fmt.Errorf("invalid BTC input count: %d", inputCount)
	}
	if len(data) != inputCount*64 {
		return nil, fmt.Errorf("BTC signature bytes length mismatch: got=%d want=%d", len(data), inputCount*64)
	}
	out := make([][]byte, inputCount)
	for i := 0; i < inputCount; i++ {
		start := i * 64
		out[i] = append([]byte(nil), data[start:start+64]...)
	}
	return out, nil
}

func btcTemplateChangeOutput(template *btc.BTCTemplate) (uint32, uint64, bool) {
	if template == nil || len(template.Outputs) <= len(template.WithdrawIDs) {
		return 0, 0, false
	}
	idx := len(template.Outputs) - 1
	amount := template.Outputs[idx].Amount
	if amount == 0 {
		return 0, 0, false
	}
	return uint32(idx), amount, true
}

func btcSignedTemplateTxID(template *btc.BTCTemplate, inputSigs [][]byte) (string, error) {
	if template == nil {
		return "", fmt.Errorf("missing BTC template")
	}
	return template.TxID()
}

func loadBTCChangeSourceUTXO(sv StateView, vaultID uint32, template *btc.BTCTemplate) (*pb.FrostUtxo, error) {
	if template == nil || len(template.Inputs) == 0 {
		return nil, fmt.Errorf("BTC template inputs empty")
	}
	for _, input := range template.Inputs {
		data, exists, err := sv.Get(keys.KeyFrostBtcUtxo(vaultID, input.TxID, input.Vout))
		if err != nil {
			if isStateViewNotFound(err) {
				continue
			}
			return nil, err
		}
		if !exists || len(data) == 0 {
			continue
		}
		var utxo pb.FrostUtxo
		if err := proto.Unmarshal(data, &utxo); err != nil {
			continue
		}
		return &utxo, nil
	}
	return nil, fmt.Errorf("source BTC UTXO not found for vault=%d", vaultID)
}

func setBTCSpendableUTXOIndex(sv StateView, vaultID uint32, seq uint64, utxo *pb.FrostUtxo, data []byte) error {
	active, err := chain.BTCIsolationActive(sv.Get)
	if err != nil {
		return err
	}
	return setBTCSpendableUTXOIndexForPolicy(sv, vaultID, seq, utxo, data, active)
}

func setBTCSpendableUTXOIndexForPolicy(sv StateView, vaultID uint32, seq uint64, utxo *pb.FrostUtxo, data []byte, active bool) error {
	if seq == 0 || utxo == nil || len(data) == 0 {
		return fmt.Errorf("invalid BTC spendable UTXO index input")
	}
	amount, err := strconv.ParseUint(utxo.GetAmount(), 10, 64)
	if err != nil {
		return fmt.Errorf("invalid BTC UTXO amount %q: %w", utxo.GetAmount(), err)
	}
	if amount == 0 {
		return fmt.Errorf("invalid BTC UTXO amount %q: must be positive", utxo.GetAmount())
	}
	if active && len(utxo.OriginId) != 32 {
		return fmt.Errorf("BTC amount index requires an origin")
	}
	sv.Set(keys.KeyFrostBtcSpendableUtxoIndex(vaultID, seq), data)
	sv.Set(keys.KeyFrostBtcUtxoSeqRef(vaultID, utxo.Txid, utxo.Vout), []byte(strconv.FormatUint(seq, 10)))
	if active {
		sv.Set(keys.KeyFrostBtcUtxoAmount(vaultID, amount, utxo.Txid, utxo.Vout), data)
	}
	return nil
}

func delBTCSpendableUTXOIndex(sv StateView, vaultID uint32, txid string, vout uint32) {
	refKey := keys.KeyFrostBtcUtxoSeqRef(vaultID, txid, vout)
	seqData, exists, err := sv.Get(refKey)
	if err != nil || !exists || len(seqData) == 0 {
		return
	}
	seq, err := strconv.ParseUint(string(seqData), 10, 64)
	if err != nil || seq == 0 {
		return
	}
	sv.Del(keys.KeyFrostBtcSpendableUtxoIndex(vaultID, seq))
	sv.Del(keys.KeyFrostBtcUtxoFIFOIndex(vaultID, seq))
	sv.Del(refKey)
}

func ensureBTCChangeUTXO(sv StateView, vaultID uint32, template *btc.BTCTemplate, inputSigs [][]byte, height uint64) (bool, uint64, error) {
	changeVout, changeAmount, ok := btcTemplateChangeOutput(template)
	if !ok {
		return false, 0, nil
	}
	txid, err := btcSignedTemplateTxID(template, inputSigs)
	if err != nil {
		return false, 0, err
	}
	changeKey := keys.KeyFrostBtcUtxo(vaultID, txid, changeVout)
	if data, exists, err := sv.Get(changeKey); err != nil {
		if !isStateViewNotFound(err) {
			return false, 0, err
		}
	} else if exists && len(data) > 0 {
		return false, 0, nil
	}
	source, err := loadBTCChangeSourceUTXO(sv, vaultID, template)
	if err != nil {
		return false, 0, err
	}
	change := &pb.FrostUtxo{
		Txid:           txid,
		Vout:           changeVout,
		Amount:         strconv.FormatUint(changeAmount, 10),
		VaultId:        vaultID,
		FinalizeHeight: height,
		RequestId:      "change:" + txid + ":" + strconv.FormatUint(uint64(changeVout), 10),
		ScriptPubkey:   append([]byte(nil), source.ScriptPubkey...),
		Tweak:          append([]byte(nil), source.Tweak...),
	}
	active, err := chain.BTCIsolationActive(sv.Get)
	if err != nil {
		return false, 0, err
	}
	if active {
		if len(template.Inputs) > 1 {
			                                                                   
			                                                                  
			                                                                
			if err := ensureBTCUTXOOrigin(sv, change, true); err != nil {
				return false, 0, err
			}
		} else {
			if err := ensureBTCUTXOOrigin(sv, source, true); err != nil {
				return false, 0, err
			}
			change.OriginId, change.OriginLegacy = append([]byte(nil), source.OriginId...), source.OriginLegacy
		}
	}
	data, err := proto.Marshal(change)
	if err != nil {
		return false, 0, err
	}
	seqKey := keys.KeyFrostBtcUtxoFIFOSeq(vaultID)
	currentSeq := readUint64FromState(sv, seqKey)
	if currentSeq == ^uint64(0) {
		return false, 0, fmt.Errorf("BTC UTXO sequence overflow for vault=%d", vaultID)
	}
	seq := currentSeq + 1
	snap := sv.Snapshot()
	sv.Set(changeKey, data)
	sv.Set(keys.KeyFrostBtcUtxoFIFOIndex(vaultID, seq), data)
	if err := setBTCSpendableUTXOIndex(sv, vaultID, seq, change, data); err != nil {
		_ = sv.Revert(snap)
		return false, 0, err
	}
	sv.Set(seqKey, []byte(strconv.FormatUint(seq, 10)))
	sv.Set(keys.KeyFrostBtcUtxoArtifact(vaultID, txid, changeVout), []byte(txid))
	if _, exists, _ := sv.Get(keys.KeyFrostBtcUtxoFIFOHead(vaultID)); !exists {
		sv.Set(keys.KeyFrostBtcUtxoFIFOHead(vaultID), []byte("1"))
	}
	return true, changeAmount, nil
}

func btcUnlockedUTXOBalance(sv StateView, vaultID uint32) (*big.Int, error) {
	rows, err := sv.Scan(keys.KeyFrostBtcUtxoFIFOIndexPrefix(vaultID))
	if err != nil {
		return nil, err
	}
	rowKeys := make([]string, 0, len(rows))
	for k := range rows {
		rowKeys = append(rowKeys, k)
	}
	sort.Strings(rowKeys)
	total := big.NewInt(0)
	for _, k := range rowKeys {
		var utxo pb.FrostUtxo
		if err := proto.Unmarshal(rows[k], &utxo); err != nil || utxo.VaultId != vaultID {
			continue
		}
		if lock, locked, err := sv.Get(keys.KeyFrostBtcLockedUtxo(vaultID, utxo.Txid, utxo.Vout)); err != nil {
			if !isStateViewNotFound(err) {
				return nil, err
			}
		} else if locked && len(lock) > 0 {
			continue
		}
		amount, err := ParseBalance(utxo.Amount)
		if err != nil || amount.Sign() <= 0 {
			continue
		}
		total.Add(total, amount)
	}
	return total, nil
}

type vaultFundsLotRef struct {
	height    uint64
	seq       uint64
	requestID string
}

func parseVaultFundsLotIndexSuffix(key, prefix string) (uint64, uint64, bool) {
	if !strings.HasPrefix(key, prefix) {
		return 0, 0, false
	}
	parts := strings.Split(strings.TrimPrefix(key, prefix), "_")
	if len(parts) != 2 {
		return 0, 0, false
	}
	height, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	seq, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return height, seq, true
}

func vaultFundsLotAtOrAfter(height, seq, headHeight, headSeq uint64) bool {
	if headHeight == 0 {
		return true
	}
	return height > headHeight || (height == headHeight && seq >= headSeq)
}

func fundsLotAvailableBalance(sv StateView, chainName, asset string, vaultID uint32) (*big.Int, bool, error) {
	prefix := keys.KeyFrostFundsLotPrefix(chainName, asset, vaultID)
	rows, err := sv.Scan(prefix)
	if err != nil {
		return nil, false, err
	}
	head := GetFundsLotHead(sv, chainName, asset, vaultID)
	refs := make([]vaultFundsLotRef, 0, len(rows))
	foundLot := false
	for k, v := range rows {
		height, seq, ok := parseVaultFundsLotIndexSuffix(k, prefix)
		if !ok {
			continue
		}
		foundLot = true
		if !vaultFundsLotAtOrAfter(height, seq, head.Height, head.Seq) || len(v) == 0 {
			continue
		}
		refs = append(refs, vaultFundsLotRef{height: height, seq: seq, requestID: string(v)})
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].height != refs[j].height {
			return refs[i].height < refs[j].height
		}
		if refs[i].seq != refs[j].seq {
			return refs[i].seq < refs[j].seq
		}
		return refs[i].requestID < refs[j].requestID
	})
	total := big.NewInt(0)
	for _, ref := range refs {
		data, exists, err := sv.Get(keys.KeyRechargeRequest(ref.requestID))
		if err != nil {
			return nil, foundLot, err
		}
		if !exists || len(data) == 0 {
			continue
		}
		var recharge pb.RechargeRequest
		if err := proto.Unmarshal(data, &recharge); err != nil {
			continue
		}
		amount, err := ParseBalance(recharge.Amount)
		if err != nil || amount.Sign() <= 0 {
			continue
		}
		amount, _, err = fundsLotRemainingAmount(sv, chainName, asset, vaultID, ref.height, ref.seq, amount)
		if err != nil {
			return nil, foundLot, err
		}
		total.Add(total, amount)
	}
	return total, foundLot, nil
}

func syncVaultAvailableBalanceFromLedger(sv StateView, chainName, asset string, vaultID uint32) error {
	var (
		total *big.Int
		found bool
		err   error
	)
	if isBTCChainName(chainName) {
		total, err = btcUnlockedUTXOBalance(sv, vaultID)
		found = true
	} else {
		total, found, err = fundsLotAvailableBalance(sv, chainName, asset, vaultID)
		if err == nil && found {
			reserved, reservedErr := getVaultReservedBalance(sv, chainName, asset, vaultID)
			if reservedErr != nil {
				return reservedErr
			}
			if total.Cmp(reserved) < 0 {
				return fmt.Errorf("vault reserved balance exceeds funds lot balance")
			}
			total.Sub(total, reserved)
		}
	}
	if err != nil || !found {
		return err
	}
	current, err := getVaultAvailableBalance(sv, chainName, asset, vaultID)
	if err != nil {
		return err
	}
	if current.Cmp(total) == 0 {
		return nil
	}
	setVaultAvailableBalance(sv, chainName, asset, vaultID, total)
	return nil
}

func syncBTCVaultAvailableBalanceFromUTXOs(sv StateView, chainName, asset string, vaultID uint32) error {
	return syncVaultAvailableBalanceFromLedger(sv, chainName, asset, vaultID)
}

func applyFrostBtcChangeOutputs(sv StateView, height uint64) {
	rows, err := sv.Scan(keys.KeyFrostWithdrawJobPrefix())
	if err != nil {
		logs.Warn("[FrostBTCChange] scan withdraw jobs failed: %v", err)
		return
	}
	rowKeys := make([]string, 0, len(rows))
	for k := range rows {
		rowKeys = append(rowKeys, k)
	}
	sort.Strings(rowKeys)
	for _, k := range rowKeys {
		var job pb.FrostWithdrawJobState
		if err := proto.Unmarshal(rows[k], &job); err != nil || job.Status != frostWithdrawJobStatusSigned {
			continue
		}
		if !isBTCChainName(job.Chain) || len(job.TemplateData) == 0 {
			continue
		}
		template, err := btc.FromJSON(job.TemplateData)
		if err != nil {
			logs.Warn("[FrostBTCChange] decode template failed job=%s: %v", job.JobId, err)
			continue
		}
		if _, _, ok := btcTemplateChangeOutput(template); !ok {
			continue
		}
		pkgData, exists, err := sv.Get(keys.KeyFrostSignedPackage(job.JobId, 0))
		if err != nil || !exists || len(pkgData) == 0 {
			continue
		}
		var pkg pb.FrostSignedPackage
		if err := proto.Unmarshal(pkgData, &pkg); err != nil {
			continue
		}
		inputSigs, err := splitBTCSignedPackageBytes(pkg.SignedPackageBytes, len(template.Inputs))
		if err != nil {
			logs.Warn("[FrostBTCChange] split signatures failed job=%s: %v", job.JobId, err)
			continue
		}
		created, changeAmount, err := ensureBTCChangeUTXO(sv, job.VaultId, template, inputSigs, height)
		if err != nil {
			logs.Warn("[FrostBTCChange] ensure change UTXO failed job=%s: %v", job.JobId, err)
			continue
		}
		if created {
			advanceBtcUtxoFIFOHead(sv, job.VaultId)
			if err := addVaultAvailableBalance(sv, job.Chain, job.Asset, job.VaultId, new(big.Int).SetUint64(changeAmount)); err != nil {
				logs.Warn("[FrostBTCChange] update available balance failed job=%s: %v", job.JobId, err)
			}
		}
	}
}
