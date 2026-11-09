package vm

import (
	keypkg "dex/keys"
	statedb "dex/stateDB"
	"errors"
	"runtime"
	"sort"
	"sync"

	"github.com/cockroachdb/pebble"
)

const (
	stateViewParallelDiffMinKeys = 4096
	stateViewParallelDiffMaxWork = 8
)

                                              

                         
type ovVal struct {
	val   []byte
	exist bool                        
}

                                     
type change struct {
	key     string
	prev    ovVal
	hasPrev bool
}

                                        

                                            
type overlayStateView struct {
	mu                  sync.RWMutex
	networkChainID      uint64                                                                     
	read                ReadThroughFn
	baseBatchRead       BatchReadFn
	scan                ScanFn
	scanLimit           ScanWithLimitFn
	scanLimitReverse    ScanWithLimitReverseFn
	scanLimitOrdered    ScanWithLimitOrderedFn
	scanLimitAfter      ScanWithLimitOrderedAfterFn
	scanKeysOrdered     ScanKeysWithLimitOrderedFn
	scanKeysAfter       ScanKeysWithLimitOrderedAfterFn
	scanFutureBook      ScanFutureOrderbookFn
	scanFutureBookVisit ScanFutureOrderbookVisitFn
	overlay             map[string]ovVal
	overlayIndex        *overlayKeyIndex
	baseGetCache        map[string]ovVal
	baseScanCache       map[string]map[string][]byte
	baseOrderedCache    map[orderedScanCacheKey][]KVPair
	activitySeq         map[string]uint32
	activitySeqLog      []activitySeqChange
	changelog           []change
	revision            uint64
}

func (s *overlayStateView) BindNetworkChainID(chainID uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.networkChainID = chainID
}

func (s *overlayStateView) NetworkChainID() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.networkChainID
}

type orderedScanCacheKey struct {
	prefix     string
	startAfter string
	limit      int
	reverse    bool
}

type activitySeqChange struct {
	snap   int
	prefix string
	prev   uint32
	had    bool
}

                                                                          
                                                                          
                                                                              
                                                                              
                                                    
func isStateViewNotFound(err error) bool {
	return errors.Is(err, statedb.ErrNotFound) ||
		errors.Is(err, statedb.ErrKVNotFound) ||
		errors.Is(err, pebble.ErrNotFound)
}

                                     
func NewStateView(read ReadThroughFn, scan ScanFn) StateView {
	return &overlayStateView{
		read:      read,
		scan:      scan,
		overlay:   make(map[string]ovVal, 1024),
		changelog: make([]change, 0, 1024),
	}
}

                                                                  
func NewStateViewWithLimitScan(read ReadThroughFn, scan ScanFn, scanLimit ScanWithLimitFn, scanLimitReverse ScanWithLimitReverseFn) StateView {
	return NewStateViewWithLimitScanOrdered(read, scan, scanLimit, scanLimitReverse, nil, nil)
}

func NewStateViewWithLimitScanOrdered(read ReadThroughFn, scan ScanFn, scanLimit ScanWithLimitFn, scanLimitReverse ScanWithLimitReverseFn, scanLimitOrdered ScanWithLimitOrderedFn, scanLimitAfter ScanWithLimitOrderedAfterFn) StateView {
	return &overlayStateView{
		read:             read,
		scan:             scan,
		scanLimit:        scanLimit,
		scanLimitReverse: scanLimitReverse,
		scanLimitOrdered: scanLimitOrdered,
		scanLimitAfter:   scanLimitAfter,
		overlay:          make(map[string]ovVal, 1024),
		changelog:        make([]change, 0, 1024),
	}
}

func NewStateViewWithLimitScanOrderedAndBatch(read ReadThroughFn, batchRead BatchReadFn, scan ScanFn, scanLimit ScanWithLimitFn, scanLimitReverse ScanWithLimitReverseFn, scanLimitOrdered ScanWithLimitOrderedFn, scanLimitAfter ScanWithLimitOrderedAfterFn) StateView {
	sv := NewStateViewWithLimitScanOrdered(read, scan, scanLimit, scanLimitReverse, scanLimitOrdered, scanLimitAfter).(*overlayStateView)
	sv.baseBatchRead = batchRead
	return sv
}

func NewStateViewWithLimitScanOrderedBatchAndKeys(read ReadThroughFn, batchRead BatchReadFn, scan ScanFn, scanLimit ScanWithLimitFn, scanLimitReverse ScanWithLimitReverseFn, scanLimitOrdered ScanWithLimitOrderedFn, scanLimitAfter ScanWithLimitOrderedAfterFn, scanKeysOrdered ScanKeysWithLimitOrderedFn, scanKeysAfter ScanKeysWithLimitOrderedAfterFn) StateView {
	sv := NewStateViewWithLimitScanOrderedAndBatch(read, batchRead, scan, scanLimit, scanLimitReverse, scanLimitOrdered, scanLimitAfter).(*overlayStateView)
	sv.scanKeysOrdered = scanKeysOrdered
	sv.scanKeysAfter = scanKeysAfter
	return sv
}

func NewStateViewWithLimitScanOrderedBatchKeysAndFuture(read ReadThroughFn, batchRead BatchReadFn, scan ScanFn, scanLimit ScanWithLimitFn, scanLimitReverse ScanWithLimitReverseFn, scanLimitOrdered ScanWithLimitOrderedFn, scanLimitAfter ScanWithLimitOrderedAfterFn, scanKeysOrdered ScanKeysWithLimitOrderedFn, scanKeysAfter ScanKeysWithLimitOrderedAfterFn, scanFutureBook ScanFutureOrderbookFn, scanFutureBookVisit ScanFutureOrderbookVisitFn) StateView {
	sv := NewStateViewWithLimitScanOrderedBatchAndKeys(read, batchRead, scan, scanLimit, scanLimitReverse, scanLimitOrdered, scanLimitAfter, scanKeysOrdered, scanKeysAfter).(*overlayStateView)
	sv.scanFutureBook = scanFutureBook
	sv.scanFutureBookVisit = scanFutureBookVisit
	return sv
}

func releaseStateViewHistory(sv StateView) {
	if releaser, ok := sv.(interface{ ReleaseHistory() }); ok {
		releaser.ReleaseHistory()
	}
}

