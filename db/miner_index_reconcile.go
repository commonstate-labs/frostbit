package db

import (
	"dex/keys"
	"dex/pb"
	"fmt"
	"sort"
	"strings"

	"github.com/cockroachdb/pebble"
)

                                                                      
                                                                             
                                                                  
type MinerIndexSource interface {
	GetActiveMiners() []string
	GetMinerIndex(address string) (uint32, bool)
}

type minerIndexEntry struct {
	index   uint64
	address string
}

                                                                              
                                                                              
                                                                              
                                                                         
                                                                 
  
                                                                           
                                                                              
                                                                              
func (manager *Manager) ReconcileMinerIndexFromLiveness(source MinerIndexSource) (int, error) {
	if manager == nil {
		return 0, fmt.Errorf("reconcile miner index: db manager is nil")
	}
	if source == nil {
		return 0, fmt.Errorf("reconcile miner index: liveness source is nil")
	}
	if manager.IndexMgr == nil {
		return 0, fmt.Errorf("reconcile miner index: index manager is nil")
	}

	addresses := source.GetActiveMiners()
	entries := make([]minerIndexEntry, 0, len(addresses))
	seenAddresses := make(map[string]struct{}, len(addresses))
	seenIndices := make(map[uint64]string, len(addresses))
	for _, address := range addresses {
		if strings.TrimSpace(address) == "" {
			return 0, fmt.Errorf("reconcile miner index: liveness returned an empty address")
		}
		if _, duplicate := seenAddresses[address]; duplicate {
			return 0, fmt.Errorf("reconcile miner index: duplicate liveness address %q", address)
		}
		seenAddresses[address] = struct{}{}

		index, ok := source.GetMinerIndex(address)
		if !ok {
			return 0, fmt.Errorf("reconcile miner index: liveness index missing for %s", address)
		}
		idx := uint64(index)
		if owner, duplicate := seenIndices[idx]; duplicate {
			return 0, fmt.Errorf("reconcile miner index: liveness index %d belongs to both %s and %s", idx, owner, address)
		}
		seenIndices[idx] = address
		entries = append(entries, minerIndexEntry{index: idx, address: address})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].index < entries[j].index })
	manager.minerIndexMu.Lock()
	defer manager.minerIndexMu.Unlock()
	                                                                             
	                                                     
	if err := manager.ForceFlushHot(); err != nil {
		return 0, fmt.Errorf("reconcile miner index: flush prior runtime writes: %w", err)
	}

	accountKeys := make([]string, len(entries))
	for i, entry := range entries {
		accountKeys[i] = keys.KeyAccount(entry.address)
	}
	accounts, err := manager.readStateKeys(accountKeys)
	if err != nil {
		return 0, fmt.Errorf("reconcile miner index: read authoritative accounts: %w", err)
	}
	for i, entry := range entries {
		accountKey := accountKeys[i]
		raw, exists := accounts[accountKey]
		if !exists || len(raw) == 0 {
			return 0, fmt.Errorf("reconcile miner index: account %s is missing from StateDB", entry.address)
		}
		var account pb.Account
		if err := ProtoUnmarshal(raw, &account); err != nil {
			return 0, fmt.Errorf("reconcile miner index: decode account %s: %w", entry.address, err)
		}
		if account.Address != entry.address {
			return 0, fmt.Errorf("reconcile miner index: account address %q does not match liveness address %q", account.Address, entry.address)
		}
		if !account.IsMiner {
			return 0, fmt.Errorf("reconcile miner index: liveness member %s is not a miner in StateDB", entry.address)
		}
		if account.Index != entry.index {
			return 0, fmt.Errorf("reconcile miner index: account %s index=%d does not match liveness index=%d", entry.address, account.Index, entry.index)
		}
	}

	runtimeDB, err := manager.dbForStorageClass(keys.StorageRuntime)
	if err != nil {
		return 0, fmt.Errorf("reconcile miner index: open runtime database: %w", err)
	}
	prefix := []byte(keys.NameOfKeyIndexToAccount())
	iter, err := runtimeDB.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: prefixUpperBound(prefix),
	})
	if err != nil {
		return 0, fmt.Errorf("reconcile miner index: enumerate existing mappings: %w", err)
	}
	batch := runtimeDB.NewBatch()
	defer batch.Close()
	for iter.SeekGE(prefix); iter.Valid(); iter.Next() {
		key := append([]byte(nil), iter.Key()...)
		if err := batch.Delete(key, nil); err != nil {
			_ = iter.Close()
			return 0, fmt.Errorf("reconcile miner index: delete stale mapping %q: %w", key, err)
		}
	}
	if err := iter.Error(); err != nil {
		_ = iter.Close()
		return 0, fmt.Errorf("reconcile miner index: enumerate existing mappings: %w", err)
	}
	if err := iter.Close(); err != nil {
		return 0, fmt.Errorf("reconcile miner index: close mapping iterator: %w", err)
	}
	for _, entry := range entries {
		if err := batch.Set(
			[]byte(keys.KeyIndexToAccount(entry.index)),
			[]byte(keys.KeyAccount(entry.address)),
			nil,
		); err != nil {
			return 0, fmt.Errorf("reconcile miner index: set mapping %d -> %s: %w", entry.index, entry.address, err)
		}
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return 0, fmt.Errorf("reconcile miner index: commit runtime mappings: %w", err)
	}

	indices := make([]uint32, len(entries))
	for i, entry := range entries {
		indices[i] = uint32(entry.index)
	}
	                                                                             
	                                                                          
	manager.IndexMgr.ReplaceIndices(indices)
	manager.InvalidateMinerCache()
	manager.Logger.Info("[MinerIndexManager] reconciled %d hot miner mappings from liveness state", len(entries))
	return len(entries), nil
}
