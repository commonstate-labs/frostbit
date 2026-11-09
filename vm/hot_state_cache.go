package vm

import (
	"dex/keys"
	"sort"
	"strings"
	"sync"
)

type hotStateScanLoader func(prefix string) (map[string][]byte, error)

                                                                                               
type HotStateCache struct {
	mu            sync.RWMutex
	prefixes      map[string]*hotPrefixIndex
	prefixBuckets map[string]map[string]*hotPrefixIndex
	futureBooks   map[string]*hotFutureOrderbookIndex
	keys          map[string]hotKeyValue
	keyOrder      []string
}

type hotPrefixIndex struct {
	keys     []string
	values   map[string][]byte
	keysOnly bool
}

type hotKeyValue struct {
	value  []byte
	exists bool
}

const hotStateMaxDirectKeys = 131072

func NewHotStateCache() *HotStateCache {
	return &HotStateCache{
		prefixes:      make(map[string]*hotPrefixIndex),
		prefixBuckets: make(map[string]map[string]*hotPrefixIndex),
		futureBooks:   make(map[string]*hotFutureOrderbookIndex),
		keys:          make(map[string]hotKeyValue),
		keyOrder:      make([]string, 0, 1024),
	}
}

func (c *HotStateCache) Reset() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prefixes = make(map[string]*hotPrefixIndex)
	c.prefixBuckets = make(map[string]map[string]*hotPrefixIndex)
	c.futureBooks = make(map[string]*hotFutureOrderbookIndex)
	c.keys = make(map[string]hotKeyValue)
	c.keyOrder = c.keyOrder[:0]
}