func stateViewChangedKeysSince(sv StateView, snap int) map[string]struct{} {
	if tracker, ok := sv.(interface{ ChangedKeysSince(int) map[string]struct{} }); ok {
		return tracker.ChangedKeysSince(snap)
	}
	return nil
}

func stateViewHasPendingWrite(sv StateView, key string) bool {
	if tracker, ok := sv.(interface{ HasPendingWrite(string) bool }); ok {
		return tracker.HasPendingWrite(key)
	}
	return false
}

func prefetchStateViewOldValues(sv StateView) error {
	if prefetcher, ok := sv.(interface{ PrefetchMissingOldValues() error }); ok {
		return prefetcher.PrefetchMissingOldValues()
	}
	return nil
}

func applyWriteOpsToStateView(sv StateView, ops []WriteOp) {
	if applier, ok := sv.(interface{ ApplyWriteOps([]WriteOp) }); ok {
		applier.ApplyWriteOps(ops)
		return
	}
	for i := range ops {
		op := &ops[i]
		if op.Key == "" {
			continue
		}
		if op.Del {
			sv.Del(op.Key)
			continue
		}
		sv.Set(op.Key, op.Value)
	}
}

func (s *overlayStateView) ApplyWriteOps(ops []WriteOp) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range ops {
		op := &ops[i]
		if op.Key == "" {
			continue
		}
		if op.HasOldValue {
			s.rememberBaseValueLocked(op.Key, op.OldValue, op.OldExists)
		}
		if _, has := s.overlay[op.Key]; !has {
			s.insertOverlayKeyLocked(op.Key)
		}
		if op.Del {
			s.overlay[op.Key] = ovVal{exist: false}
			s.revision++
			continue
		}
		s.overlay[op.Key] = ovVal{val: cloneBytes(op.Value), exist: true}
		s.revision++
	}
}

func (s *overlayStateView) Get(key string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if v, ok := s.overlay[key]; ok {
		if !v.exist {                      
			return nil, false, nil
		}
		                                    
		result := make([]byte, len(v.val))
		copy(result, v.val)
		return result, true, nil
	}
	if v, ok := s.baseGetCache[key]; ok {
		if !v.exist {
			return nil, false, nil
		}
		return cloneBytes(v.val), true, nil
	}

	                        
	val, err := s.read(key)
	if err != nil {
		if isStateViewNotFound(err) {
			s.rememberBaseValueLocked(key, nil, false)
			return nil, false, nil
		}
		return nil, false, err
	}
	if val == nil {
		s.rememberBaseValueLocked(key, nil, false)
		return nil, false, nil
	}
	s.rememberBaseValueLocked(key, val, true)
	return val, true, nil
}

func (s *overlayStateView) GetMany(keys []string) (map[string][]byte, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	result := make(map[string][]byte, len(keys))
	missing := make([]string, 0, len(keys))

	s.mu.Lock()
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if v, ok := s.overlay[key]; ok {
			if v.exist {
				result[key] = cloneBytes(v.val)
			}
			continue
		}
		if v, ok := s.baseGetCache[key]; ok {
			if v.exist {
				result[key] = cloneBytes(v.val)
			}
			continue
		}
		missing = append(missing, key)
	}
	batchRead := s.baseBatchRead
	read := s.read
	s.mu.Unlock()

	if len(missing) == 0 {
		return result, nil
	}
	if batchRead != nil {
		values, err := batchRead(missing)
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, key := range missing {
			if _, ok := s.baseGetCache[key]; ok {
				if v, ok := s.baseGetCache[key]; ok && v.exist {
					result[key] = cloneBytes(v.val)
				}
				continue
			}
			v, exists := values[key]
			s.rememberBaseValueLocked(key, v, exists)
			if exists {
				result[key] = cloneBytes(v)
			}
		}
		return result, nil
	}
	if read == nil {
		return result, nil
	}
	values := make(map[string]ovVal, len(missing))
	for _, key := range missing {
		v, err := read(key)
		if err != nil {
			if isStateViewNotFound(err) {
				values[key] = ovVal{exist: false}
				continue
			}
			return nil, err
		}
		values[key] = ovVal{val: v, exist: v != nil}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, key := range missing {
		if _, ok := s.baseGetCache[key]; ok {
			if v, ok := s.baseGetCache[key]; ok && v.exist {
				result[key] = cloneBytes(v.val)
			}
			continue
		}
		v := values[key]
		s.rememberBaseValueLocked(key, v.val, v.exist)
		if v.exist {
			result[key] = cloneBytes(v.val)
		}
	}
	return result, nil
}

func (s *overlayStateView) rememberBaseValueLocked(key string, val []byte, exists bool) {
	if key == "" {
		return
	}
	if s.baseGetCache == nil {
		s.baseGetCache = make(map[string]ovVal)
	}
	if _, ok := s.baseGetCache[key]; ok {
		return
	}
	if !exists {
		s.baseGetCache[key] = ovVal{exist: false}
		return
	}
	s.baseGetCache[key] = ovVal{val: cloneBytes(val), exist: true}
}

func (s *overlayStateView) rememberBaseMapLocked(base map[string][]byte) {
	for k, v := range base {
		s.rememberBaseValueLocked(k, v, true)
	}
}

func (s *overlayStateView) rememberBasePairsLocked(base []KVPair) {
	for _, p := range base {
		s.rememberBaseValueLocked(p.Key, p.Value, true)
	}
}

func (s *overlayStateView) Set(key string, val []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	prev, has := s.overlay[key]
	s.changelog = append(s.changelog, change{key: key, prev: prev, hasPrev: has})
	if !has {
		s.insertOverlayKeyLocked(key)
	}
	                                                   
	valCopy := make([]byte, len(val))
	copy(valCopy, val)
	s.overlay[key] = ovVal{val: valCopy, exist: true}
	s.revision++
}

func cloneBytes(v []byte) []byte {
	if v == nil {
		return nil
	}
	out := make([]byte, len(v))
	copy(out, v)
	return out
}

