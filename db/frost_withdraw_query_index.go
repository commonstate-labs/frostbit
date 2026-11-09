package db

import (
	"fmt"
	"strings"

	"dex/keys"
	"dex/pb"

	"github.com/cockroachdb/pebble"
	"google.golang.org/protobuf/proto"
)

                                                                              
                                                                                  
                                                                                  
func (manager *Manager) RebuildActiveFrostWithdrawQueryIndex() error {
	manager.frostWithdrawQueryIndexMu.Lock()
	defer manager.frostWithdrawQueryIndexMu.Unlock()
	return manager.rebuildActiveFrostWithdrawQueryIndexLocked()
}

func (manager *Manager) rebuildActiveFrostWithdrawQueryIndexLocked() (err error) {
	defer func() { manager.frostWithdrawQueryIndexErr = err }()
	                                                                              
	if err = manager.ForceFlush(); err != nil {
		return err
	}
	manager.mu.RLock()
	stateStore := manager.stateDB
	manager.mu.RUnlock()
	if stateStore == nil {
		return fmt.Errorf("active withdrawal index requires StateDB")
	}
	queryDB, err := manager.dbForStorageClass(keys.StorageQuery)
	if err != nil {
		return err
	}
	batch := queryDB.NewBatch()
	defer func() { _ = batch.Close() }()
	flush := func() error {
		if batch.Count() == 0 {
			return nil
		}
		if err := batch.Commit(pebble.Sync); err != nil {
			return err
		}
		if err := batch.Close(); err != nil {
			return err
		}
		batch = queryDB.NewBatch()
		return nil
	}
	flushIfFull := func() error {
		if batch.Count() >= 1000 || len(batch.Repr()) >= 1<<20 {
			return flush()
		}
		return nil
	}
	prefix := []byte(keys.KeyFrostWithdrawQueryPrefix())
	iter, err := queryDB.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: prefixUpperBound(prefix)})
	if err != nil {
		return err
	}
	defer iter.Close()
	for iter.First(); iter.Valid(); iter.Next() {
		if strings.HasSuffix(string(iter.Key()), "|kind:active") {
			if err := batch.Delete(iter.Key(), pebble.NoSync); err != nil {
				return err
			}
			if err := flushIfFull(); err != nil {
				return err
			}
		}
	}
	if err := iter.Error(); err != nil {
		return err
	}
	                                                                        
	                                                                        
	cursor := ""
	for {
		rows, err := stateStore.IterateLatestByPrefixLimitAfter(keys.KeyFrostWithdrawFIFOIndexPrefix(), cursor, 256, false)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			cursor = row.Key
			id := string(row.Value)
			if id == "" || strings.TrimSpace(id) != id {
				return fmt.Errorf("invalid withdrawal FIFO reference %q", row.Key)
			}
			raw, exists, err := stateStore.Get(keys.KeyFrostWithdraw(id))
			if err != nil {
				return err
			}
			var state pb.FrostWithdrawState
			if !exists || len(raw) == 0 || proto.Unmarshal(raw, &state) != nil || state.WithdrawId != id ||
				keys.KeyFrostWithdrawFIFOIndex(state.Chain, state.Asset, state.Seq) != row.Key {
				return fmt.Errorf("withdrawal FIFO/source mismatch for %q", row.Key)
			}
			if state.Status == "SIGNED" || state.Status == "CANCELLED" {
				continue
			}
			if state.Status != "QUEUED" && state.Status != "SIGNING" {
				return fmt.Errorf("invalid active withdrawal status for %q", id)
			}
			for _, indexKey := range []string{
				keys.KeyFrostWithdrawQueryGlobalState(false, state.RequestHeight, state.Seq, id),
				keys.KeyFrostWithdrawQueryChainState(state.Chain, false, state.RequestHeight, state.Seq, id),
				keys.KeyFrostWithdrawQueryChainVaultState(state.Chain, state.VaultId, false, state.RequestHeight, state.Seq, id),
			} {
				if err := batch.Set([]byte(indexKey), []byte(id), pebble.NoSync); err != nil {
					return err
				}
			}
			if err := flushIfFull(); err != nil {
				return err
			}
		}
	}
	return flush()
}

func (manager *Manager) frostWithdrawQueryReadBarrier(prefix string) (func(), error) {
	if !strings.HasPrefix(prefix, keys.KeyFrostWithdrawQueryPrefix()) {
		return func() {}, nil
	}
	manager.frostWithdrawQueryIndexMu.RLock()
	if manager.frostWithdrawQueryIndexErr != nil {
		err := fmt.Errorf("active withdrawal query index unavailable: %w", manager.frostWithdrawQueryIndexErr)
		manager.frostWithdrawQueryIndexMu.RUnlock()
		return nil, err
	}
	return manager.frostWithdrawQueryIndexMu.RUnlock, nil
}
