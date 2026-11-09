                                              
                                                                    

package planning

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	chainpkg "dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/keys"
	"dex/pb"
	"dex/utils"

	"github.com/btcsuite/btcd/chaincfg"
	"google.golang.org/protobuf/proto"
)

const btcSpendableIndexScanBatch = 64

type orderedStateScanner interface {
	ScanOrderedAfter(prefix, startAfter string, limit int, reverse bool, fn func(k string, v []byte) bool) error
}

type btcUTXOScanStats struct {
	Scanned      uint64
	IndexMissing uint64
	DecodeFailed uint64
	Locked       uint64
	ZeroAmount   uint64
	TooSmall     uint64
	Selected     uint64
}

type btcUTXOSelectionError struct {
	Code   string
	Reason string

	Chain    string
	VaultID  uint32
	Need     uint64
	Got      uint64
	Head     uint64
	MaxSeq   uint64
	ScanStat btcUTXOScanStats
}

func (e *btcUTXOSelectionError) Error() string {
	return fmt.Sprintf(
		"UTXO selection failed: need=%d got=%d code=%s chain=%s vault=%d head=%d max_seq=%d scanned=%d selected=%d locked=%d missing=%d decode_failed=%d zero_amount=%d too_small=%d reason=%s",
		e.Need, e.Got, e.Code, e.Chain, e.VaultID, e.Head, e.MaxSeq,
		e.ScanStat.Scanned, e.ScanStat.Selected, e.ScanStat.Locked, e.ScanStat.IndexMissing, e.ScanStat.DecodeFailed, e.ScanStat.ZeroAmount, e.ScanStat.TooSmall,
		e.Reason,
	)
}

func newBTCUTXOSelectionError(
	code, reason, chain string,
	vaultID uint32,
	need, got, head, maxSeq uint64,
	scanStat btcUTXOScanStats,
) error {
	return &btcUTXOSelectionError{
		Code:     code,
		Reason:   reason,
		Chain:    chain,
		VaultID:  vaultID,
		Need:     need,
		Got:      got,
		Head:     head,
		MaxSeq:   maxSeq,
		ScanStat: scanStat,
	}
}

func classifyBTCUTXOInsufficient(got uint64, stat btcUTXOScanStats) (string, string) {
	if got == 0 {
		switch {
		case stat.Scanned == 0:
			return "fifo_window_empty", "no FIFO window scanned"
		case stat.IndexMissing == stat.Scanned:
			return "fifo_index_missing_all", "all FIFO index entries are missing"
		case stat.Locked == stat.Scanned:
			return "all_locked", "all candidate UTXOs are locked"
		case stat.DecodeFailed == stat.Scanned:
			return "decode_failed_all", "all candidate UTXOs failed to decode"
		case stat.ZeroAmount == stat.Scanned:
			return "zero_amount_all", "all candidate UTXOs have zero amount"
		default:
			return "no_spendable_utxo", "no spendable UTXO found in FIFO window"
		}
	}
	if stat.TooSmall > 0 && stat.Selected == 0 {
		return "selected_but_insufficient_amount", "spendable UTXOs exist but all are below required amount"
	}
	if stat.Selected == 0 {
		return "no_spendable_utxo", "no spendable UTXO found in FIFO window"
	}
	return "selected_but_insufficient_amount", "spendable UTXOs exist but total amount is below required amount"
}

                                                                            
                                                                        
func (p *JobWindowPlanner) selectBTCUTXOs(chain string, vaultID uint32) ([]chainpkg.UTXO, error) {
	return p.selectBTCUTXOsCovering(chain, vaultID, 0)
}

type btcUTXOTarget func(inputCount int) uint64

func (p *JobWindowPlanner) calculateBTCVaultMaxSpendableUTXO(chain string, vaultID uint32) (uint64, error) {
	utxos, usedIndex, err := p.selectBTCSpendableUTXOsFromIndex(chain, vaultID, func(inputCount int) uint64 {
		return 0
	})
	if err != nil {
		return 0, err
	}
	if usedIndex {
		if len(utxos) == 0 {
			return 0, nil
		}
		return utxos[0].Amount, nil
	}
	return p.calculateBTCVaultFirstSpendableUTXOFIFO(chain, vaultID)
}

