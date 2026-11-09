package db

import (
	"dex/blocktrace"
	"dex/keys"
	"dex/logs"
	statedb "dex/stateDB"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/cockroachdb/pebble"
)

const stateSyncSlowThreshold = 300 * time.Millisecond

const (
	stateSyncParallelBuildMinUpdates = 4096
	stateSyncParallelBuildMaxWork    = 8
)

type stateWriteOp interface {
	GetKey() string
	GetValue() []byte
	IsDel() bool
}

type stateWriteOpWithOldValue interface {
	OldValueKnown() bool
	GetOldValue() []byte
	OldValueExists() bool
}

type stateWriteOpWithAuthoritativeOldValue interface {
	OldValueIsAuthoritative() bool
}

                                                                            
                                                                  
func (manager *Manager) SyncStateUpdates(height uint64, updates []interface{}) (int, error) {
	return manager.syncStateUpdates(height, "", updates, false)
}

                                                                              
                                                                                
                                                                           
func (manager *Manager) SyncFinalizedStateUpdates(height uint64, updates []interface{}, forceCheckpoint bool) (int, error) {
	return manager.syncStateUpdates(height, "", updates, forceCheckpoint)
}

                                                                              
                                                                             
                                               
func (manager *Manager) SyncFinalizedStateUpdatesForBlock(height uint64, blockID string, updates []interface{}, forceCheckpoint bool) (int, error) {
	if height == 0 || blockID == "" {
		return 0, fmt.Errorf("finalized state block identity is required")
	}
	return manager.syncStateUpdates(height, blockID, updates, forceCheckpoint)
}

func (manager *Manager) syncStateUpdates(height uint64, blockID string, updates []interface{}, forceCheckpoint bool) (accepted int, err error) {
	startAt := time.Now()
	var (
		buildKVsDur  time.Duration
		applyDur     time.Duration
		buildWorkers int
	)
	defer func() {
		blocktrace.Record(height, "", "DB.syncStateUpdates", time.Since(startAt),
			blocktrace.F("updates", len(updates)), blocktrace.F("stateful", accepted),
			blocktrace.F("build", buildKVsDur), blocktrace.F("buildWorkers", buildWorkers), blocktrace.F("apply", applyDur),
			blocktrace.F("forceCheckpoint", forceCheckpoint), blocktrace.F("err", err),
		)
	}()

	manager.mu.RLock()
	stateStore := manager.stateDB
	manager.mu.RUnlock()
	if stateStore == nil {
		return 0, nil
	}

	buildAt := time.Now()
	kvs, buildWorkers, err := buildStateSyncKVs(updates)
	if err != nil {
		buildKVsDur = time.Since(buildAt)
		return 0, err
	}
	buildKVsDur = time.Since(buildAt)

	if len(kvs) == 0 && !forceCheckpoint && blockID == "" {
		return 0, nil
	}

	applyAt := time.Now()
	err = stateStore.ApplyAccountUpdateWithOptions(height, statedb.ApplyOptions{ForceCheckpoint: forceCheckpoint, BlockID: blockID}, kvs...)
	applyDur = time.Since(applyAt)
	if err != nil {
		return 0, err
	}

	totalDur := time.Since(startAt)
	cfg := stateStore.Config()
	checkpointEvery := cfg.CheckpointEvery
	checkpointBoundary := (checkpointEvery > 0 && height > 0 && height%checkpointEvery == 0) || forceCheckpoint
	if totalDur >= stateSyncSlowThreshold || (checkpointBoundary && totalDur >= 100*time.Millisecond) {
		logs.Warn(
			"[StateSync][Slow] height=%d updates=%d stateful=%d total=%s build=%s apply=%s checkpointEvery=%d checkpointBoundary=%t forceCheckpoint=%t",
			height, len(updates), len(kvs), totalDur, buildKVsDur, applyDur, checkpointEvery, checkpointBoundary, forceCheckpoint,
		)
	}
	return len(kvs), nil
}

                                                                            
                                                                             
                                           
func (manager *Manager) FinalizedStateBlockID(height uint64) (string, bool, error) {
	manager.mu.RLock()
	stateStore := manager.stateDB
	manager.mu.RUnlock()
	if stateStore == nil {
		return "", false, nil
	}
	return stateStore.AppliedBlockID(height)
}

                                                                            
                                                                        
                                                                
func (manager *Manager) GetFinalizedStateHeight() (uint64, bool, error) {
	manager.mu.RLock()
	stateStore := manager.stateDB
	manager.mu.RUnlock()
	if stateStore == nil {
		return 0, false, nil
	}
	return stateStore.LastAppliedHeight()
}

                                                                          
                                                                               
                                                             
func (manager *Manager) RecoverFinalizedStateCommit(height uint64, blockID string, forceCheckpoint bool) error {
	committedID, exists, err := manager.FinalizedStateBlockID(height)
	if err != nil {
		return err
	}
	if !exists || committedID != blockID {
		return fmt.Errorf("cannot recover finalized height %d: StateDB block=%q want=%q", height, committedID, blockID)
	}

	manager.mu.RLock()
	stateStore := manager.stateDB
	runtimeDB := manager.runtimeDB
	manager.mu.RUnlock()
	if forceCheckpoint {
		if stateStore == nil {
			return fmt.Errorf("cannot recover checkpoint: stateDB is not initialized")
		}
		latest, latestErr := stateStore.LatestCheckpointHeight()
		if latestErr != nil {
			return latestErr
		}
		if latest < height {
			if err := stateStore.RecreateCheckpoint(height); err != nil {
				return fmt.Errorf("recover finalized checkpoint %d: %w", height, err)
			}
		}
	}
	if runtimeDB == nil {
		return fmt.Errorf("cannot recover finalized marker: runtime database is not initialized")
	}
	return runtimeDB.Set([]byte(keys.KeyVMCommitHeight(height)), []byte(blockID), pebble.Sync)
}