func cloneKVPairs(in []KVPair) []KVPair {
	out := make([]KVPair, 0, len(in))
	for _, p := range in {
		out = append(out, KVPair{Key: p.Key, Value: cloneBytes(p.Value)})
	}
	return out
}

func cloneBytesMap(in map[string][]byte) map[string][]byte {
	out := make(map[string][]byte, len(in))
	for k, v := range in {
		out[k] = cloneBytes(v)
	}
	return out
}

func prefixUpperBoundString(prefix string) (string, bool) {
	if prefix == "" {
		return "", false
	}
	b := []byte(prefix)
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] != 0xff {
			b[i]++
			return string(b[:i+1]), true
		}
	}
	return "", false
}

func (s *overlayStateView) insertOverlayKeyLocked(key string) {
	if s.overlayIndex != nil {
		s.overlayIndex.insert(key)
	}
}

func (s *overlayStateView) removeOverlayKeyLocked(key string) {
	if s.overlayIndex != nil {
		s.overlayIndex.remove(key)
	}
}

func (s *overlayStateView) ensureOverlayIndexLocked() {
	if s.overlayIndex != nil {
		return
	}
	keys := make([]string, 0, len(s.overlay))
	for k := range s.overlay {
		keys = append(keys, k)
	}
	s.overlayIndex = newOverlayKeyIndex(keys)
}

func (s *overlayStateView) overlayKeysLocked(prefix, startAfter, frontier string, reverse bool) []string {
	s.ensureOverlayIndexLocked()
	return s.overlayIndex.rangeKeys(prefix, startAfter, frontier, reverse)
}

func (s *overlayStateView) overlayTombstoneCountLocked(prefix, startAfter string, reverse bool) int {
	count := 0
	for _, k := range s.overlayKeysLocked(prefix, startAfter, "", reverse) {
		if !s.overlay[k].exist {
			count++
		}
	}
	return count
}

func (s *overlayStateView) effectiveBaseLimitLocked(prefix, startAfter string, limit int, reverse bool) int {
	if limit <= 0 {
		return limit
	}
	return limit + s.overlayTombstoneCountLocked(prefix, startAfter, reverse)
}

func (s *overlayStateView) baseScanCachedLocked(prefix string) (map[string][]byte, error) {
	if s.scan == nil {
		return nil, nil
	}
	if s.baseScanCache != nil {
		if cached, ok := s.baseScanCache[prefix]; ok {
			return cloneBytesMap(cached), nil
		}
	}
	base, err := s.scan(prefix)
	if err != nil {
		return nil, err
	}
	s.rememberBaseMapLocked(base)
	if s.baseScanCache == nil {
		s.baseScanCache = make(map[string]map[string][]byte)
	}
	cached := cloneBytesMap(base)
	s.baseScanCache[prefix] = cached
	return cloneBytesMap(cached), nil
}

func (s *overlayStateView) baseOrderedCachedLocked(prefix, startAfter string, limit int, reverse bool) ([]KVPair, error) {
	key := orderedScanCacheKey{prefix: prefix, startAfter: startAfter, limit: limit, reverse: reverse}
	if s.baseOrderedCache != nil {
		if cached, ok := s.baseOrderedCache[key]; ok {
			return cached, nil
		}
	}

	var (
		base []KVPair
		err  error
	)
	if startAfter != "" && s.scanLimitAfter != nil {
		base, err = s.scanLimitAfter(prefix, startAfter, limit, reverse)
	} else if startAfter == "" && s.scanLimitOrdered != nil {
		base, err = s.scanLimitOrdered(prefix, limit, reverse)
	} else {
		var kvs map[string][]byte
		if startAfter != "" && s.scan != nil {
			kvs, err = s.scan(prefix)
		} else if reverse && s.scanLimitReverse != nil {
			kvs, err = s.scanLimitReverse(prefix, limit)
		} else if !reverse && s.scanLimit != nil {
			kvs, err = s.scanLimit(prefix, limit)
		} else if s.scan != nil {
			kvs, err = s.scan(prefix)
		}
		if err != nil {
			return nil, err
		}
		base = kvMapToOrderedPairs(kvs, reverse)
	}
	if startAfter != "" {
		filtered := base[:0]
		for _, pair := range base {
			if orderedAfter(pair.Key, startAfter, reverse) {
				filtered = append(filtered, pair)
			}
		}
		base = filtered
	}
	if limit > 0 && len(base) > limit {
		base = base[:limit]
	}
	s.rememberBasePairsLocked(base)
	if s.baseOrderedCache == nil {
		s.baseOrderedCache = make(map[orderedScanCacheKey][]KVPair)
	}
	cached := cloneKVPairs(base)
	s.baseOrderedCache[key] = cached
	return cached, nil
}

func (s *overlayStateView) baseOrderedKeysLocked(prefix, startAfter string, limit int, reverse bool) ([]string, error) {
	if startAfter != "" && s.scanKeysAfter != nil {
		return s.scanKeysAfter(prefix, startAfter, limit, reverse)
	}
	if startAfter == "" && s.scanKeysOrdered != nil {
		return s.scanKeysOrdered(prefix, limit, reverse)
	}
	base, err := s.baseOrderedCachedLocked(prefix, startAfter, limit, reverse)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(base))
	for _, p := range base {
		out = append(out, p.Key)
	}
	return out, nil
}

                                            
func (s *overlayStateView) Scan(prefix string) (map[string][]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := make(map[string][]byte)

	                                                                                
	base, err := s.baseScanCachedLocked(prefix)
	if err != nil {
		return nil, err
	}
	for k, v := range base {
		result[k] = v
	}

	                        
	for _, k := range s.overlayKeysLocked(prefix, "", "", false) {
		v := s.overlay[k]
		if !v.exist {
			delete(result, k)
			continue
		}
		result[k] = cloneBytes(v.val)
	}

	return result, nil
}

func (s *overlayStateView) NextUserActivitySeq(prefix string) (uint32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activitySeq == nil {
		s.activitySeq = make(map[string]uint32)
	}
	next, ok := s.activitySeq[prefix]
	if !ok {
		n, err := s.countEffectivePrefixLocked(prefix)
		if err != nil {
			return 0, err
		}
		next = uint32(n)
	}
	if next == ^uint32(0) {
		return 0, errors.New("user activity seq overflow")
	}
	prev, had := s.activitySeq[prefix]
	s.activitySeqLog = append(s.activitySeqLog, activitySeqChange{
		snap:   len(s.changelog),
		prefix: prefix,
		prev:   prev,
		had:    had,
	})
	s.activitySeq[prefix] = next + 1
	return next, nil
}

