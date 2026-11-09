package db

import (
	"dex/keys"
	"dex/pb"
	"dex/witness"
	"fmt"

	"github.com/cockroachdb/pebble"
	"google.golang.org/protobuf/proto"
)

                                                                           
                                                                          
func (manager *Manager) RebuildWitnessAssignmentIndex() error {
	return manager.RebuildActiveRechargeQueryIndex()
}

                                                                           
func (manager *Manager) rebuildWitnessAssignmentIndexLocked() error {
	manager.mu.RLock()
	stateStore := manager.stateDB
	manager.mu.RUnlock()
	if stateStore == nil {
		return fmt.Errorf("witness assignment index requires StateDB")
	}
	queryDB, err := manager.dbForStorageClass(keys.StorageQuery)
	if err != nil {
		return err
	}
	batch := queryDB.NewBatch()
	defer func() { _ = batch.Close() }()
	prefix := []byte(keys.KeyWitnessAssignedPrefix(""))
	if err := batch.DeleteRange(prefix, prefixUpperBound(prefix), pebble.NoSync); err != nil {
		return err
	}
	flush := func() error {
		if err := batch.Commit(pebble.Sync); err != nil {
			return err
		}
		if err := batch.Close(); err != nil {
			return err
		}
		batch = queryDB.NewBatch()
		return nil
	}
	err = stateStore.IterateLatestByPrefix(keys.KeyRechargeRequestPrefix(), func(key string, value []byte) error {
		var req pb.RechargeRequest
		if err := proto.Unmarshal(value, &req); err != nil {
			return fmt.Errorf("decode assignment source %s: %w", key, err)
		}
		if req.RequestId == "" || key != keys.KeyRechargeRequest(req.RequestId) {
			return fmt.Errorf("assignment source identity mismatch: %s", key)
		}
		for _, indexKey := range witness.RequestWitnessAssignmentKeys(&req) {
			if err := batch.Set([]byte(indexKey), []byte(req.RequestId), pebble.NoSync); err != nil {
				return err
			}
		}
		if batch.Count() >= 1000 || len(batch.Repr()) >= 1<<20 {
			return flush()
		}
		return nil
	})
	if err != nil {
		return err
	}
	return flush()
}
