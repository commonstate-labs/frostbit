                                         
                                                          
package adapters

import (
	"dex/frost/runtime"
	iface "dex/interfaces"
	"sort"
	"strings"
)

                                                      
type DBGetter interface {
	Get(key string) ([]byte, error)
	                                            
	Scan(prefix string) (map[string][]byte, error)
}

type dbBatchGetter interface {
	GetKVs(keyList []string) (map[string][]byte, error)
}

type dbOrderedScanner interface {
	ScanKVWithLimitOrderedAfter(prefix, startAfter string, limit int, reverse bool) ([]iface.KVPair, error)
}

                                                              
type StateDBReader struct {
	db DBGetter
}

                                              
func NewStateDBReader(db DBGetter) *StateDBReader {
	return &StateDBReader{db: db}
}

                              
func (r *StateDBReader) Get(key string) ([]byte, bool, error) {
	data, err := r.db.Get(key)
	if err != nil {
		                                      
		if isNotFoundError(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if len(data) == 0 {
		return nil, false, nil
	}
	return data, true, nil
}

                                            
func (r *StateDBReader) Scan(prefix string, fn func(k string, v []byte) bool) error {
	results, err := r.db.Scan(prefix)
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(results))
	for k := range results {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := results[k]
		if !fn(k, v) {
			break
		}
	}
	return nil
}

func (r *StateDBReader) ScanOrderedAfter(prefix, startAfter string, limit int, reverse bool, fn func(k string, v []byte) bool) error {
	if ordered, ok := r.db.(dbOrderedScanner); ok {
		pairs, err := ordered.ScanKVWithLimitOrderedAfter(prefix, startAfter, limit, reverse)
		if err != nil {
			return err
		}
		for _, pair := range pairs {
			if !fn(pair.Key, pair.Value) {
				break
			}
		}
		return nil
	}

	seen := 0
	return r.Scan(prefix, func(k string, v []byte) bool {
		if startAfter != "" {
			if reverse {
				if k >= startAfter {
					return true
				}
			} else if k <= startAfter {
				return true
			}
		}
		if limit > 0 && seen >= limit {
			return false
		}
		seen++
		return fn(k, v)
	})
}

                                                              
func (r *StateDBReader) GetMany(keys []string) (map[string][]byte, error) {
	out := make(map[string][]byte, len(keys))
	if len(keys) == 0 {
		return out, nil
	}

	if batchDB, ok := r.db.(dbBatchGetter); ok {
		kvs, err := batchDB.GetKVs(keys)
		if err != nil {
			return nil, err
		}
		for _, key := range keys {
			if key == "" {
				continue
			}
			val, ok := kvs[key]
			if !ok || len(val) == 0 {
				continue
			}
			v := make([]byte, len(val))
			copy(v, val)
			out[key] = v
		}
		return out, nil
	}

	for _, key := range keys {
		if key == "" {
			continue
		}
		data, err := r.db.Get(key)
		if err != nil {
			if isNotFoundError(err) {
				continue
			}
			return nil, err
		}
		if len(data) == 0 {
			continue
		}
		v := make([]byte, len(data))
		copy(v, data)
		out[key] = v
	}
	return out, nil
}

                                                      
func isNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "not found") ||
		strings.Contains(errStr, "key not found") ||
		strings.Contains(errStr, "does not exist")
}

                                                           
var _ runtime.ChainStateReader = (*StateDBReader)(nil)

                                                   
type FakeStateReader struct {
	data map[string][]byte
}

                                                  
func NewFakeStateReader() *FakeStateReader {
	return &FakeStateReader{
		data: make(map[string][]byte),
	}
}

                                     
func (r *FakeStateReader) Set(key string, value []byte) {
	r.data[key] = value
}

                              
func (r *FakeStateReader) Get(key string) ([]byte, bool, error) {
	v, ok := r.data[key]
	if !ok || len(v) == 0 {
		return nil, false, nil
	}
	return v, true, nil
}

                                            
func (r *FakeStateReader) Scan(prefix string, fn func(k string, v []byte) bool) error {
	keys := make([]string, 0, len(r.data))
	for k := range r.data {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !fn(k, r.data[k]) {
			break
		}
	}
	return nil
}

func (r *FakeStateReader) ScanOrderedAfter(prefix, startAfter string, limit int, reverse bool, fn func(k string, v []byte) bool) error {
	keys := make([]string, 0, len(r.data))
	for k := range r.data {
		if strings.HasPrefix(k, prefix) {
			if startAfter != "" {
				if reverse {
					if k >= startAfter {
						continue
					}
				} else if k <= startAfter {
					continue
				}
			}
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	if reverse {
		for i, j := 0, len(keys)-1; i < j; i, j = i+1, j-1 {
			keys[i], keys[j] = keys[j], keys[i]
		}
	}
	if limit > 0 && len(keys) > limit {
		keys = keys[:limit]
	}
	for _, k := range keys {
		if !fn(k, r.data[k]) {
			break
		}
	}
	return nil
}

                                                              
func (r *FakeStateReader) GetMany(keys []string) (map[string][]byte, error) {
	out := make(map[string][]byte, len(keys))
	for _, key := range keys {
		if key == "" {
			continue
		}
		if v, ok := r.data[key]; ok && len(v) > 0 {
			out[key] = v
		}
	}
	return out, nil
}

                                                             
var _ runtime.ChainStateReader = (*FakeStateReader)(nil)