func (p *JobWindowPlanner) calculateBTCVaultFirstSpendableUTXOFIFO(chain string, vaultID uint32) (uint64, error) {
	head := uint64(1)
	if headData, headExists, err := p.stateReader.Get(keys.KeyFrostBtcUtxoFIFOHead(vaultID)); err != nil {
		return 0, err
	} else if headExists && len(headData) > 0 {
		parsed, err := strconv.ParseUint(string(headData), 10, 64)
		if err != nil || parsed == 0 {
			return 0, nil
		}
		head = parsed
	}

	seqData, exists, err := p.stateReader.Get(keys.KeyFrostBtcUtxoFIFOSeq(vaultID))
	if err != nil {
		return 0, err
	}
	if !exists || len(seqData) == 0 {
		return 0, nil
	}
	maxSeq, err := strconv.ParseUint(string(seqData), 10, 64)
	if err != nil || maxSeq == 0 || head > maxSeq {
		return 0, nil
	}

	for seq := head; seq <= maxSeq; seq++ {
		utxoData, exists, err := p.stateReader.Get(keys.KeyFrostBtcUtxoFIFOIndex(vaultID, seq))
		if err != nil {
			return 0, err
		}
		if !exists || len(utxoData) == 0 {
			continue
		}
		var utxo pb.FrostUtxo
		if err := proto.Unmarshal(utxoData, &utxo); err != nil || utxo.VaultId != vaultID {
			continue
		}
		if lockVal, locked, err := p.stateReader.Get(keys.KeyFrostBtcLockedUtxo(vaultID, utxo.Txid, utxo.Vout)); err != nil {
			return 0, err
		} else if locked && len(lockVal) > 0 {
			continue
		}
		amount, err := parseBTCAmount(utxo.Amount)
		if err == nil {
			return amount, nil
		}
	}
	return 0, nil
}

func (p *JobWindowPlanner) selectBTCUTXOsCovering(chain string, vaultID uint32, minAmount uint64) ([]chainpkg.UTXO, error) {
	return p.selectBTCUTXOsMeetingTarget(chain, vaultID, func(inputCount int) uint64 {
		return minAmount
	})
}

func (p *JobWindowPlanner) selectBTCUTXOsMeetingTarget(chain string, vaultID uint32, target btcUTXOTarget) ([]chainpkg.UTXO, error) {
	utxos, usedIndex, err := p.selectBTCSpendableUTXOsFromIndex(chain, vaultID, target)
	if err != nil {
		return nil, err
	}
	if usedIndex {
		if len(utxos) == 0 {
			return nil, newBTCUTXOSelectionError("no_spendable_utxo", "no spendable UTXO found in spendable index", chain, vaultID, target(0), 0, 0, 0, btcUTXOScanStats{})
		}
		need := target(len(utxos))
		got := sumUTXOAmount(utxos)
		if need > 0 && got < need {
			return nil, newBTCUTXOSelectionError("selected_but_insufficient_amount", "FIFO spendable UTXOs total amount is below required amount", chain, vaultID, need, got, 0, 0, btcUTXOScanStats{Scanned: uint64(len(utxos)), Selected: uint64(len(utxos)), TooSmall: 1})
		}
		return utxos, nil
	}
	return p.selectBTCUTXOsMeetingTargetFIFO(chain, vaultID, target)
}

func (p *JobWindowPlanner) selectBTCSpendableUTXOsFromIndex(chain string, vaultID uint32, target btcUTXOTarget) ([]chainpkg.UTXO, bool, error) {
	prefix := keys.KeyFrostBtcSpendableUtxoIndexPrefix(vaultID)
	startAfter := ""
	seenAny := false
	selected := make([]chainpkg.UTXO, 0, 1)
	var total uint64
	for {
		lastKey := ""
		batchCount := 0
		err := p.scanBTCSpendableIndex(prefix, startAfter, btcSpendableIndexScanBatch, false, func(k string, data []byte) bool {
			seenAny = true
			batchCount++
			lastKey = k
			utxo, ok := decodeBTCSpendableIndexUTXO(data, vaultID)
			if !ok {
				return true
			}
			if lockVal, locked, err := p.stateReader.Get(keys.KeyFrostBtcLockedUtxo(vaultID, utxo.TxID, utxo.Vout)); err != nil || (locked && len(lockVal) > 0) {
				return true
			}
			selected = append(selected, utxo)
			total += utxo.Amount
			need := target(len(selected))
			return need > 0 && total < need
		})
		if err != nil {
			return nil, seenAny, err
		}
		if len(selected) > 0 {
			need := target(len(selected))
			if need == 0 || total >= need {
				return selected, true, nil
			}
		}
		if batchCount < btcSpendableIndexScanBatch || lastKey == "" || lastKey == startAfter {
			break
		}
		startAfter = lastKey
	}
	return selected, seenAny, nil
}

