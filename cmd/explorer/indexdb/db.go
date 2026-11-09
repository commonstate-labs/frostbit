package indexdb

import (
	"dex/db"
	"fmt"
	"os"
	"runtime"
	"sync"

	"github.com/cockroachdb/pebble"
)

const (
	indexDBMemTableSize                = 64 << 20
	indexDBMemTableStopWritesThreshold = 4
	indexDBBaseTargetFileSize          = 32 << 20
	indexDBMaxConcurrentCompactions    = 4
)

                                                
type IndexDB struct {
	db   *pebble.DB
	path string
	mu   sync.RWMutex
	                                                                             
	                                                                          
	                                                                          
	writeMu sync.Mutex
	nodeDB  *db.Manager                                                       
}

                                  
func New(path string) (*IndexDB, error) {
	opts := newIndexDBOptions()
	if err := os.MkdirAll(path, 0755); err != nil {
		return nil, fmt.Errorf("failed to create indexdb dir: %w", err)
	}
	d, err := pebble.Open(path, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open indexdb: %w", err)
	}
	return &IndexDB{db: d, path: path}, nil
}

func newIndexDBOptions() *pebble.Options {
	return &pebble.Options{
		MemTableSize:                indexDBMemTableSize,
		MemTableStopWritesThreshold: indexDBMemTableStopWritesThreshold,
		Levels: []pebble.LevelOptions{{
			TargetFileSize: indexDBBaseTargetFileSize,
		}},
		MaxConcurrentCompactions: func() int {
			return boundedIndexDBCompactions(runtime.GOMAXPROCS(0))
		},
	}
}

func boundedIndexDBCompactions(procs int) int {
	if procs < 1 {
		return 1
	}
	if procs > indexDBMaxConcurrentCompactions {
		return indexDBMaxConcurrentCompactions
	}
	return procs
}

                                                                         
                                                                                
                 
func (idb *IndexDB) Path() string {
	if idb == nil {
		return ""
	}
	return idb.path
}

                        
func (idb *IndexDB) Close() error {
	idb.mu.Lock()
	defer idb.mu.Unlock()
	if idb.db != nil {
		return idb.db.Close()
	}
	return nil
}

                   
func (idb *IndexDB) Set(key, value string) error {
	return idb.db.Set([]byte(key), []byte(value), pebble.NoSync)
}

                
func (idb *IndexDB) Get(key string) (string, error) {
	raw, closer, err := idb.db.Get([]byte(key))
	if err != nil {
		if err == pebble.ErrNotFound {
			return "", nil
		}
		return "", err
	}
	val := string(raw)
	closer.Close()
	return val, nil
}

                   
func (idb *IndexDB) Delete(key string) error {
	return idb.db.Delete([]byte(key), pebble.NoSync)
}

                                                                          
                                                                         
                                                                           
                                           
func (idb *IndexDB) Reset() error {
	idb.writeMu.Lock()
	defer idb.writeMu.Unlock()
	idb.mu.Lock()
	defer idb.mu.Unlock()
	return idb.db.DeleteRange([]byte{0x00}, []byte{0xff}, pebble.NoSync)
}

                                                                            
type IndexBatch struct {
	batch *pebble.Batch
}

                                                       
func (idb *IndexDB) NewBatch() *IndexBatch {
	return &IndexBatch{batch: idb.db.NewBatch()}
}

                                     
func (b *IndexBatch) Set(key, value string) error {
	return b.batch.Set([]byte(key), []byte(value), pebble.NoSync)
}

                                                                             
func (b *IndexBatch) SetBytes(key string, value []byte) error {
	return b.batch.Set([]byte(key), value, pebble.NoSync)
}

                                       
func (b *IndexBatch) Delete(key string) error {
	return b.batch.Delete([]byte(key), pebble.NoSync)
}

                                                     
func (b *IndexBatch) Commit() error {
	return b.batch.Commit(pebble.NoSync)
}

                                                                       
func (b *IndexBatch) Close() error {
	return b.batch.Close()
}

                                         
func prefixUpperBound(prefix []byte) []byte {
	upper := make([]byte, len(prefix))
	copy(upper, prefix)
	for i := len(upper) - 1; i >= 0; i-- {
		upper[i]++
		if upper[i] != 0 {
			return upper
		}
	}
	return nil
}

                                               
func (idb *IndexDB) ScanPrefix(prefix string, limit int) ([]KV, error) {
	var results []KV
	p := []byte(prefix)
	iter, err := idb.db.NewIter(&pebble.IterOptions{LowerBound: p, UpperBound: prefixUpperBound(p)})
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	count := 0
	for iter.SeekGE(p); iter.Valid(); iter.Next() {
		if limit > 0 && count >= limit {
			break
		}
		key := string(iter.Key())
		val := string(iter.Value())
		results = append(results, KV{Key: key, Value: val})
		count++
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return results, nil
}

                                                                                       
func (idb *IndexDB) ScanPrefixReverse(prefix string, limit int) ([]KV, error) {
	var results []KV
	p := []byte(prefix)
	upper := prefixUpperBound(p)
	iter, err := idb.db.NewIter(&pebble.IterOptions{LowerBound: p, UpperBound: upper})
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	count := 0
	for iter.SeekLT(upper); iter.Valid(); iter.Prev() {
		if limit > 0 && count >= limit {
			break
		}
		key := string(iter.Key())
		val := string(iter.Value())
		results = append(results, KV{Key: key, Value: val})
		count++
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return results, nil
}

               
type KV struct {
	Key   string
	Value string
}
