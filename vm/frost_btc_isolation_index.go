package vm

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"dex/frost/chain"
	"dex/keys"
	"dex/pb"
	"dex/protocol"

	"google.golang.org/protobuf/proto"
)

                                                                               
                                                                               
                                  
func migrateBTCIsolationV3(sv StateView) error {
	key := keys.KeyFrostBtcUtxoAmountBuildState()
	if raw, found, err := sv.Get(key); err != nil && !isStateViewNotFound(err) {
		return err
	} else if found {
		var state pb.FrostBtcIsolationIndexState
		if proto.Unmarshal(raw, &state) != nil {
			return fmt.Errorf("invalid BTC amount index migration state")
		}
		return nil
	}
	raw, _ := proto.Marshal(&pb.FrostBtcIsolationIndexState{Cursor: keys.KeyFrostBtcUtxoAllPrefix(), JobCursor: keys.KeyFrostWithdrawJobPrefix()})
	sv.Set(key, raw)
	return nil
}

func ensureBTCUTXOOrigin(sv StateView, utxo *pb.FrostUtxo, legacy bool) error {
	if len(utxo.OriginId) == 32 {
		return nil
	}
	if len(utxo.OriginId) != 0 {
		return fmt.Errorf("invalid BTC UTXO origin")
	}
	cfg, err := loadFrostVaultConfig(sv, chain.ChainBTC)
	if err != nil {
		return err
	}
	network := networkChainIDFromView(sv)
	if network == 0 {
		return fmt.Errorf("BTC isolation requires bound DEX network")
	}
	utxo.OriginId = chain.BTCOriginID(network, cfg.BtcNetwork, utxo.Txid, utxo.Vout, legacy)
	utxo.OriginLegacy = legacy
	return nil
}

func btcPrimaryUTXOKey(key string) bool {
	parts := strings.Split(strings.TrimPrefix(key, keys.KeyFrostBtcUtxoAllPrefix()), "_")
	if len(parts) != 3 {
		return false
	}
	vault, err := strconv.ParseUint(parts[0], 10, 32)
	if err != nil {
		return false
	}
	vout, err := strconv.ParseUint(parts[2], 10, 32)
	if err != nil {
		return false
	}
	txid, err := hex.DecodeString(parts[1])
	return err == nil && len(txid) == 32 && strings.ToLower(parts[1]) == parts[1] &&
		key == keys.KeyFrostBtcUtxo(uint32(vault), parts[1], uint32(vout))
}