func (p *JobWindowPlanner) scanBTCSpendableIndex(prefix, startAfter string, limit int, reverse bool, fn func(k string, v []byte) bool) error {
	if ordered, ok := p.stateReader.(orderedStateScanner); ok {
		return ordered.ScanOrderedAfter(prefix, startAfter, limit, reverse, fn)
	}
	rows := make(map[string][]byte)
	if err := p.stateReader.Scan(prefix, func(k string, v []byte) bool {
		if startAfter != "" {
			if reverse {
				if k >= startAfter {
					return true
				}
			} else if k <= startAfter {
				return true
			}
		}
		rows[k] = v
		return true
	}); err != nil {
		return err
	}
	rowKeys := make([]string, 0, len(rows))
	for k := range rows {
		rowKeys = append(rowKeys, k)
	}
	sort.Strings(rowKeys)
	if reverse {
		for i, j := 0, len(rowKeys)-1; i < j; i, j = i+1, j-1 {
			rowKeys[i], rowKeys[j] = rowKeys[j], rowKeys[i]
		}
	}
	if limit > 0 && len(rowKeys) > limit {
		rowKeys = rowKeys[:limit]
	}
	for _, k := range rowKeys {
		if !fn(k, rows[k]) {
			break
		}
	}
	return nil
}

func decodeBTCSpendableIndexUTXO(data []byte, vaultID uint32) (chainpkg.UTXO, bool) {
	if len(data) == 0 {
		return chainpkg.UTXO{}, false
	}
	var protoUtxo pb.FrostUtxo
	if err := proto.Unmarshal(data, &protoUtxo); err != nil || protoUtxo.VaultId != vaultID {
		return chainpkg.UTXO{}, false
	}
	amount, err := parseBTCAmount(protoUtxo.Amount)
	if err != nil {
		return chainpkg.UTXO{}, false
	}
	return chainpkg.UTXO{
		TxID:          protoUtxo.Txid,
		Vout:          protoUtxo.Vout,
		Amount:        amount,
		ScriptPubKey:  append([]byte(nil), protoUtxo.ScriptPubkey...),
		ConfirmHeight: protoUtxo.FinalizeHeight,
	}, true
}

func (p *JobWindowPlanner) selectBTCUTXOsCoveringFIFO(chain string, vaultID uint32, minAmount uint64) ([]chainpkg.UTXO, error) {
	return p.selectBTCUTXOsMeetingTargetFIFO(chain, vaultID, func(inputCount int) uint64 {
		return minAmount
	})
}

