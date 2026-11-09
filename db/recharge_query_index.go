package db

import (
	"fmt"
	"strings"

	"dex/keys"
	"dex/pb"

	"github.com/cockroachdb/pebble"
	"google.golang.org/protobuf/proto"
)

                                                                              
                                                                               
                                                                                
                                                                               
                                                                             
func (manager *Manager) RebuildActiveRechargeQueryIndex() error {
	manager.rechargeQueryIndexMu.Lock()
	defer manager.rechargeQueryIndexMu.Unlock()
	return manager.rebuildActiveRechargeQueryIndexLocked()
}

func (manager *Manager) rebuildActiveRechargeQueryIndexLocked() (err error) {
	defer func() { manager.rechargeQueryIndexErr = err }()
	                                                                         
	if err = manager.ForceFlush(); err != nil {
		return err
	}
	manager.mu.RLock()
	stateStore := manager.stateDB
	manager.mu.RUnlock()
	if stateStore == nil {
		return fmt.Errorf("active recharge index requires StateDB")
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
	                                                                           
	                                                                             
	prefix := []byte(keys.KeyRechargeQueryRecentPrefix())
	iter, err := queryDB.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: prefixUpperBound(prefix)})
	if err != nil {
		return err
	}
	defer iter.Close()
	for iter.First(); iter.Valid(); iter.Next() {
		head, _, hasHeight := strings.Cut(string(iter.Key()), "|height:")
		if hasHeight && strings.HasSuffix(head, "|kind:active") {
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
	err = stateStore.IterateLatestByPrefix(keys.KeyRechargeRequestPrefix(), func(key string, value []byte) error {
		var req pb.RechargeRequest
		if err := proto.Unmarshal(value, &req); err != nil {
			return fmt.Errorf("decode active recharge source %q: %w", key, err)
		}
		if req.RequestId == "" || keys.KeyRechargeRequest(req.RequestId) != key {
			return fmt.Errorf("recharge request identity mismatch for %q", key)
		}
		if req.Status == pb.RechargeRequestStatus_RECHARGE_FINALIZED || req.Status == pb.RechargeRequestStatus_RECHARGE_REJECTED {
			return nil
		}
		                                                                           
		                                                                       
		height := req.CreateHeight
		for _, indexKey := range []string{
			keys.KeyRechargeQueryRecentGlobal(false, height, req.RequestId),
			keys.KeyRechargeQueryRecentChain(req.NativeChain, false, height, req.RequestId),
			keys.KeyRechargeQueryRecentVault(req.VaultId, false, height, req.RequestId),
			keys.KeyRechargeQueryRecentChainVault(req.NativeChain, req.VaultId, false, height, req.RequestId),
		} {
			if err := batch.Set([]byte(indexKey), []byte(key), pebble.NoSync); err != nil {
				return err
			}
		}
		return flushIfFull()
	})
	if err != nil {
		return err
	}
	if err := flush(); err != nil {
		return err
	}
	return manager.rebuildWitnessAssignmentIndexLocked()
}

                                                                             
                                                                               
func (manager *Manager) rechargeQueryReadBarrier(prefix string) (func(), error) {
	if !strings.HasPrefix(prefix, keys.KeyRechargeQueryRecentPrefix()) && !strings.HasPrefix(prefix, keys.KeyWitnessAssignedPrefix("")) {
		return func() {}, nil
	}
	manager.rechargeQueryIndexMu.RLock()
	if manager.rechargeQueryIndexErr != nil {
		err := fmt.Errorf("active recharge query index unavailable: %w", manager.rechargeQueryIndexErr)
		manager.rechargeQueryIndexMu.RUnlock()
		return nil, err
	}
	return manager.rechargeQueryIndexMu.RUnlock, nil
}
