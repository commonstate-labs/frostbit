package statedb

import (
	"dex/blocktrace"
	"dex/keys"
	"dex/logs"
	"encoding/binary"
	"fmt"
	"runtime"
	"sort"
	"sync"
	"time"
)

const (
	stateDBApplyProbeWarnThreshold      = 300 * time.Millisecond
	stateDBApplyCheckpointInfoThreshold = 80 * time.Millisecond
	stateDBApplyLockWaitWarnThreshold   = 20 * time.Millisecond
	stateDBApplyCanonicalMinUpdates     = 4096
	stateDBApplyCanonicalMaxWork        = 8
)

type ApplyOptions struct {
	ForceCheckpoint bool
	                                                                           
	                                                                       
	                                                                      
	BlockID string
}

                                                                      
                                                                         
                                                              
func (s *DB) ApplyAccountUpdate(height uint64, kvs ...KVUpdate) error {
	return s.ApplyAccountUpdateWithOptions(height, ApplyOptions{}, kvs...)
}

                                                                                
                                                                             
func (s *DB) ApplyAccountUpdateWithOptions(height uint64, opts ApplyOptions, kvs ...KVUpdate) (err error) {
	startAt := time.Now()
	inputUpdateCount := len(kvs)
	checkpointBoundary := height > 0 && (opts.ForceCheckpoint || checkpointOf(height, s.checkpointEvery()))
	var (
		durLockWait      time.Duration
		durCanonical     time.Duration
		durApplyPrep     time.Duration
		durWriteBatch    time.Duration
		durCheckpoint    time.Duration
		statefulCount    int
		canonicalWorkers int
		applyPrepWorkers int
		oldMissFallbacks int
		oldMissSample    string
	)
	defer func() {
		total := time.Since(startAt)
		blocktrace.Record(height, "", "StateDB.ApplyAccountUpdate", total,
			blocktrace.F("updates", inputUpdateCount), blocktrace.F("stateful", statefulCount),
			blocktrace.F("canonical", durCanonical), blocktrace.F("canonicalWorkers", canonicalWorkers),
			blocktrace.F("applyPrep", durApplyPrep), blocktrace.F("applyPrepWorkers", applyPrepWorkers),
			blocktrace.F("lockWait", durLockWait),
			blocktrace.F("writeBatch", durWriteBatch), blocktrace.F("checkpoint", durCheckpoint),
			blocktrace.F("boundary", checkpointBoundary), blocktrace.F("oldMissFallbacks", oldMissFallbacks),
			blocktrace.F("err", err),
		)
		if err == nil &&
			total < stateDBApplyCheckpointInfoThreshold &&
			durLockWait < stateDBApplyLockWaitWarnThreshold {
			return
		}
		if err != nil ||
			total >= stateDBApplyProbeWarnThreshold ||
			durLockWait >= stateDBApplyLockWaitWarnThreshold ||
			(checkpointBoundary && total >= stateDBApplyCheckpointInfoThreshold) {
			logs.Warn(
				"[StateDB][Apply] height=%d updates=%d stateful=%d total=%s canonical=%s applyPrep=%s lockWait=%s writeBatch=%s checkpoint=%s boundary=%t err=%v",
				height, inputUpdateCount, statefulCount, total, durCanonical, durApplyPrep, durLockWait, durWriteBatch, durCheckpoint, checkpointBoundary, err,
			)
		}
	}()

	canonicalAt := time.Now()
	kvs, statefulCount, canonicalWorkers, err = canonicalStateUpdates(kvs)
	durCanonical = time.Since(canonicalAt)
	if err != nil {
		return err
	}
	applyPrepAt := time.Now()
	prepared, applyPrepWorkers := prepareStateApplyItems(kvs, s.conf.ShardHexWidth)
	durApplyPrep = time.Since(applyPrepAt)

	lockWaitAt := time.Now()
	s.mu.Lock()
	durLockWait = time.Since(lockWaitAt)
	locked := true
	defer func() {
		if locked {
			s.mu.Unlock()
		}
	}()

	writeBatchAt := time.Now()
	touchedShards := map[string]struct{}{}
	type fingerprintMutation struct {
		key, shard string
		oldLeaf    preparedStateLeaf
		newLeaf    preparedStateLeaf
		deleted    bool
	}
	fingerprintMutations := make([]fingerprintMutation, 0, len(prepared))
	err = s.store.Update(func(tx kvWriter) error {
		for _, item := range prepared {
			kv := item.kv
			touchedShards[item.shard] = struct{}{}

			var oldVal []byte
			oldLeaf := item.oldLeaf
			if kv.HasOldValue {
				if kv.OldExists {
					oldLeaf.exists = true
				} else if kv.OldValueAuthoritative {
					oldVal = nil
				} else {
					var getErr error
					oldVal, getErr = tx.Get(item.stateKey)
					if getErr != nil && !s.store.IsNotFound(getErr) {
						return getErr
					}
					if s.store.IsNotFound(getErr) {
						oldVal = nil
					} else {
						oldLeaf = makePreparedStateLeaf(kv.Key, oldVal)
						oldMissFallbacks++
						if oldMissSample == "" {
							oldMissSample = kv.Key
						}
					}
				}
			} else {
				var getErr error
				oldVal, getErr = tx.Get(item.stateKey)
				if getErr != nil && !s.store.IsNotFound(getErr) {
					return getErr
				}
				if s.store.IsNotFound(getErr) {
					oldVal = nil
				} else {
					oldLeaf = makePreparedStateLeaf(kv.Key, oldVal)
				}
			}

			if kv.Deleted {
				if err := tx.Delete(item.stateKey); err != nil && !s.store.IsNotFound(err) {
					return err
				}
				fingerprintMutations = append(fingerprintMutations, fingerprintMutation{
					key: kv.Key, shard: item.shard, oldLeaf: oldLeaf, deleted: true,
				})
				continue
			}
			if err := tx.Set(item.stateKey, kv.Value); err != nil {
				return err
			}
			fingerprintMutations = append(fingerprintMutations, fingerprintMutation{
				key: kv.Key, shard: item.shard, oldLeaf: oldLeaf, newLeaf: item.newLeaf,
			})
		}

		if height > 0 {
			hb := make([]byte, 8)
			binary.BigEndian.PutUint64(hb, height)

			if err := tx.Set(kMetaLastAppliedHeight(), hb); err != nil {
				return err
			}
			if opts.BlockID != "" {
				existing, getErr := tx.Get(kMetaAppliedBlock(height))
				switch {
				case getErr == nil && string(existing) != opts.BlockID:
					return fmt.Errorf("state height %d already applied with different block %q", height, string(existing))
				case getErr != nil && !s.store.IsNotFound(getErr):
					return getErr
				}
				if err := tx.Set(kMetaAppliedBlock(height), []byte(opts.BlockID)); err != nil {
					return err
				}
			}
		}
		return nil
	})
	durWriteBatch = time.Since(writeBatchAt)
	if err != nil {
		return err
	}
	                                                                      
	                                                                           
	                                                  
	for _, mutation := range fingerprintMutations {
		if mutation.deleted {
			s.updateFingerprintForDeletePrepared(mutation.key, mutation.shard, mutation.oldLeaf)
		} else {
			s.updateFingerprintForPutPrepared(mutation.key, mutation.shard, mutation.oldLeaf, mutation.newLeaf)
		}
	}
	if oldMissFallbacks > 0 {
		logs.Warn("[StateDB][Apply] height=%d old-value-missing fallback used for %d keys (sample=%s)", height, oldMissFallbacks, oldMissSample)
	}
	_ = touchedShards

	if checkpointBoundary {
		checkpointAt := time.Now()
		err = s.createCheckpointLocked(height, false)
		durCheckpoint = time.Since(checkpointAt)
		if err != nil {
			return err
		}
	}
	s.mu.Unlock()
	locked = false
	return nil
}

