package statedb

import (
	"bytes"
	"container/heap"
	"dex/logs"
	"errors"
	"sort"
	"time"

	"github.com/cockroachdb/pebble"
)

const (
	stateDBGetManyProbeDebugThreshold   = 5 * time.Millisecond
	stateDBGetManyProbeWarnThreshold    = 50 * time.Millisecond
	stateDBGetManyLockWaitWarnThreshold = 20 * time.Millisecond
)

                                                 
func (s *DB) Get(key string) ([]byte, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.store == nil {
		return nil, false, errors.New("stateDB is not initialized")
	}

	sh := shardOf(key, s.conf.ShardHexWidth)
	stateKey := kState(sh, key)
	v, err := s.store.Get(stateKey)
	if err != nil {
		if s.store.IsNotFound(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return v, true, nil
}

                                                   
func (s *DB) GetMany(keys []string) (out map[string][]byte, err error) {
	startAt := time.Now()
	mode := "init"
	shardGroups := 0
	lockWait := time.Duration(0)
	defer func() {
		cost := time.Since(startAt)
		if err == nil && cost < stateDBGetManyProbeDebugThreshold {
			return
		}
		if err != nil || cost >= stateDBGetManyProbeWarnThreshold || lockWait >= stateDBGetManyLockWaitWarnThreshold {
			logs.Warn(
				"[StateDB][GetMany] mode=%s keys=%d hits=%d shards=%d cost=%s lockWait=%s err=%v",
				mode, len(keys), len(out), shardGroups, cost, lockWait, err,
			)
			return
		}
		logs.Debug(
			"[StateDB][GetMany] mode=%s keys=%d hits=%d shards=%d cost=%s lockWait=%s",
			mode, len(keys), len(out), shardGroups, cost, lockWait,
		)
	}()

	lockWaitAt := time.Now()
	s.mu.RLock()
	lockWait = time.Since(lockWaitAt)
	defer s.mu.RUnlock()
	if s.store == nil {
		mode = "closed"
		return nil, errors.New("stateDB is not initialized")
	}

	out = make(map[string][]byte, len(keys))
	if len(keys) == 0 {
		mode = "empty"
		return out, nil
	}

	                                                                                
	if ps, ok := s.store.(*pebbleStore); ok {
		mode = "pebble_seek"
		keysByShard := make(map[string][]string, len(s.shards))
		for _, key := range keys {
			if key == "" {
				continue
			}
			sh := shardOf(key, s.conf.ShardHexWidth)
			keysByShard[sh] = append(keysByShard[sh], key)
		}
		shardGroups = len(keysByShard)
		if len(keysByShard) == 0 {
			mode = "empty_filtered"
			return out, nil
		}

		shards := make([]string, 0, len(keysByShard))
		for sh := range keysByShard {
			shards = append(shards, sh)
		}
		sort.Strings(shards)

		snap := ps.db.NewSnapshot()
		defer snap.Close()
		iter, err := snap.NewIter(nil)
		if err != nil {
			return nil, err
		}
		defer iter.Close()

		for _, sh := range shards {
			shardKeys := keysByShard[sh]
			sort.Strings(shardKeys)
			uniq := shardKeys[:0]
			prev := ""
			for _, key := range shardKeys {
				if key == prev {
					continue
				}
				uniq = append(uniq, key)
				prev = key
			}
			for _, key := range uniq {
				stateKey := []byte(kState(sh, key))
				if !iter.SeekGE(stateKey) {
					break
				}
				if !bytes.Equal(iter.Key(), stateKey) {
					continue
				}
				val := iter.Value()
				copied := make([]byte, len(val))
				copy(copied, val)
				out[key] = copied
			}
		}
		if err := iter.Error(); err != nil {
			return nil, err
		}
		return out, nil
	}

	                                            
	mode = "fallback_get"
	err = s.store.View(func(tx kvReader) error {
		for _, key := range keys {
			if key == "" {
				continue
			}
			sh := shardOf(key, s.conf.ShardHexWidth)
			stateKey := kState(sh, key)
			v, err := tx.Get(stateKey)
			if err != nil {
				if s.store.IsNotFound(err) {
					continue
				}
				return err
			}
			out[key] = v
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

                                      
func (s *DB) Exists(key string) (bool, error) {
	_, exists, err := s.Get(key)
	return exists, err
}

                                                                            
                    
func (s *DB) PageCurrentDiff(shard string, pageSize int, pageToken string) (Page, error) {
	if pageSize <= 0 {
		pageSize = s.conf.PageSize
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.store == nil {
		return Page{}, errors.New("stateDB is not initialized")
	}

	startAfter := decodeToken(pageToken)
	prefix := kStatePrefix(shard)
	prefixStr := string(prefix)

	stopScan := errors.New("stop scan")
	out := Page{Items: make([]KVUpdate, 0, pageSize)}
	last := ""

	err := s.store.View(func(tx kvReader) error {
		return tx.IteratePrefix(prefix, []byte(startAfter), func(key []byte, value []byte) error {
			k := string(key)
			if len(k) <= len(prefixStr) || k[:len(prefixStr)] != prefixStr {
				return nil
			}
			bizKey := k[len(prefixStr):]
			out.Items = append(out.Items, KVUpdate{
				Key:     bizKey,
				Value:   append([]byte(nil), value...),
				Deleted: false,
			})
			last = bizKey
			if len(out.Items) >= pageSize {
				return stopScan
			}
			return nil
		})
	})
	if err != nil && !errors.Is(err, stopScan) {
		return Page{}, err
	}
	if last != "" {
		out.NextPageToken = encodeToken(last)
	}
	return out, nil
}

                                                                            
func (s *DB) IterateLatestByPrefix(prefix string, fn func(key string, value []byte) error) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.store == nil {
		return errors.New("stateDB is not initialized")
	}
	for _, shard := range s.shards {
		statePrefix := kStatePrefix(shard)
		statePrefixLen := len(statePrefix)
		scanPrefix := kState(shard, prefix)

		err := s.store.View(func(tx kvReader) error {
			return tx.IteratePrefix(scanPrefix, nil, func(key []byte, value []byte) error {
				if len(key) <= statePrefixLen {
					return nil
				}
				return fn(string(key[statePrefixLen:]), append([]byte(nil), value...))
			})
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *DB) IterateLatestByPrefixLimit(prefix string, limit int, reverse bool) ([]KVUpdate, error) {
	return s.IterateLatestByPrefixLimitAfter(prefix, "", limit, reverse)
}

func (s *DB) IterateLatestByPrefixLimitAfter(prefix, startAfter string, limit int, reverse bool) ([]KVUpdate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.store == nil {
		return nil, errors.New("stateDB is not initialized")
	}

	if ps, ok := s.store.(*pebbleStore); ok {
		return s.iterateLatestByPrefixLimitPebbleLocked(ps, prefix, startAfter, limit, reverse)
	}

	out := make([]KVUpdate, 0)
	for _, shard := range s.shards {
		statePrefix := kStatePrefix(shard)
		statePrefixLen := len(statePrefix)
		scanPrefix := kState(shard, prefix)
		if err := s.store.View(func(tx kvReader) error {
			return tx.IteratePrefix(scanPrefix, nil, func(key []byte, value []byte) error {
				if len(key) <= statePrefixLen {
					return nil
				}
				bizKey := string(key[statePrefixLen:])
				if orderedScanAfter(bizKey, startAfter, reverse) {
					out = append(out, KVUpdate{Key: bizKey, Value: append([]byte(nil), value...)})
				}
				return nil
			})
		}); err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if reverse {
			return out[i].Key > out[j].Key
		}
		return out[i].Key < out[j].Key
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *DB) IterateLatestKeysByPrefixLimitAfter(prefix, startAfter string, limit int, reverse bool) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.store == nil {
		return nil, errors.New("stateDB is not initialized")
	}

	if ps, ok := s.store.(*pebbleStore); ok {
		return s.iterateLatestKeysByPrefixLimitPebbleLocked(ps, prefix, startAfter, limit, reverse)
	}

	out := make([]string, 0)
	for _, shard := range s.shards {
		statePrefix := kStatePrefix(shard)
		statePrefixLen := len(statePrefix)
		scanPrefix := kState(shard, prefix)
		if err := s.store.View(func(tx kvReader) error {
			return tx.IteratePrefix(scanPrefix, nil, func(key []byte, value []byte) error {
				if len(key) <= statePrefixLen {
					return nil
				}
				bizKey := string(key[statePrefixLen:])
				if orderedScanAfter(bizKey, startAfter, reverse) {
					out = append(out, bizKey)
				}
				return nil
			})
		}); err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if reverse {
			return out[i] > out[j]
		}
		return out[i] < out[j]
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *DB) iterateLatestByPrefixLimitPebbleLocked(ps *pebbleStore, prefix, startAfter string, limit int, reverse bool) ([]KVUpdate, error) {
	capHint := 0
	if limit > 0 {
		capHint = limit
	}
	out := make([]KVUpdate, 0, capHint)
	snap := ps.db.NewSnapshot()
	defer snap.Close()

	h := stateScanHeap{reverse: reverse}
	heap.Init(&h)
	cursors := make([]*stateScanCursor, 0, len(s.shards))
	for _, shard := range s.shards {
		statePrefix := kStatePrefix(shard)
		scanPrefix := kState(shard, prefix)
		upper := prefixUpperBound(scanPrefix)
		iter, err := snap.NewIter(&pebble.IterOptions{LowerBound: scanPrefix, UpperBound: upper})
		if err != nil {
			for _, c := range cursors {
				c.iter.Close()
			}
			return nil, err
		}
		cursor := &stateScanCursor{
			iter:           iter,
			statePrefixLen: len(statePrefix),
			scanPrefix:     scanPrefix,
			shard:          shard,
		}
		cursors = append(cursors, cursor)

		var ok bool
		if reverse {
			if startAfter != "" {
				ok = iter.SeekLT([]byte(kState(shard, startAfter)))
			} else if upper == nil {
				ok = iter.Last()
			} else {
				ok = iter.SeekLT(upper)
			}
		} else {
			if startAfter != "" {
				ok = iter.SeekGE([]byte(kState(shard, startAfter)))
				if ok && bytes.Equal(iter.Key(), []byte(kState(shard, startAfter))) {
					ok = iter.Next()
				}
			} else {
				ok = iter.SeekGE(scanPrefix)
			}
		}
		if ok && cursor.captureKey() {
			heap.Push(&h, cursor)
		}
	}

	for h.Len() > 0 && (limit <= 0 || len(out) < limit) {
		cursor := heap.Pop(&h).(*stateScanCursor)
		out = append(out, KVUpdate{Key: cursor.key, Value: append([]byte(nil), cursor.iter.Value()...)})

		var ok bool
		if reverse {
			ok = cursor.iter.Prev()
		} else {
			ok = cursor.iter.Next()
		}
		if ok && cursor.captureKey() {
			heap.Push(&h, cursor)
		}
	}

	for _, cursor := range cursors {
		if err := cursor.iter.Error(); err != nil {
			cursor.iter.Close()
			return nil, err
		}
		cursor.iter.Close()
	}
	return out, nil
}

func (s *DB) iterateLatestKeysByPrefixLimitPebbleLocked(ps *pebbleStore, prefix, startAfter string, limit int, reverse bool) ([]string, error) {
	capHint := 0
	if limit > 0 {
		capHint = limit
	}
	out := make([]string, 0, capHint)
	snap := ps.db.NewSnapshot()
	defer snap.Close()

	h := stateScanHeap{reverse: reverse}
	heap.Init(&h)
	cursors := make([]*stateScanCursor, 0, len(s.shards))
	for _, shard := range s.shards {
		statePrefix := kStatePrefix(shard)
		scanPrefix := kState(shard, prefix)
		upper := prefixUpperBound(scanPrefix)
		iter, err := snap.NewIter(&pebble.IterOptions{LowerBound: scanPrefix, UpperBound: upper})
		if err != nil {
			for _, c := range cursors {
				c.iter.Close()
			}
			return nil, err
		}
		cursor := &stateScanCursor{
			iter:           iter,
			statePrefixLen: len(statePrefix),
			scanPrefix:     scanPrefix,
			shard:          shard,
		}
		cursors = append(cursors, cursor)

		var ok bool
		if reverse {
			if startAfter != "" {
				ok = iter.SeekLT([]byte(kState(shard, startAfter)))
			} else if upper == nil {
				ok = iter.Last()
			} else {
				ok = iter.SeekLT(upper)
			}
		} else {
			if startAfter != "" {
				ok = iter.SeekGE([]byte(kState(shard, startAfter)))
				if ok && bytes.Equal(iter.Key(), []byte(kState(shard, startAfter))) {
					ok = iter.Next()
				}
			} else {
				ok = iter.SeekGE(scanPrefix)
			}
		}
		if ok && cursor.captureKey() {
			heap.Push(&h, cursor)
		}
	}

	for h.Len() > 0 && (limit <= 0 || len(out) < limit) {
		cursor := heap.Pop(&h).(*stateScanCursor)
		out = append(out, cursor.key)
		var ok bool
		if reverse {
			ok = cursor.iter.Prev()
		} else {
			ok = cursor.iter.Next()
		}
		if ok && cursor.captureKey() {
			heap.Push(&h, cursor)
		}
	}

	for _, cursor := range cursors {
		if err := cursor.iter.Error(); err != nil {
			cursor.iter.Close()
			return nil, err
		}
		cursor.iter.Close()
	}
	return out, nil
}

func orderedScanAfter(key, startAfter string, reverse bool) bool {
	if startAfter == "" {
		return true
	}
	if reverse {
		return key < startAfter
	}
	return key > startAfter
}

type stateScanCursor struct {
	iter           *pebble.Iterator
	statePrefixLen int
	scanPrefix     []byte
	shard          string
	key            string
}

func (c *stateScanCursor) captureKey() bool {
	key := c.iter.Key()
	if !bytes.HasPrefix(key, c.scanPrefix) {
		return false
	}
	c.key = string(key[c.statePrefixLen:])
	return true
}

type stateScanHeap struct {
	items   []*stateScanCursor
	reverse bool
}

func (h stateScanHeap) Len() int { return len(h.items) }

func (h stateScanHeap) Less(i, j int) bool {
	if h.items[i].key == h.items[j].key {
		return h.items[i].shard < h.items[j].shard
	}
	if h.reverse {
		return h.items[i].key > h.items[j].key
	}
	return h.items[i].key < h.items[j].key
}

func (h stateScanHeap) Swap(i, j int) { h.items[i], h.items[j] = h.items[j], h.items[i] }

func (h *stateScanHeap) Push(x any) { h.items = append(h.items, x.(*stateScanCursor)) }

func (h *stateScanHeap) Pop() any {
	old := h.items
	n := len(old)
	item := old[n-1]
	h.items[n-1] = nil
	h.items = old[:n-1]
	return item
}

                                             
func (s *DB) IterateLatestSnapshot(fn func(key string, value []byte) error) error {
	return s.IterateLatestByPrefix("", fn)
}