func (c *HotStateCache) Get(key string, load func(string) ([]byte, error)) ([]byte, bool, error) {
	if c == nil || !isHotStateKey(key) {
		return nil, false, nil
	}
	c.mu.RLock()
	if v, ok := c.keys[key]; ok {
		out := cloneBytes(v.value)
		c.mu.RUnlock()
		if !v.exists {
			return nil, true, nil
		}
		return out, true, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := c.keys[key]; ok {
		if !v.exists {
			return nil, true, nil
		}
		return cloneBytes(v.value), true, nil
	}
	val, err := load(key)
	if err != nil {
		return nil, true, err
	}
	c.rememberKeyLocked(key, val, val != nil)
	if val == nil {
		return nil, true, nil
	}
	return cloneBytes(val), true, nil
}

func (c *HotStateCache) Scan(prefix string, load hotStateScanLoader) (map[string][]byte, bool, error) {
	if c == nil || !isHotStatePrefix(prefix) {
		return nil, false, nil
	}
	if isFutureOrderbookPriceIndexPrefix(prefix) {
		c.mu.RLock()
		idx := c.futureBooks[prefix]
		if idx != nil {
			out := idx.toMap()
			c.mu.RUnlock()
			return out, true, nil
		}
		c.mu.RUnlock()

		c.mu.Lock()
		defer c.mu.Unlock()
		if idx = c.futureBooks[prefix]; idx == nil {
			kvs, err := load(prefix)
			if err != nil {
				return nil, true, err
			}
			idx = newHotFutureOrderbookIndex(prefix, kvs)
			c.rememberFutureBookLocked(prefix, idx)
		}
		return idx.toMap(), true, nil
	}
	c.mu.RLock()
	idx := c.prefixes[prefix]
	if idx != nil {
		if idx.keysOnly {
			c.mu.RUnlock()
			c.mu.Lock()
			defer c.mu.Unlock()
			if idx = c.prefixes[prefix]; idx == nil || idx.keysOnly {
				kvs, err := load(prefix)
				if err != nil {
					return nil, true, err
				}
				idx = newHotPrefixIndex(kvs)
				c.rememberPrefixLocked(prefix, idx)
			}
			return idx.toMap(), true, nil
		}
		out := idx.toMap()
		c.mu.RUnlock()
		return out, true, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	if idx = c.prefixes[prefix]; idx == nil {
		kvs, err := load(prefix)
		if err != nil {
			return nil, true, err
		}
		idx = newHotPrefixIndex(kvs)
		c.rememberPrefixLocked(prefix, idx)
	}
	return idx.toMap(), true, nil
}

func (c *HotStateCache) ScanOrderedAfter(prefix, startAfter string, limit int, reverse bool, load hotStateScanLoader) ([]KVPair, bool, error) {
	if c == nil || !isHotStatePrefix(prefix) {
		return nil, false, nil
	}
	if isFutureOrderbookPriceIndexPrefix(prefix) {
		c.mu.RLock()
		idx := c.futureBooks[prefix]
		if idx != nil {
			out := idx.scanOrderedAfter(startAfter, limit, reverse)
			c.mu.RUnlock()
			return out, true, nil
		}
		c.mu.RUnlock()

		c.mu.Lock()
		defer c.mu.Unlock()
		if idx = c.futureBooks[prefix]; idx == nil {
			kvs, err := load(prefix)
			if err != nil {
				return nil, true, err
			}
			idx = newHotFutureOrderbookIndex(prefix, kvs)
			c.rememberFutureBookLocked(prefix, idx)
		}
		return idx.scanOrderedAfter(startAfter, limit, reverse), true, nil
	}
	c.mu.RLock()
	idx := c.prefixes[prefix]
	if idx != nil {
		if idx.keysOnly {
			c.mu.RUnlock()
			c.mu.Lock()
			defer c.mu.Unlock()
			if idx = c.prefixes[prefix]; idx == nil || idx.keysOnly {
				kvs, err := load(prefix)
				if err != nil {
					return nil, true, err
				}
				idx = newHotPrefixIndex(kvs)
				c.rememberPrefixLocked(prefix, idx)
			}
			return idx.scanOrderedAfter(startAfter, limit, reverse), true, nil
		}
		out := idx.scanOrderedAfter(startAfter, limit, reverse)
		c.mu.RUnlock()
		return out, true, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	if idx = c.prefixes[prefix]; idx == nil {
		kvs, err := load(prefix)
		if err != nil {
			return nil, true, err
		}
		idx = newHotPrefixIndex(kvs)
		c.rememberPrefixLocked(prefix, idx)
	}
	return idx.scanOrderedAfter(startAfter, limit, reverse), true, nil
}

func (c *HotStateCache) ScanFutureOrderbookSnapshots(prefix, startAfter string, limit int, reverse bool, load hotStateScanLoader) ([]futureOrderbookEntry, bool, error) {
	if c == nil || !isFutureOrderbookPriceIndexPrefix(prefix) {
		return nil, false, nil
	}
	c.mu.RLock()
	idx := c.futureBooks[prefix]
	if idx != nil {
		out := idx.scanSnapshotsAfter(startAfter, limit, reverse)
		c.mu.RUnlock()
		return out, true, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	if idx = c.futureBooks[prefix]; idx == nil {
		kvs, err := load(prefix)
		if err != nil {
			return nil, true, err
		}
		idx = newHotFutureOrderbookIndex(prefix, kvs)
		c.rememberFutureBookLocked(prefix, idx)
	}
	return idx.scanSnapshotsAfter(startAfter, limit, reverse), true, nil
}

func (c *HotStateCache) VisitFutureOrderbookSnapshots(prefix, startAfter string, limit int, reverse bool, load hotStateScanLoader, visit FutureOrderbookVisitFn) (bool, error) {
	if c == nil || !isFutureOrderbookPriceIndexPrefix(prefix) {
		return false, nil
	}
	c.mu.RLock()
	idx := c.futureBooks[prefix]
	if idx != nil {
		idx.visitSnapshotsAfter(startAfter, limit, reverse, visit)
		c.mu.RUnlock()
		return true, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	if idx = c.futureBooks[prefix]; idx == nil {
		kvs, err := load(prefix)
		if err != nil {
			return true, err
		}
		idx = newHotFutureOrderbookIndex(prefix, kvs)
		c.rememberFutureBookLocked(prefix, idx)
	}
	idx.visitSnapshotsAfter(startAfter, limit, reverse, visit)
	return true, nil
}

func (c *HotStateCache) ScanKeysOrderedAfter(prefix, startAfter string, limit int, reverse bool, load hotStateScanLoader) ([]string, bool, error) {
	if c == nil || !isHotStatePrefix(prefix) {
		return nil, false, nil
	}
	if isFutureOrderbookPriceIndexPrefix(prefix) {
		c.mu.RLock()
		idx := c.futureBooks[prefix]
		if idx != nil {
			out := idx.scanKeysOrderedAfter(startAfter, limit, reverse)
			c.mu.RUnlock()
			return out, true, nil
		}
		c.mu.RUnlock()

		c.mu.Lock()
		defer c.mu.Unlock()
		if idx = c.futureBooks[prefix]; idx == nil {
			kvs, err := load(prefix)
			if err != nil {
				return nil, true, err
			}
			idx = newHotFutureOrderbookIndex(prefix, kvs)
			c.rememberFutureBookLocked(prefix, idx)
		}
		return idx.scanKeysOrderedAfter(startAfter, limit, reverse), true, nil
	}
	c.mu.RLock()
	idx := c.prefixes[prefix]
	if idx != nil {
		out := idx.scanKeysOrderedAfter(startAfter, limit, reverse)
		c.mu.RUnlock()
		return out, true, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	if idx = c.prefixes[prefix]; idx == nil {
		kvs, err := load(prefix)
		if err != nil {
			return nil, true, err
		}
		idx = newHotPrefixKeyIndex(kvs)
		c.rememberPrefixLocked(prefix, idx)
	}
	return idx.scanKeysOrderedAfter(startAfter, limit, reverse), true, nil
}

func (c *HotStateCache) ApplyDiff(diff []WriteOp) {
	if c == nil || len(diff) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, op := range diff {
		if op.Key == "" {
			continue
		}
		if _, ok := c.keys[op.Key]; ok {
			if op.Del {
				delete(c.keys, op.Key)
			} else {
				c.keys[op.Key] = hotKeyValue{value: cloneBytes(op.Value), exists: true}
			}
		}
		if prefix, ok := hotOrderbookPriceIndexPrefixFromKey(op.Key); ok {
			if isFutureOrderbookPriceIndexPrefix(prefix) {
				if idx := c.futureBooks[prefix]; idx != nil {
					idx.apply(op)
				}
				continue
			}
			if idx := c.prefixes[prefix]; idx != nil {
				idx.apply(op)
			}
			continue
		}
		if strings.HasPrefix(op.Key, keys.KeyFutureMarketConfigPrefix()) {
			if idx := c.prefixes[keys.KeyFutureMarketConfigPrefix()]; idx != nil {
				idx.apply(op)
			}
			continue
		}
		bucket := hotPrefixBucketForKey(op.Key)
		if bucket == "" {
			continue
		}
		for prefix, idx := range c.prefixBuckets[bucket] {
			if strings.HasPrefix(op.Key, prefix) {
				idx.apply(op)
			}
		}
	}
}

func (x *Executor) ResetHotStateCache() {
	if x == nil || x.HotState == nil {
		return
	}
	x.HotState.Reset()
}

func (x *Executor) applyHotStateDiff(diff []WriteOp) {
	if x == nil || x.HotState == nil {
		return
	}
	x.HotState.ApplyDiff(diff)
}

func (c *HotStateCache) getOrLoadPrefix(prefix string, load hotStateScanLoader) (*hotPrefixIndex, error) {
	c.mu.RLock()
	idx := c.prefixes[prefix]
	if idx != nil {
		out := idx.clone()
		c.mu.RUnlock()
		return out, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	if idx = c.prefixes[prefix]; idx != nil {
		return idx.clone(), nil
	}
	kvs, err := load(prefix)
	if err != nil {
		return nil, err
	}
	idx = newHotPrefixIndex(kvs)
	c.rememberPrefixLocked(prefix, idx)
	return idx.clone(), nil
}

func (c *HotStateCache) rememberPrefixLocked(prefix string, idx *hotPrefixIndex) {
	c.prefixes[prefix] = idx
	bucket := hotPrefixBucket(prefix)
	if bucket == "" {
		return
	}
	if c.prefixBuckets == nil {
		c.prefixBuckets = make(map[string]map[string]*hotPrefixIndex)
	}
	m := c.prefixBuckets[bucket]
	if m == nil {
		m = make(map[string]*hotPrefixIndex)
		c.prefixBuckets[bucket] = m
	}
	m[prefix] = idx
}

func (c *HotStateCache) rememberFutureBookLocked(prefix string, idx *hotFutureOrderbookIndex) {
	if c.futureBooks == nil {
		c.futureBooks = make(map[string]*hotFutureOrderbookIndex)
	}
	c.futureBooks[prefix] = idx
}

func newHotPrefixIndex(kvs map[string][]byte) *hotPrefixIndex {
	idx := &hotPrefixIndex{
		keys:   make([]string, 0, len(kvs)),
		values: make(map[string][]byte, len(kvs)),
	}
	for k, v := range kvs {
		idx.keys = append(idx.keys, k)
		idx.values[k] = cloneBytes(v)
	}
	sort.Strings(idx.keys)
	return idx
}

func newHotPrefixKeyIndex(kvs map[string][]byte) *hotPrefixIndex {
	idx := &hotPrefixIndex{
		keys:     make([]string, 0, len(kvs)),
		keysOnly: true,
	}
	for k := range kvs {
		idx.keys = append(idx.keys, k)
	}
	sort.Strings(idx.keys)
	return idx
}

func (idx *hotPrefixIndex) clone() *hotPrefixIndex {
	if idx == nil {
		return newHotPrefixIndex(nil)
	}
	out := &hotPrefixIndex{
		keys:     append([]string(nil), idx.keys...),
		keysOnly: idx.keysOnly,
	}
	if !idx.keysOnly {
		out.values = make(map[string][]byte, len(idx.values))
		for k, v := range idx.values {
			out.values[k] = cloneBytes(v)
		}
	}
	return out
}

func (idx *hotPrefixIndex) toMap() map[string][]byte {
	out := make(map[string][]byte, len(idx.keys))
	if idx.keysOnly {
		for _, k := range idx.keys {
			out[k] = nil
		}
		return out
	}
	for k, v := range idx.values {
		out[k] = cloneBytes(v)
	}
	return out
}

func (idx *hotPrefixIndex) scanOrderedAfter(startAfter string, limit int, reverse bool) []KVPair {
	if idx == nil || len(idx.keys) == 0 {
		return nil
	}
	out := make([]KVPair, 0, scanOutputCap(len(idx.keys), limit))
	if reverse {
		start := len(idx.keys) - 1
		if startAfter != "" {
			start = sort.SearchStrings(idx.keys, startAfter) - 1
		}
		for i := start; i >= 0; i-- {
			k := idx.keys[i]
			out = append(out, KVPair{Key: k, Value: cloneBytes(idx.values[k])})
			if limit > 0 && len(out) >= limit {
				break
			}
		}
		return out
	}
	start := 0
	if startAfter != "" {
		start = sort.Search(len(idx.keys), func(i int) bool { return idx.keys[i] > startAfter })
	}
	for i := start; i < len(idx.keys); i++ {
		k := idx.keys[i]
		out = append(out, KVPair{Key: k, Value: cloneBytes(idx.values[k])})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func (idx *hotPrefixIndex) scanKeysOrderedAfter(startAfter string, limit int, reverse bool) []string {
	if idx == nil || len(idx.keys) == 0 {
		return nil
	}
	out := make([]string, 0, scanOutputCap(len(idx.keys), limit))
	if reverse {
		start := len(idx.keys) - 1
		if startAfter != "" {
			start = sort.SearchStrings(idx.keys, startAfter) - 1
		}
		for i := start; i >= 0; i-- {
			out = append(out, idx.keys[i])
			if limit > 0 && len(out) >= limit {
				break
			}
		}
		return out
	}
	start := 0
	if startAfter != "" {
		start = sort.Search(len(idx.keys), func(i int) bool { return idx.keys[i] > startAfter })
	}
	for i := start; i < len(idx.keys); i++ {
		out = append(out, idx.keys[i])
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func (idx *hotPrefixIndex) apply(op WriteOp) {
	pos := sort.SearchStrings(idx.keys, op.Key)
	exists := pos < len(idx.keys) && idx.keys[pos] == op.Key
	if op.Del {
		if exists {
			copy(idx.keys[pos:], idx.keys[pos+1:])
			idx.keys[len(idx.keys)-1] = ""
			idx.keys = idx.keys[:len(idx.keys)-1]
		}
		delete(idx.values, op.Key)
		return
	}
	if !exists {
		idx.keys = append(idx.keys, "")
		copy(idx.keys[pos+1:], idx.keys[pos:])
		idx.keys[pos] = op.Key
	}
	if idx.keysOnly {
		return
	}
	idx.values[op.Key] = cloneBytes(op.Value)
}

func (c *HotStateCache) rememberKeyLocked(key string, value []byte, exists bool) {
	if _, ok := c.keys[key]; !ok {
		c.keyOrder = append(c.keyOrder, key)
	}
	c.keys[key] = hotKeyValue{value: cloneBytes(value), exists: exists}
	for len(c.keys) > hotStateMaxDirectKeys && len(c.keyOrder) > 0 {
		victim := c.keyOrder[0]
		c.keyOrder[0] = ""
		c.keyOrder = c.keyOrder[1:]
		delete(c.keys, victim)
	}
	if len(c.keyOrder) > hotStateMaxDirectKeys*2 {
		compacted := c.keyOrder[:0]
		for _, k := range c.keyOrder {
			if _, ok := c.keys[k]; ok {
				compacted = append(compacted, k)
			}
		}
		c.keyOrder = compacted
	}
}

func scanOutputCap(total, limit int) int {
	if limit <= 0 || limit > total {
		return total
	}
	return limit
}

func isHotStatePrefix(prefix string) bool {
	if prefix == "" {
		return false
	}
	switch {
	case isOrderbookPriceIndexPrefix(prefix):
		return true
	case strings.HasPrefix(prefix, "v1_future_pair_pos_"):
		return true
	case prefix == keys.KeyFutureMarketConfigPrefix():
		return true
	case strings.HasPrefix(prefix, "v1_future_liq_long_") || strings.HasPrefix(prefix, "v1_future_liq_short_"):
		return true
	default:
		return false
	}
}

func isOrderbookPriceIndexPrefix(prefix string) bool {
	if !(strings.HasPrefix(prefix, "v1_pair:") || strings.HasPrefix(prefix, "v1_future_pair:")) {
		return false
	}
	return strings.Contains(prefix, "|side:") && strings.Contains(prefix, "|is_filled:")
}

func isHotStateKey(key string) bool {
	switch {
	case strings.HasPrefix(key, keys.KeyOrderStatePrefix()):
		return true
	case strings.HasPrefix(key, keys.KeyFutureOrderStatePrefix()):
		return true
	case strings.HasPrefix(key, keys.KeyFuturePositionPrefix()):
		return true
	case strings.HasPrefix(key, "v1_future_owner_pair_side_"):
		return true
	case strings.HasPrefix(key, keys.KeyFutureMarketConfigPrefix()):
		return true
	case strings.HasPrefix(key, "v1_future_liq_ref_"):
		return true
	default:
		return false
	}
}

func hotOrderbookPriceIndexPrefixFromKey(key string) (string, bool) {
	if !(strings.HasPrefix(key, "v1_pair:") || strings.HasPrefix(key, "v1_future_pair:")) {
		return "", false
	}
	i := strings.Index(key, "|price:")
	if i <= 0 {
		return "", false
	}
	prefix := key[:i+1]
	if !isOrderbookPriceIndexPrefix(prefix) {
		return "", false
	}
	return prefix, true
}

func hotPrefixBucket(prefix string) string {
	switch {
	case strings.HasPrefix(prefix, "v1_future_pair_pos_"):
		return "v1_future_pair_pos_"
	case strings.HasPrefix(prefix, "v1_future_liq_long_"):
		return "v1_future_liq_long_"
	case strings.HasPrefix(prefix, "v1_future_liq_short_"):
		return "v1_future_liq_short_"
	default:
		return ""
	}
}

func hotPrefixBucketForKey(key string) string {
	switch {
	case strings.HasPrefix(key, "v1_future_pair_pos_"):
		return "v1_future_pair_pos_"
	case strings.HasPrefix(key, "v1_future_owner_pair_side_"):
		return ""
	case strings.HasPrefix(key, "v1_future_liq_long_"):
		return "v1_future_liq_long_"
	case strings.HasPrefix(key, "v1_future_liq_short_"):
		return "v1_future_liq_short_"
	default:
		return ""
	}
}