func (p *JobWindowPlanner) selectBTCUTXOsMeetingTargetFIFO(chain string, vaultID uint32, target btcUTXOTarget) ([]chainpkg.UTXO, error) {
	                       
	headKey := keys.KeyFrostBtcUtxoFIFOHead(vaultID)
	seqKey := keys.KeyFrostBtcUtxoFIFOSeq(vaultID)

	head := uint64(1)
	if headData, headExists, e := p.stateReader.Get(headKey); e != nil {
		return nil, newBTCUTXOSelectionError("fifo_head_read_failed", e.Error(), chain, vaultID, 0, 0, 0, 0, btcUTXOScanStats{})
	} else if headExists && len(headData) > 0 {
		parsed, pe := strconv.ParseUint(string(headData), 10, 64)
		if pe != nil || parsed == 0 {
			return nil, newBTCUTXOSelectionError("fifo_head_invalid", fmt.Sprintf("invalid head=%q", string(headData)), chain, vaultID, 0, 0, 0, 0, btcUTXOScanStats{})
		}
		head = parsed
	}

	seqData, exists, err := p.stateReader.Get(seqKey)
	if err != nil {
		return nil, newBTCUTXOSelectionError("fifo_seq_read_failed", err.Error(), chain, vaultID, 0, 0, head, 0, btcUTXOScanStats{})
	}
	if !exists || len(seqData) == 0 {
		return nil, newBTCUTXOSelectionError("fifo_seq_missing", "FIFO seq missing", chain, vaultID, 0, 0, head, 0, btcUTXOScanStats{})
	}
	maxSeq, err := strconv.ParseUint(string(seqData), 10, 64)
	if err != nil || maxSeq == 0 || head > maxSeq {
		return nil, newBTCUTXOSelectionError("fifo_seq_invalid", fmt.Sprintf("seq=%q head=%d", string(seqData), head), chain, vaultID, 0, 0, head, maxSeq, btcUTXOScanStats{})
	}

	scanStat := btcUTXOScanStats{}
	selected := make([]chainpkg.UTXO, 0, 1)
	total := uint64(0)
	for seq := head; seq <= maxSeq; seq++ {
		scanStat.Scanned++
		indexKey := keys.KeyFrostBtcUtxoFIFOIndex(vaultID, seq)
		utxoData, idxExists, e := p.stateReader.Get(indexKey)
		if e != nil {
			return nil, newBTCUTXOSelectionError("fifo_index_read_failed", e.Error(), chain, vaultID, 0, 0, head, maxSeq, scanStat)
		}
		if !idxExists || len(utxoData) == 0 {
			scanStat.IndexMissing++
			continue
		}

		var protoUtxo pb.FrostUtxo
		if e := proto.Unmarshal(utxoData, &protoUtxo); e != nil {
			scanStat.DecodeFailed++
			continue
		}
		if protoUtxo.VaultId != vaultID {
			scanStat.DecodeFailed++
			continue
		}

		lockKey := keys.KeyFrostBtcLockedUtxo(vaultID, protoUtxo.Txid, protoUtxo.Vout)
		if lockVal, locked, e := p.stateReader.Get(lockKey); e != nil {
			return nil, newBTCUTXOSelectionError("utxo_lock_read_failed", e.Error(), chain, vaultID, 0, 0, head, maxSeq, scanStat)
		} else if locked && len(lockVal) > 0 {
			scanStat.Locked++
			continue
		}

		amount, err := parseBTCAmount(protoUtxo.Amount)
		if err != nil {
			scanStat.ZeroAmount++
			continue
		}

		scanStat.Selected++
		selected = append(selected, chainpkg.UTXO{
			TxID:          protoUtxo.Txid,
			Vout:          protoUtxo.Vout,
			Amount:        amount,
			ScriptPubKey:  append([]byte(nil), protoUtxo.ScriptPubkey...),
			ConfirmHeight: protoUtxo.FinalizeHeight,
		})
		total += amount
		need := target(len(selected))
		if need == 0 || total >= need {
			return selected, nil
		}
		scanStat.TooSmall++
	}

	code, reason := classifyBTCUTXOInsufficient(total, scanStat)
	return nil, newBTCUTXOSelectionError(code, reason, chain, vaultID, target(len(selected)), total, head, maxSeq, scanStat)
}

func (p *JobWindowPlanner) btcFeeRateForChain(chainName string) (uint64, error) {
	if p == nil || p.stateReader == nil {
		return 0, fmt.Errorf("BTC fee config reader is unavailable")
	}
	key := keys.KeyFrostVaultConfig(chainpkg.NormalizeChain(chainName), 0)
	data, exists, err := p.stateReader.Get(key)
	if err != nil {
		return 0, fmt.Errorf("read BTC fee config: %w", err)
	}
	if !exists || len(data) == 0 {
		return 0, fmt.Errorf("BTC fee config not found")
	}
	var cfg pb.FrostVaultConfig
	if err := proto.Unmarshal(data, &cfg); err != nil {
		return 0, fmt.Errorf("decode BTC fee config: %w", err)
	}
	if cfg.BtcFeeSatsPerVbyte == 0 {
		return 0, fmt.Errorf("BTC fee rate is not configured")
	}
	return cfg.BtcFeeSatsPerVbyte, nil
}

                                                                         
                                                      
func (p *JobWindowPlanner) estimateBTCFee(chainName string, inputCount int, outputScripts [][]byte) (uint64, error) {
	feeRate, err := p.btcFeeRateForChain(chainName)
	if err != nil {
		return 0, err
	}
	return btc.EstimateWithdrawFee(inputCount, outputScripts, feeRate)
}

func sumUTXOAmount(utxos []chainpkg.UTXO) uint64 {
	var total uint64
	for _, utxo := range utxos {
		total += utxo.Amount
	}
	return total
}