func canonicalStateUpdates(kvs []KVUpdate) ([]KVUpdate, int, int, error) {
	if len(kvs) == 0 {
		return nil, 0, 1, nil
	}
	prepared, workers, err := prepareCanonicalStateUpdates(kvs)
	if err != nil {
		return nil, 0, workers, err
	}
	byKey := make(map[string]KVUpdate, len(prepared))
	for _, kv := range prepared {
		if prev, ok := byKey[kv.Key]; ok {
			if !prev.HasOldValue {
				kv.HasOldValue = false
				kv.OldExists = false
				kv.OldValue = nil
				kv.OldValueAuthoritative = false
			} else {
				kv.HasOldValue = true
				kv.OldExists = prev.OldExists
				kv.OldValue = cloneUpdateBytes(prev.OldValue)
				kv.OldValueAuthoritative = prev.OldValueAuthoritative
			}
		}
		byKey[kv.Key] = kv
	}
	keysSorted := make([]string, 0, len(byKey))
	for key := range byKey {
		keysSorted = append(keysSorted, key)
	}
	sort.Strings(keysSorted)
	out := make([]KVUpdate, 0, len(keysSorted))
	for _, key := range keysSorted {
		out = append(out, byKey[key])
	}
	return out, len(out), workers, nil
}

func prepareCanonicalStateUpdates(kvs []KVUpdate) ([]KVUpdate, int, error) {
	workers := stateDBApplyCanonicalWorkerCount(len(kvs))
	if workers <= 1 {
		out := make([]KVUpdate, 0, len(kvs))
		for _, kv := range kvs {
			prepared, keep, err := prepareCanonicalStateUpdate(kv)
			if err != nil {
				return nil, 1, err
			}
			if keep {
				out = append(out, prepared)
			}
		}
		return out, 1, nil
	}
	chunk := (len(kvs) + workers - 1) / workers
	type partResult struct {
		kvs []KVUpdate
		err error
	}
	parts := make([]partResult, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		start := i * chunk
		if start >= len(kvs) {
			parts = parts[:i]
			break
		}
		end := start + chunk
		if end > len(kvs) {
			end = len(kvs)
		}
		wg.Add(1)
		go func(idx, start, end int) {
			defer wg.Done()
			out := make([]KVUpdate, 0, end-start)
			for _, kv := range kvs[start:end] {
				prepared, keep, err := prepareCanonicalStateUpdate(kv)
				if err != nil {
					parts[idx] = partResult{err: err}
					return
				}
				if keep {
					out = append(out, prepared)
				}
			}
			parts[idx] = partResult{kvs: out}
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
	out := make([]KVUpdate, 0, total)
	for _, part := range parts {
		out = append(out, part.kvs...)
	}
	return out, len(parts), nil
}

func prepareCanonicalStateUpdate(kv KVUpdate) (KVUpdate, bool, error) {
	switch keys.CategorizeKey(kv.Key) {
	case keys.CategoryState:
	case keys.CategoryKV:
		return KVUpdate{}, false, nil
	default:
		return KVUpdate{}, false, fmt.Errorf("unregistered StateDB update key prefix: %q", kv.Key)
	}
	kv.OldValue = cloneUpdateBytes(kv.OldValue)
	kv.Value = cloneUpdateBytes(kv.Value)
	return kv, true, nil
}

func stateDBApplyCanonicalWorkerCount(items int) int {
	if items < stateDBApplyCanonicalMinUpdates {
		return 1
	}
	workers := runtime.GOMAXPROCS(0)
	if workers > stateDBApplyCanonicalMaxWork {
		workers = stateDBApplyCanonicalMaxWork
	}
	if workers < 2 || items < workers*2 {
		return 1
	}
	return workers
}

type preparedStateLeaf struct {
	digest   [StateRootSize]byte
	valueLen int
	exists   bool
}

type preparedStateApplyItem struct {
	kv       KVUpdate
	shard    string
	stateKey []byte
	oldLeaf  preparedStateLeaf
	newLeaf  preparedStateLeaf
}

func prepareStateApplyItems(kvs []KVUpdate, shardHexWidth int) ([]preparedStateApplyItem, int) {
	workers := stateDBApplyCanonicalWorkerCount(len(kvs))
	if workers <= 1 {
		out := make([]preparedStateApplyItem, len(kvs))
		for i := range kvs {
			out[i] = prepareStateApplyItem(kvs[i], shardHexWidth)
		}
		return out, 1
	}
	chunk := (len(kvs) + workers - 1) / workers
	parts := make([][]preparedStateApplyItem, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		start := i * chunk
		if start >= len(kvs) {
			parts = parts[:i]
			break
		}
		end := start + chunk
		if end > len(kvs) {
			end = len(kvs)
		}
		wg.Add(1)
		go func(idx, start, end int) {
			defer wg.Done()
			part := make([]preparedStateApplyItem, end-start)
			for j := start; j < end; j++ {
				part[j-start] = prepareStateApplyItem(kvs[j], shardHexWidth)
			}
			parts[idx] = part
		}(i, start, end)
	}
	wg.Wait()
	out := make([]preparedStateApplyItem, 0, len(kvs))
	for _, part := range parts {
		out = append(out, part...)
	}
	return out, len(parts)
}

func prepareStateApplyItem(kv KVUpdate, shardHexWidth int) preparedStateApplyItem {
	sh := shardOf(kv.Key, shardHexWidth)
	item := preparedStateApplyItem{kv: kv, shard: sh, stateKey: kState(sh, kv.Key)}
	if kv.HasOldValue && kv.OldExists {
		item.oldLeaf = makePreparedStateLeaf(kv.Key, kv.OldValue)
	}
	if !kv.Deleted {
		item.newLeaf = makePreparedStateLeaf(kv.Key, kv.Value)
	}
	return item
}

func makePreparedStateLeaf(key string, value []byte) preparedStateLeaf {
	return preparedStateLeaf{digest: HashStateKVLeaf(key, value), valueLen: len(value), exists: true}
}

func (s *DB) updateFingerprintForPutPrepared(bizKey, shard string, oldLeaf, newLeaf preparedStateLeaf) {
	fp := s.liveFingerprints[shard]
	if fp == nil {
		return
	}
	if oldLeaf.exists {
		xorStateDigest(&fp.XorDigest, oldLeaf.digest)
		fp.XorHash ^= digestLogID(oldLeaf.digest[:])
		fp.TotalValueBytes -= int64(oldLeaf.valueLen)
	} else {
		fp.KeyCount++
		fp.TotalKeyBytes += int64(len(bizKey))
	}
	xorStateDigest(&fp.XorDigest, newLeaf.digest)
	fp.XorHash ^= digestLogID(newLeaf.digest[:])
	fp.TotalValueBytes += int64(newLeaf.valueLen)
}

func (s *DB) updateFingerprintForDeletePrepared(bizKey, shard string, oldLeaf preparedStateLeaf) {
	if !oldLeaf.exists {
		return
	}
	fp := s.liveFingerprints[shard]
	if fp == nil {
		return
	}
	xorStateDigest(&fp.XorDigest, oldLeaf.digest)
	fp.XorHash ^= digestLogID(oldLeaf.digest[:])
	fp.KeyCount--
	fp.TotalKeyBytes -= int64(len(bizKey))
	fp.TotalValueBytes -= int64(oldLeaf.valueLen)
}

func cloneUpdateBytes(v []byte) []byte {
	if v == nil {
		return nil
	}
	out := make([]byte, len(v))
	copy(out, v)
	return out
}

                                                 
                                                         
func (s *DB) FlushAndRotate(epochEnd uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createCheckpointLocked(epochEnd, false)
}

                                                                                  
func (s *DB) RecreateCheckpoint(height uint64) error {
	if height == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createCheckpointLocked(height, true)
}