func buildStateSyncKVs(updates []interface{}) ([]statedb.KVUpdate, int, error) {
	workers := stateSyncBuildWorkerCount(len(updates))
	if workers <= 1 {
		kvs := make([]statedb.KVUpdate, 0, len(updates))
		for _, u := range updates {
			kv, keep, err := stateSyncKVFromUpdate(u)
			if err != nil {
				return nil, 1, err
			}
			if keep {
				kvs = append(kvs, kv)
			}
		}
		return kvs, 1, nil
	}
	chunk := (len(updates) + workers - 1) / workers
	type partResult struct {
		kvs []statedb.KVUpdate
		err error
	}
	parts := make([]partResult, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		start := i * chunk
		if start >= len(updates) {
			parts = parts[:i]
			break
		}
		end := start + chunk
		if end > len(updates) {
			end = len(updates)
		}
		wg.Add(1)
		go func(idx, start, end int) {
			defer wg.Done()
			kvs := make([]statedb.KVUpdate, 0, end-start)
			for _, u := range updates[start:end] {
				kv, keep, err := stateSyncKVFromUpdate(u)
				if err != nil {
					parts[idx] = partResult{err: err}
					return
				}
				if keep {
					kvs = append(kvs, kv)
				}
			}
			parts[idx] = partResult{kvs: kvs}
		}(i, start, end)
	}
	wg.Wait()
	total := 0
	for _, part := range parts {
		if part.err != nil {
			return nil, len(parts), part.err
		}
		total += len(part.kvs)
	}
	kvs := make([]statedb.KVUpdate, 0, total)
	for _, part := range parts {
		kvs = append(kvs, part.kvs...)
	}
	return kvs, len(parts), nil
}

func stateSyncBuildWorkerCount(items int) int {
	if items < stateSyncParallelBuildMinUpdates {
		return 1
	}
	workers := runtime.GOMAXPROCS(0)
	if workers > stateSyncParallelBuildMaxWork {
		workers = stateSyncParallelBuildMaxWork
	}
	if workers < 2 || items < workers*2 {
		return 1
	}
	return workers
}

func stateSyncKVFromUpdate(u interface{}) (statedb.KVUpdate, bool, error) {
	op, ok := u.(stateWriteOp)
	if !ok || op == nil {
		return statedb.KVUpdate{}, false, nil
	}
	key := op.GetKey()
	if key == "" {
		return statedb.KVUpdate{}, false, nil
	}
	switch keys.CategorizeKey(key) {
	case keys.CategoryState:
	case keys.CategoryKV:
		return statedb.KVUpdate{}, false, nil
	default:
		return statedb.KVUpdate{}, false, fmt.Errorf("unregistered VM write key prefix: %q", key)
	}
	kv := statedb.KVUpdate{
		Key:     key,
		Value:   cloneStateSyncBytes(op.GetValue()),
		Deleted: op.IsDel(),
	}
	if oldOp, ok := u.(stateWriteOpWithOldValue); ok && oldOp.OldValueKnown() {
		kv.HasOldValue = true
		kv.OldExists = oldOp.OldValueExists()
		if oldOp.OldValueExists() {
			kv.OldValue = cloneStateSyncBytes(oldOp.GetOldValue())
		}
		if authOp, ok := u.(stateWriteOpWithAuthoritativeOldValue); ok && authOp.OldValueIsAuthoritative() {
			kv.OldValueAuthoritative = true
		}
	}
	return kv, true, nil
}

func cloneStateSyncBytes(v []byte) []byte {
	if v == nil {
		return nil
	}
	out := make([]byte, len(v))
	copy(out, v)
	return out
}