func (s *overlayStateView) countEffectivePrefixLocked(prefix string) (int, error) {
	base, err := s.baseScanCachedLocked(prefix)
	if err != nil {
		return 0, err
	}
	count := len(base)
	for _, k := range s.overlayKeysLocked(prefix, "", "", false) {
		v := s.overlay[k]
		_, inBase := base[k]
		if v.exist {
			if !inBase {
				count++
			}
			continue
		}
		if inBase {
			count--
		}
	}
	return count, nil
}

                                                                          
func (s *overlayStateView) mergeOverlay(result map[string][]byte, prefix string) {
	for _, k := range s.overlayKeysLocked(prefix, "", "", false) {
		v := s.overlay[k]
		if !v.exist {
			delete(result, k)
			continue
		}
		result[k] = cloneBytes(v.val)
	}
}

func kvMapToOrderedPairs(kvs map[string][]byte, reverse bool) []KVPair {
	keys := make([]string, 0, len(kvs))
	for k := range kvs {
		keys = append(keys, k)
	}
	if reverse {
		sort.Sort(sort.Reverse(sort.StringSlice(keys)))
	} else {
		sort.Strings(keys)
	}
	out := make([]KVPair, 0, len(keys))
	for _, k := range keys {
		val := make([]byte, len(kvs[k]))
		copy(val, kvs[k])
		out = append(out, KVPair{Key: k, Value: val})
	}
	return out
}

func orderedPairsToMap(pairs []KVPair) map[string][]byte {
	out := make(map[string][]byte, len(pairs))
	for _, p := range pairs {
		out[p.Key] = cloneBytes(p.Value)
	}
	return out
}

func orderedCompare(a, b string, reverse bool) int {
	if a == b {
		return 0
	}
	if reverse {
		if a > b {
			return -1
		}
		return 1
	}
	if a < b {
		return -1
	}
	return 1
}

func orderedAfter(key, startAfter string, reverse bool) bool {
	if startAfter == "" {
		return true
	}
	if reverse {
		return key < startAfter
	}
	return key > startAfter
}

func appendKVPairCopy(out []KVPair, key string, value []byte) []KVPair {
	return append(out, KVPair{Key: key, Value: cloneBytes(value)})
}

func (s *overlayStateView) mergeOrderedPairsWithOverlay(base []KVPair, prefix, startAfter string, reverse bool) []KVPair {
	overlayKeys := s.overlayKeysLocked(prefix, startAfter, "", reverse)
	out := make([]KVPair, 0, len(base)+len(overlayKeys))

	baseIdx := 0
	overlayIdx := 0
	overlayDone := func() bool { return overlayIdx >= len(overlayKeys) }
	nextOverlayKey := func() string { return overlayKeys[overlayIdx] }
	advanceOverlay := func() { overlayIdx++ }

	for baseIdx < len(base) || !overlayDone() {
		if baseIdx >= len(base) {
			k := nextOverlayKey()
			v := s.overlay[k]
			if v.exist && orderedAfter(k, startAfter, reverse) {
				out = appendKVPairCopy(out, k, v.val)
			}
			advanceOverlay()
			continue
		}
		if overlayDone() {
			p := base[baseIdx]
			if orderedAfter(p.Key, startAfter, reverse) {
				out = appendKVPairCopy(out, p.Key, p.Value)
			}
			baseIdx++
			continue
		}

		baseKey := base[baseIdx].Key
		overlayKey := nextOverlayKey()
		switch orderedCompare(baseKey, overlayKey, reverse) {
		case 0:
			v := s.overlay[overlayKey]
			if v.exist && orderedAfter(overlayKey, startAfter, reverse) {
				out = appendKVPairCopy(out, overlayKey, v.val)
			}
			baseIdx++
			advanceOverlay()
		case -1:
			p := base[baseIdx]
			if orderedAfter(p.Key, startAfter, reverse) {
				out = appendKVPairCopy(out, p.Key, p.Value)
			}
			baseIdx++
		default:
			v := s.overlay[overlayKey]
			if v.exist && orderedAfter(overlayKey, startAfter, reverse) {
				out = appendKVPairCopy(out, overlayKey, v.val)
			}
			advanceOverlay()
		}
	}
	return out
}

func (s *overlayStateView) mergeOrderedKeysWithOverlay(base []string, prefix, startAfter string, reverse bool) []string {
	overlayKeys := s.overlayKeysLocked(prefix, startAfter, "", reverse)
	out := make([]string, 0, len(base)+len(overlayKeys))

	baseIdx := 0
	overlayIdx := 0
	overlayDone := func() bool { return overlayIdx >= len(overlayKeys) }
	nextOverlayKey := func() string { return overlayKeys[overlayIdx] }
	advanceOverlay := func() { overlayIdx++ }

	for baseIdx < len(base) || !overlayDone() {
		if baseIdx >= len(base) {
			k := nextOverlayKey()
			if s.overlay[k].exist && orderedAfter(k, startAfter, reverse) {
				out = append(out, k)
			}
			advanceOverlay()
			continue
		}
		if overlayDone() {
			k := base[baseIdx]
			if orderedAfter(k, startAfter, reverse) {
				out = append(out, k)
			}
			baseIdx++
			continue
		}

		baseKey := base[baseIdx]
		overlayKey := nextOverlayKey()
		switch orderedCompare(baseKey, overlayKey, reverse) {
		case 0:
			if s.overlay[overlayKey].exist && orderedAfter(overlayKey, startAfter, reverse) {
				out = append(out, overlayKey)
			}
			baseIdx++
			advanceOverlay()
		case -1:
			if orderedAfter(baseKey, startAfter, reverse) {
				out = append(out, baseKey)
			}
			baseIdx++
		default:
			if s.overlay[overlayKey].exist && orderedAfter(overlayKey, startAfter, reverse) {
				out = append(out, overlayKey)
			}
			advanceOverlay()
		}
	}
	return out
}