func processBTCIsolationAmountIndex(sv StateView) error {
	active, err := chain.BTCIsolationActive(sv.Get)
	if err != nil || !active {
		return err
	}
	key := keys.KeyFrostBtcUtxoAmountBuildState()
	raw, found, err := sv.Get(key)
	if err != nil {
		return err
	}
	var state pb.FrostBtcIsolationIndexState
	if !found || proto.Unmarshal(raw, &state) != nil {
		return fmt.Errorf("BTC amount index build state is missing or invalid")
	}
	if state.Done && state.JobsDone {
		return nil
	}
	if _, ok := sv.(orderedStateScannerAfter); !ok {
		return fmt.Errorf("BTC amount index requires ordered cursor scanning")
	}
	if !state.JobsDone {
		jobs, err := scanStateViewOrderedAfter(sv, keys.KeyFrostWithdrawJobPrefix(), state.JobCursor, protocol.BTCIsolationIndexPageSize, false)
		if err != nil {
			return err
		}
		for _, row := range jobs {
			var job pb.FrostWithdrawJobState
			if proto.Unmarshal(row.Value, &job) != nil || row.Key != keys.KeyFrostWithdrawJob(job.JobId) {
				return fmt.Errorf("invalid withdrawal job during recovery index build")
			}
			if chain.NormalizeChain(job.Chain) == chain.ChainBTC && job.Status == frostWithdrawJobStatusClaimed {
				sv.Set(keys.KeyFrostBtcWithdrawActiveChild(job.Asset, job.JobId), []byte(job.JobId))
			}
		}
		if len(jobs) > 0 {
			state.JobCursor = jobs[len(jobs)-1].Key
		}
		state.JobsDone = len(jobs) < protocol.BTCIsolationIndexPageSize
	}
	if state.Done {
		encoded, _ := proto.Marshal(&state)
		sv.Set(key, encoded)
		return nil
	}
	rows, err := scanStateViewOrderedAfter(sv, keys.KeyFrostBtcUtxoAllPrefix(), state.Cursor, protocol.BTCIsolationIndexPageSize, false)
	if err != nil {
		return err
	}
	utxos := make([]*pb.FrostUtxo, 0, len(rows))
	readKeys := make([]string, 0, 2*len(rows))
	for _, row := range rows {
		suffix := strings.TrimPrefix(row.Key, keys.KeyFrostBtcUtxoAllPrefix())
		                                                                     
		                                                                     
		if len(suffix) > 0 && (suffix[0] < '0' || suffix[0] > '9') {
			state.Done = true
			break
		}
		if !btcPrimaryUTXOKey(row.Key) {
			return fmt.Errorf("invalid primary BTC UTXO key during index build")
		}
		utxo := new(pb.FrostUtxo)
		if proto.Unmarshal(row.Value, utxo) != nil || row.Key != keys.KeyFrostBtcUtxo(utxo.VaultId, utxo.Txid, utxo.Vout) {
			return fmt.Errorf("invalid primary BTC UTXO during amount index build")
		}
		amount, err := strconv.ParseUint(utxo.Amount, 10, 64)
		if err != nil || amount == 0 {
			return fmt.Errorf("invalid BTC amount during index build")
		}
		utxo.Amount = strconv.FormatUint(amount, 10)
		utxos = append(utxos, utxo)
		readKeys = append(readKeys, keys.KeyFrostBtcLockedUtxo(utxo.VaultId, utxo.Txid, utxo.Vout), keys.KeyFrostBtcUtxoSeqRef(utxo.VaultId, utxo.Txid, utxo.Vout))
	}
	values, err := getManyFrostState(sv, readKeys)
	if err != nil {
		return err
	}
	cfg, err := loadFrostVaultConfig(sv, chain.ChainBTC)
	if err != nil {
		return err
	}
	network := networkChainIDFromView(sv)
	if network == 0 {
		return fmt.Errorf("BTC isolation requires bound DEX network")
	}
	for _, utxo := range utxos {
		if len(utxo.OriginId) == 0 {
			utxo.OriginId, utxo.OriginLegacy = chain.BTCOriginID(network, cfg.BtcNetwork, utxo.Txid, utxo.Vout, true), true
		} else if len(utxo.OriginId) != 32 {
			return fmt.Errorf("invalid BTC UTXO origin")
		}
		data, err := proto.Marshal(utxo)
		if err != nil {
			return err
		}
		sv.Set(keys.KeyFrostBtcUtxo(utxo.VaultId, utxo.Txid, utxo.Vout), data)
		if len(values[keys.KeyFrostBtcLockedUtxo(utxo.VaultId, utxo.Txid, utxo.Vout)]) != 0 {
			continue
		}
		seq, err := strconv.ParseUint(string(values[keys.KeyFrostBtcUtxoSeqRef(utxo.VaultId, utxo.Txid, utxo.Vout)]), 10, 64)
		if err != nil || seq == 0 {
			return fmt.Errorf("BTC spendable sequence missing during index build")
		}
		sv.Set(keys.KeyFrostBtcUtxoFIFOIndex(utxo.VaultId, seq), data)
		if err := setBTCSpendableUTXOIndexForPolicy(sv, utxo.VaultId, seq, utxo, data, true); err != nil {
			return err
		}
	}
	if len(rows) < protocol.BTCIsolationIndexPageSize {
		state.Done = true
	}
	if len(rows) > 0 {
		state.Cursor = rows[len(rows)-1].Key
	}
	encoded, _ := proto.Marshal(&state)
	sv.Set(key, encoded)
	return nil
}