func (p *JobWindowPlanner) getYoungestVaultTreasuryAddress(chain string) (string, error) {
	youngestVaultID, err := p.selectYoungestActiveVaultID(chain)
	if err != nil {
		return "", err
	}

	vaultStateKey := keys.KeyFrostVaultState(chain, youngestVaultID)
	vaultStateData, exists, err := p.stateReader.Get(vaultStateKey)
	if err != nil {
		return "", err
	}
	if !exists || len(vaultStateData) == 0 {
		return "", fmt.Errorf("vault state not found for youngest vault: chain=%s vault=%d", chain, youngestVaultID)
	}

	var state pb.FrostVaultState
	if err := proto.Unmarshal(vaultStateData, &state); err != nil {
		return "", err
	}
	cfgData, found, err := p.stateReader.Get(keys.KeyFrostVaultConfig(chain, 0))
	if err != nil || !found {
		return "", fmt.Errorf("BTC guardian configuration unavailable")
	}
	var cfg pb.FrostVaultConfig
	if proto.Unmarshal(cfgData, &cfg) != nil {
		return "", fmt.Errorf("invalid BTC guardian configuration")
	}
	addr, err := utils.DualCustodyTaprootAddress(state.GroupPubkey, cfg.GuardianPubkey, p.btcNetParamsForChain(chain))
	if err != nil {
		return "", fmt.Errorf("derive taproot address for youngest vault failed: chain=%s vault=%d err=%w", chain, youngestVaultID, err)
	}
	return addr, nil
}

func (p *JobWindowPlanner) btcNetParamsForChain(chain string) *chaincfg.Params {
	if p != nil && p.stateReader != nil {
		if data, exists, err := p.stateReader.Get(keys.KeyFrostVaultConfig(chain, 0)); err == nil && exists && len(data) > 0 {
			var cfg pb.FrostVaultConfig
			if proto.Unmarshal(data, &cfg) == nil && strings.TrimSpace(cfg.BtcNetwork) != "" {
				return btcNetParamsForChain(cfg.BtcNetwork)
			}
		}
	}
	return btcNetParamsForChain(chain)
}

func btcNetParamsForChain(chainOrNetwork string) *chaincfg.Params {
	lower := strings.ToLower(strings.TrimSpace(chainOrNetwork))
	switch lower {
	case "btc_testnet", "btc-testnet", "btctestnet", "testnet", "testnet3":
		return &chaincfg.TestNet3Params
	case "btc_regtest", "btc-regtest", "btcregtest", "regtest":
		return &chaincfg.RegressionNetParams
	case "btc_signet", "btc-signet", "btcsignet", "signet":
		return &chaincfg.SigNetParams
	default:
		return &chaincfg.MainNetParams
	}
}

func (p *JobWindowPlanner) selectYoungestActiveVaultID(chain string) (uint32, error) {
	vaultCfgKey := keys.KeyFrostVaultConfig(chain, 0)
	vaultCfgData, exists, err := p.stateReader.Get(vaultCfgKey)
	if err != nil {
		return 0, err
	}

	vaultCount := uint32(1)
	if exists && len(vaultCfgData) > 0 {
		var cfg pb.FrostVaultConfig
		if err := proto.Unmarshal(vaultCfgData, &cfg); err == nil && cfg.VaultCount > 0 {
			vaultCount = cfg.VaultCount
		}
	}

	found := false
	var youngestID uint32
	var bestEpoch uint64
	var bestActiveSince uint64

	for id := uint32(0); id < vaultCount; id++ {
		vaultStateKey := keys.KeyFrostVaultState(chain, id)
		vaultStateData, stateExists, e := p.stateReader.Get(vaultStateKey)
		if e != nil || !stateExists || len(vaultStateData) == 0 {
			continue
		}

		var state pb.FrostVaultState
		if e := proto.Unmarshal(vaultStateData, &state); e != nil {
			continue
		}
		if state.Status != "ACTIVE" {
			continue
		}

		if !found ||
			state.KeyEpoch > bestEpoch ||
			(state.KeyEpoch == bestEpoch && state.ActiveSinceHeight > bestActiveSince) ||
			(state.KeyEpoch == bestEpoch && state.ActiveSinceHeight == bestActiveSince && id > youngestID) {
			found = true
			youngestID = id
			bestEpoch = state.KeyEpoch
			bestActiveSince = state.ActiveSinceHeight
		}
	}

	if !found {
		return 0, fmt.Errorf("no ACTIVE vault found for chain=%s", chain)
	}
	return youngestID, nil
}