func growOrderedScanChunk(chunk int) int {
	maxInt := int(^uint(0) >> 1)
	if chunk >= maxInt/2 {
		return maxInt
	}
	return chunk * 2
}

func (s *overlayStateView) mergeOrderedPairWindowLocked(base []KVPair, prefix, startAfter, frontier string, reverse bool, limit int) []KVPair {
	overlayKeys := s.overlayKeysLocked(prefix, startAfter, frontier, reverse)
	capacity := len(base) + len(overlayKeys)
	if limit > 0 && capacity > limit {
		capacity = limit
	}
	out := make([]KVPair, 0, capacity)
	baseIdx := 0
	overlayIdx := 0
	overlayDone := func() bool { return overlayIdx >= len(overlayKeys) }
	nextOverlayKey := func() string { return overlayKeys[overlayIdx] }
	advanceOverlay := func() { overlayIdx++ }
	appendPair := func(key string, value []byte) {
		if limit <= 0 || len(out) < limit {
			out = appendKVPairCopy(out, key, value)
		}
	}
	for (limit <= 0 || len(out) < limit) && (baseIdx < len(base) || !overlayDone()) {
		if baseIdx >= len(base) {
			key := nextOverlayKey()
			value := s.overlay[key]
			if value.exist {
				appendPair(key, value.val)
			}
			advanceOverlay()
			continue
		}
		if overlayDone() {
			pair := base[baseIdx]
			appendPair(pair.Key, pair.Value)
			baseIdx++
			continue
		}
		baseKey := base[baseIdx].Key
		overlayKey := nextOverlayKey()
		switch orderedCompare(baseKey, overlayKey, reverse) {
		case 0:
			value := s.overlay[overlayKey]
			if value.exist {
				appendPair(overlayKey, value.val)
			}
			baseIdx++
			advanceOverlay()
		case -1:
			appendPair(baseKey, base[baseIdx].Value)
			baseIdx++
		default:
			value := s.overlay[overlayKey]
			if value.exist {
				appendPair(overlayKey, value.val)
			}
			advanceOverlay()
		}
	}
	return out
}

func (s *overlayStateView) mergeOrderedKeyWindowLocked(base []string, prefix, startAfter, frontier string, reverse bool, limit int) []string {
	overlayKeys := s.overlayKeysLocked(prefix, startAfter, frontier, reverse)
	capacity := len(base) + len(overlayKeys)
	if limit > 0 && capacity > limit {
		capacity = limit
	}
	out := make([]string, 0, capacity)
	baseIdx := 0
	overlayIdx := 0
	overlayDone := func() bool { return overlayIdx >= len(overlayKeys) }
	nextOverlayKey := func() string { return overlayKeys[overlayIdx] }
	advanceOverlay := func() { overlayIdx++ }
	appendKey := func(key string) {
		if limit <= 0 || len(out) < limit {
			out = append(out, key)
		}
	}
	for (limit <= 0 || len(out) < limit) && (baseIdx < len(base) || !overlayDone()) {
		if baseIdx >= len(base) {
			key := nextOverlayKey()
			if s.overlay[key].exist {
				appendKey(key)
			}
			advanceOverlay()
			continue
		}
		if overlayDone() {
			appendKey(base[baseIdx])
			baseIdx++
			continue
		}
		baseKey := base[baseIdx]
		overlayKey := nextOverlayKey()
		switch orderedCompare(baseKey, overlayKey, reverse) {
		case 0:
			if s.overlay[overlayKey].exist {
				appendKey(overlayKey)
			}
			baseIdx++
			advanceOverlay()
		case -1:
			appendKey(baseKey)
			baseIdx++
		default:
			if s.overlay[overlayKey].exist {
				appendKey(overlayKey)
			}
			advanceOverlay()
		}
	}
	return out
}

                                                                              
                                                                            
                                                                             
                                                                              
                                                        
func (s *overlayStateView) scanOrderedPairsAfterFilledLocked(prefix, startAfter string, limit int, reverse bool) ([]KVPair, error) {
	if limit <= 0 {
		base, err := s.baseOrderedCachedLocked(prefix, startAfter, limit, reverse)
		if err != nil {
			return nil, err
		}
		return s.mergeOrderedPairsWithOverlay(base, prefix, startAfter, reverse), nil
	}

	baseCursor := startAfter
	chunk := limit
	out := make([]KVPair, 0, limit)
	for {
		page, err := s.baseOrderedCachedLocked(prefix, baseCursor, chunk, reverse)
		if err != nil {
			return nil, err
		}
		if len(page) > chunk {
			page = page[:chunk]
		}
		previous := baseCursor
		for _, pair := range page {
			if !orderedAfter(pair.Key, previous, reverse) {
				return nil, errors.New("stateview: non-monotonic ordered base scan")
			}
			previous = pair.Key
		}
		exhausted := len(page) < chunk
		frontier := ""
		if !exhausted {
			frontier = page[len(page)-1].Key
		}
		out = append(out, s.mergeOrderedPairWindowLocked(page, prefix, baseCursor, frontier, reverse, limit-len(out))...)
		if len(out) >= limit || exhausted {
			return out, nil
		}
		frontier = page[len(page)-1].Key
		baseCursor = frontier
		chunk = growOrderedScanChunk(chunk)
	}
}

func (s *overlayStateView) scanOrderedKeysAfterFilledLocked(prefix, startAfter string, limit int, reverse bool) ([]string, error) {
	if limit <= 0 {
		base, err := s.baseOrderedKeysLocked(prefix, startAfter, limit, reverse)
		if err != nil {
			return nil, err
		}
		return s.mergeOrderedKeysWithOverlay(base, prefix, startAfter, reverse), nil
	}

	baseCursor := startAfter
	chunk := limit
	out := make([]string, 0, limit)
	for {
		page, err := s.baseOrderedKeysLocked(prefix, baseCursor, chunk, reverse)
		if err != nil {
			return nil, err
		}
		if len(page) > chunk {
			page = page[:chunk]
		}
		previous := baseCursor
		for _, key := range page {
			if !orderedAfter(key, previous, reverse) {
				return nil, errors.New("stateview: non-monotonic ordered base key scan")
			}
			previous = key
		}
		exhausted := len(page) < chunk
		frontier := ""
		if !exhausted {
			frontier = page[len(page)-1]
		}
		out = append(out, s.mergeOrderedKeyWindowLocked(page, prefix, baseCursor, frontier, reverse, limit-len(out))...)
		if len(out) >= limit || exhausted {
			return out, nil
		}
		frontier = page[len(page)-1]
		baseCursor = frontier
		chunk = growOrderedScanChunk(chunk)
	}
}

func compareFutureOrderbookKeys(aPrice, aOrder, bPrice, bOrder string, reverse bool) int {
	cmp := 0
	if aPrice < bPrice {
		cmp = -1
	} else if aPrice > bPrice {
		cmp = 1
	} else if aOrder < bOrder {
		cmp = -1
	} else if aOrder > bOrder {
		cmp = 1
	}
	if reverse {
		return -cmp
	}
	return cmp
}

func futureOrderbookEntryAfter(e futureOrderbookEntry, startAfter string, reverse bool) bool {
	if startAfter == "" {
		return true
	}
	_, priceKey, orderID, ok := parseFutureOrderbookKey(startAfter)
	if !ok {
		return true
	}
	return compareFutureOrderbookKeys(e.priceKey, e.orderID, priceKey, orderID, reverse) > 0
}

func canonicalFutureOrderbookCursor(prefix, cursor string) string {
	if cursor == "" {
		return ""
	}
	pfx, priceKey, orderID, ok := parseFutureOrderbookKey(cursor)
	if !ok || pfx != prefix {
		return ""
	}
	canonical := (futureOrderbookEntry{priceKey: priceKey, orderID: orderID}).indexKey(prefix)
	if canonical != cursor {
		return ""
	}
	return canonical
}

func canonicalFutureOrderbookEntryKey(prefix string, entry futureOrderbookEntry) (string, bool) {
	key := entry.indexKey(prefix)
	pfx, priceKey, orderID, ok := parseFutureOrderbookKey(key)
	if !ok || pfx != prefix || priceKey != entry.priceKey || orderID != entry.orderID {
		return "", false
	}
	return key, true
}

func makeOverlayFutureOrderbookEntry(prefix, key string, side int32, v ovVal) (futureOrderbookEntry, bool) {
	pfx, priceKey, orderID, ok := parseFutureOrderbookKey(key)
	if !ok || pfx != prefix || (futureOrderbookEntry{priceKey: priceKey, orderID: orderID}).indexKey(prefix) != key {
		return futureOrderbookEntry{}, false
	}
	return futureOrderbookEntry{
		priceKey: priceKey,
		orderID:  orderID,
		snapshot: compactFutureOrderSnapshot(orderID, side, v.val),
	}, true
}

                                                                               
                                                                            
                                                                         
func (s *overlayStateView) visitOverlayWindowLocked(prefix, startAfter, frontier string, reverse bool, visit func(string) bool) {
	s.ensureOverlayIndexLocked()
	index := s.overlayIndex
	if index == nil || index.tree.Len() == 0 {
		return
	}
	upper, hasUpper := prefixUpperBoundString(prefix)
	walk := func(key string) bool {
		index.rangeVisits++
		if reverse {
			if startAfter != "" && key >= startAfter {
				return true
			}
			if hasUpper && key >= upper {
				return true
			}
			if key < prefix {
				return false
			}
			if frontier != "" && key < frontier {
				return false
			}
			return visit(key)
		}
		if key < prefix || (startAfter != "" && key <= startAfter) {
			return true
		}
		if hasUpper && key >= upper {
			return false
		}
		if frontier != "" && key > frontier {
			return false
		}
		return visit(key)
	}
	if reverse {
		pivot := startAfter
		if hasUpper && (pivot == "" || pivot > upper) {
			pivot = upper
		}
		if pivot != "" {
			index.tree.DescendLessOrEqual(pivot, walk)
		} else {
			index.tree.Descend(walk)
		}
		return
	}
	pivot := prefix
	if startAfter > pivot {
		pivot = startAfter
	}
	index.tree.AscendGreaterOrEqual(pivot, walk)
}

func (s *overlayStateView) mergeFutureOrderbookWindowLocked(base []futureOrderbookEntry, prefix, startAfter, frontier string, reverse bool, limit int) []futureOrderbookEntry {
	_, side, _ := parseFutureOrderbookPrefix(prefix)
	capacity := len(base)
	if limit > 0 && capacity > limit {
		capacity = limit
	}
	out := make([]futureOrderbookEntry, 0, capacity)
	baseIdx := 0
	full := func() bool { return limit > 0 && len(out) >= limit }
	emitBaseBefore := func(priceKey, orderID string) {
		for baseIdx < len(base) && !full() && compareFutureOrderbookKeys(base[baseIdx].priceKey, base[baseIdx].orderID, priceKey, orderID, reverse) < 0 {
			out = append(out, base[baseIdx])
			baseIdx++
		}
	}
	s.visitOverlayWindowLocked(prefix, startAfter, frontier, reverse, func(key string) bool {
		entry, ok := makeOverlayFutureOrderbookEntry(prefix, key, side, s.overlay[key])
		if !ok {
			return true
		}
		emitBaseBefore(entry.priceKey, entry.orderID)
		if full() {
			return false
		}
		if baseIdx < len(base) && compareFutureOrderbookKeys(base[baseIdx].priceKey, base[baseIdx].orderID, entry.priceKey, entry.orderID, reverse) == 0 {
			baseIdx++
		}
		if s.overlay[key].exist {
			out = append(out, entry)
		}
		return !full()
	})
	for baseIdx < len(base) && !full() {
		out = append(out, base[baseIdx])
		baseIdx++
	}
	return out
}

type futureOrderbookPageScan func(prefix, startAfter string, limit int, reverse bool) ([]futureOrderbookEntry, bool, error)

func (s *overlayStateView) scanFutureOrderbookAfterFilledLocked(prefix, startAfter string, limit int, reverse bool, scanPage futureOrderbookPageScan) ([]futureOrderbookEntry, bool, error) {
	baseCursor := canonicalFutureOrderbookCursor(prefix, startAfter)
	if limit <= 0 {
		base, ok, err := scanPage(prefix, baseCursor, limit, reverse)
		if err != nil || !ok {
			return nil, ok, err
		}
		previous := baseCursor
		for _, entry := range base {
			key, valid := canonicalFutureOrderbookEntryKey(prefix, entry)
			if !valid || !futureOrderbookEntryAfter(entry, previous, reverse) {
				return nil, true, errors.New("stateview: non-monotonic future orderbook base scan")
			}
			previous = key
		}
		return s.mergeFutureOrderbookWindowLocked(base, prefix, baseCursor, "", reverse, limit), true, nil
	}

	out := make([]futureOrderbookEntry, 0, limit)
	chunk := limit
	for len(out) < limit {
		base, ok, err := scanPage(prefix, baseCursor, chunk, reverse)
		if err != nil || !ok {
			return nil, ok, err
		}
		if len(base) > chunk {
			base = base[:chunk]
		}
		previous := baseCursor
		for _, entry := range base {
			key, valid := canonicalFutureOrderbookEntryKey(prefix, entry)
			if !valid || !futureOrderbookEntryAfter(entry, previous, reverse) {
				return nil, true, errors.New("stateview: non-monotonic future orderbook base scan")
			}
			previous = key
		}
		exhausted := len(base) < chunk
		frontier := ""
		if !exhausted {
			frontier, _ = canonicalFutureOrderbookEntryKey(prefix, base[len(base)-1])
		}
		out = append(out, s.mergeFutureOrderbookWindowLocked(base, prefix, baseCursor, frontier, reverse, limit-len(out))...)
		if len(out) >= limit || exhausted {
			return out, true, nil
		}
		baseCursor = frontier
		chunk = growOrderedScanChunk(chunk)
	}
	return out, true, nil
}

func (s *overlayStateView) VisitFutureOrderbookSnapshots(prefix, startAfter string, limit int, reverse bool, visit FutureOrderbookVisitFn) (bool, error) {
	if s.scanFutureBookVisit == nil || !isFutureOrderbookPriceIndexPrefix(prefix) {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	scanPage := func(prefix, startAfter string, limit int, reverse bool) ([]futureOrderbookEntry, bool, error) {
		capacity := limit
		if capacity <= 0 || capacity > 4096 {
			capacity = 4096
		}
		page := make([]futureOrderbookEntry, 0, capacity)
		ok, err := s.scanFutureBookVisit(prefix, startAfter, limit, reverse, func(entry futureOrderbookEntry) bool {
			page = append(page, entry)
			return limit <= 0 || len(page) < limit
		})
		return page, ok, err
	}
	page, ok, err := s.scanFutureOrderbookAfterFilledLocked(prefix, startAfter, limit, reverse, scanPage)
	if err != nil || !ok {
		return ok, err
	}
	for _, entry := range page {
		if !visit(entry) {
			break
		}
	}
	return true, nil
}

func (s *overlayStateView) ScanFutureOrderbookSnapshots(prefix, startAfter string, limit int, reverse bool) ([]futureOrderbookEntry, bool, error) {
	if s.scanFutureBook == nil || !isFutureOrderbookPriceIndexPrefix(prefix) {
		return nil, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scanFutureOrderbookAfterFilledLocked(prefix, startAfter, limit, reverse, futureOrderbookPageScan(s.scanFutureBook))
}

func (s *overlayStateView) ScanWithLimitOrdered(prefix string, limit int, reverse bool) ([]KVPair, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	baseLimit := s.effectiveBaseLimitLocked(prefix, "", limit, reverse)
	base, err := s.baseOrderedCachedLocked(prefix, "", baseLimit, reverse)
	if err != nil {
		return nil, err
	}

	return s.mergeOrderedPairsWithOverlay(base, prefix, "", reverse), nil
}

func (s *overlayStateView) ScanKeysWithLimitOrdered(prefix string, limit int, reverse bool) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	baseLimit := s.effectiveBaseLimitLocked(prefix, "", limit, reverse)
	base, err := s.baseOrderedKeysLocked(prefix, "", baseLimit, reverse)
	if err != nil {
		return nil, err
	}
	return s.mergeOrderedKeysWithOverlay(base, prefix, "", reverse), nil
}

func (s *overlayStateView) ScanWithLimitOrderedAfter(prefix, startAfter string, limit int, reverse bool) ([]KVPair, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scanOrderedPairsAfterFilledLocked(prefix, startAfter, limit, reverse)
}

func (s *overlayStateView) ScanKeysWithLimitOrderedAfter(prefix, startAfter string, limit int, reverse bool) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scanOrderedKeysAfterFilledLocked(prefix, startAfter, limit, reverse)
}

                                                                                                
func (s *overlayStateView) ScanWithLimit(prefix string, limit int) (map[string][]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	baseLimit := s.effectiveBaseLimitLocked(prefix, "", limit, false)
	base, err := s.baseOrderedCachedLocked(prefix, "", baseLimit, false)
	if err != nil {
		return nil, err
	}
	result := orderedPairsToMap(base)
	s.mergeOverlay(result, prefix)
	return result, nil
}

                                                                                                       
func (s *overlayStateView) ScanWithLimitReverse(prefix string, limit int) (map[string][]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	baseLimit := s.effectiveBaseLimitLocked(prefix, "", limit, true)
	base, err := s.baseOrderedCachedLocked(prefix, "", baseLimit, true)
	if err != nil {
		return nil, err
	}
	result := orderedPairsToMap(base)
	s.mergeOverlay(result, prefix)
	return result, nil
}

func (s *overlayStateView) Del(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	prev, has := s.overlay[key]
	s.changelog = append(s.changelog, change{key: key, prev: prev, hasPrev: has})
	if !has {
		s.insertOverlayKeyLocked(key)
	}
	s.overlay[key] = ovVal{val: nil, exist: false}
	s.revision++
}

func (s *overlayStateView) Snapshot() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.changelog)
}

                                                                             
                                                                              
                                               
func (s *overlayStateView) Revision() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.revision
}

func (s *overlayStateView) Revert(snap int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if snap < 0 || snap > len(s.changelog) {
		return ErrInvalidSnapshot
	}

	                               
	for i := len(s.changelog) - 1; i >= snap; i-- {
		c := s.changelog[i]
		if c.hasPrev {
			if _, ok := s.overlay[c.key]; !ok {
				s.insertOverlayKeyLocked(c.key)
			}
			s.overlay[c.key] = c.prev
		} else {
			delete(s.overlay, c.key)
			s.removeOverlayKeyLocked(c.key)
		}
	}
	s.changelog = s.changelog[:snap]
	for i := len(s.activitySeqLog) - 1; i >= 0; i-- {
		ch := s.activitySeqLog[i]
		if ch.snap < snap {
			break
		}
		if ch.had {
			s.activitySeq[ch.prefix] = ch.prev
		} else {
			delete(s.activitySeq, ch.prefix)
		}
		s.activitySeqLog = s.activitySeqLog[:i]
	}
	s.revision++
	return nil
}

func (s *overlayStateView) ReleaseHistory() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.changelog {
		s.changelog[i] = change{}
	}
	s.changelog = s.changelog[:0]
	for i := range s.activitySeqLog {
		s.activitySeqLog[i] = activitySeqChange{}
	}
	s.activitySeqLog = s.activitySeqLog[:0]
	s.revision++
}

func (s *overlayStateView) ChangedKeysSince(snap int) map[string]struct{} {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if snap < 0 {
		snap = 0
	}
	if snap > len(s.changelog) {
		snap = len(s.changelog)
	}
	changed := make(map[string]struct{}, len(s.changelog)-snap)
	for _, c := range s.changelog[snap:] {
		changed[c.key] = struct{}{}
	}
	return changed
}

func (s *overlayStateView) HasPendingWrite(key string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.overlay[key]
	return ok
}

func (s *overlayStateView) PrefetchMissingOldValues() error {
	s.mu.RLock()
	missing := make([]string, 0, len(s.overlay))
	for k := range s.overlay {
		if _, ok := s.baseGetCache[k]; ok || !keypkg.IsStatefulKey(k) {
			continue
		}
		missing = append(missing, k)
	}
	sort.Strings(missing)
	batchRead := s.baseBatchRead
	read := s.read
	s.mu.RUnlock()
	if len(missing) == 0 {
		return nil
	}
	if batchRead != nil {
		values, err := batchRead(missing)
		if err != nil {
			return err
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, k := range missing {
			if _, ok := s.baseGetCache[k]; ok {
				continue
			}
			v, exists := values[k]
			s.rememberBaseValueLocked(k, v, exists)
		}
		return nil
	}
	if read == nil {
		return nil
	}
	values := make(map[string]ovVal, len(missing))
	for _, k := range missing {
		v, err := read(k)
		if err != nil {
			if isStateViewNotFound(err) {
				values[k] = ovVal{exist: false}
				continue
			}
			return err
		}
		values[k] = ovVal{val: cloneBytes(v), exist: v != nil}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range missing {
		if _, ok := s.baseGetCache[k]; ok {
			continue
		}
		v := values[k]
		s.rememberBaseValueLocked(k, v.val, v.exist)
	}
	return nil
}

func (s *overlayStateView) Diff() []WriteOp {
	s.mu.RLock()

	                                                            
	orderedKeys := make([]string, 0, len(s.overlay))
	for k := range s.overlay {
		orderedKeys = append(orderedKeys, k)
	}
	sort.Strings(orderedKeys)
	if len(orderedKeys) >= stateViewParallelDiffMinKeys {
		if inputs, workers, ok := s.diffInputsForParallelLocked(orderedKeys); ok {
			s.mu.RUnlock()
			diff := diffInputsParallel(inputs, workers)
			return diff
		}
	}

	diff := make([]WriteOp, 0, len(s.overlay))
	for _, k := range orderedKeys {
		v := s.overlay[k]
		                                                        
		op := WriteOp{
			Key:   k,
			Value: v.val,
			Del:   !v.exist,
		}
		if old, ok := s.baseGetCache[k]; ok {
			op.OldValue = cloneBytes(old.val)
			op.OldExists = old.exist
			op.HasOldValue = true
			op.OldValueAuthoritative = true
		}
		diff = append(diff, op)
	}
	s.mu.RUnlock()
	return diff
}

type stateViewDiffInput struct {
	key    string
	value  []byte
	del    bool
	old    ovVal
	hasOld bool
}

func (s *overlayStateView) diffInputsForParallelLocked(orderedKeys []string) ([]stateViewDiffInput, int, bool) {
	workers := runtime.GOMAXPROCS(0)
	if workers > stateViewParallelDiffMaxWork {
		workers = stateViewParallelDiffMaxWork
	}
	if workers < 2 || len(orderedKeys) < workers*2 {
		return nil, 0, false
	}
	inputs := make([]stateViewDiffInput, len(orderedKeys))
	for i, k := range orderedKeys {
		v := s.overlay[k]
		inputs[i] = stateViewDiffInput{key: k, value: v.val, del: !v.exist}
		if old, ok := s.baseGetCache[k]; ok {
			inputs[i].old = old
			inputs[i].hasOld = true
		}
	}
	return inputs, workers, true
}

func diffInputsParallel(inputs []stateViewDiffInput, workers int) []WriteOp {
	chunk := (len(inputs) + workers - 1) / workers
	parts := make([][]WriteOp, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		start := i * chunk
		if start >= len(inputs) {
			parts = parts[:i]
			break
		}
		end := start + chunk
		if end > len(inputs) {
			end = len(inputs)
		}
		wg.Add(1)
		go func(idx, start, end int) {
			defer wg.Done()
			part := make([]WriteOp, 0, end-start)
			for _, in := range inputs[start:end] {
				op := WriteOp{Key: in.key, Value: in.value, Del: in.del}
				if in.hasOld {
					op.OldValue = cloneBytes(in.old.val)
					op.OldExists = in.old.exist
					op.HasOldValue = true
					op.OldValueAuthoritative = true
				}
				part = append(part, op)
			}
			parts[idx] = part
		}(i, start, end)
	}
	wg.Wait()
	diff := make([]WriteOp, 0, len(inputs))
	for _, part := range parts {
		diff = append(diff, part...)
	}
	return diff
}
