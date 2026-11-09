package db

import (
	"bytes"
	"dex/config"
	"dex/interfaces"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	statedb "dex/stateDB"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cockroachdb/pebble"
)

const (
	dbReadKVKeysProbeDebugThreshold = 5 * time.Millisecond
	dbReadKVKeysProbeWarnThreshold  = 50 * time.Millisecond
)

                                       
type Manager struct {
	                                                                          
	                                                                            
	                             
	Db        *pebble.DB
	runtimeDB *pebble.DB
	queryDB   *pebble.DB
	archiveDB *pebble.DB
	mu        sync.RWMutex
	closeOnce sync.Once
	closeErr  error

	                                                              
	writeQueueChan chan WriteTask
	                                                               
	coldWriteQueueChan chan WriteTask
	                     
	forceFlushChan     chan flushRequest
	coldForceFlushChan chan flushRequest
	                                         
	stopChan chan struct{}
	                                                         
	writeQueueEnqueueTotal         uint64
	writeQueueEnqueueSetTotal      uint64
	writeQueueEnqueueDeleteTotal   uint64
	writeQueueEnqueueBlockedCount  uint64
	writeQueueEnqueueBlockedNs     uint64
	writeQueueDequeuedTotal        uint64
	writeQueueFlushBatchTotal      uint64
	writeQueueFlushedTaskTotal     uint64
	writeQueueFlushErrTotal        uint64
	writeQueueFlushDurationNsTotal uint64
	writeQueueForceFlushTotal      uint64
	writeQueueMaxDepth             uint64
	writeQueueFlushInFlightSince   uint64                                                      
	writeQueueKeyCounters          sync.Map                             
	writeQueueBlockedKeyCounters   sync.Map                             
	maxPebbleBatchBytes            int
	maxStateApplyBytes             int

	                                                                              
	maxBatchSize  int                                              
	flushInterval time.Duration                                    
	IndexMgr      *MinerIndexManager          
	                  
	seq   uint64
	seqMu sync.Mutex
	                                                                                         
	wg sync.WaitGroup
	                                           
	cachedBlocks   []*pb.Block
	cachedBlocksMu sync.RWMutex
	Logger         logs.Logger
	cfg            *config.Config
	dataDir        string
	stateDB        *statedb.DB
	                                                                 
	minerCacheMu         sync.RWMutex
	minerCacheEpoch      uint64
	minerCacheReady      bool
	minerCacheGeneration uint64
	minerParticipants    []*pb.Account
	minerSampleRand      *rand.Rand
	minerSampleRandMu    sync.Mutex
	                                                                          
	                                                  
	minerIndexMu               sync.RWMutex
	referralIndexMu            sync.RWMutex
	referralIndexErr           error
	rechargeQueryIndexMu       sync.RWMutex
	rechargeQueryIndexErr      error
	frostWithdrawQueryIndexMu  sync.RWMutex
	frostWithdrawQueryIndexErr error

	                                                
	discoveryNodesMu      sync.RWMutex
	discoveryNodesCache   []string
	discoveryNodesCacheAt time.Time

	frostArchiveCoverageMu          sync.Mutex
	frostArchiveCoverageInitialized bool
	frostArchiveCoverageContinuous  bool
	frostArchiveObservedThrough     uint64

	blockArchiveCoverageMu              sync.Mutex
	blockArchiveCoverageInitialized     bool
	blockArchiveCoverageFrom            uint64
	blockArchiveCoverageObservedThrough uint64
	blockArchiveCoverageBootstrapLatest uint64
}

                                                 
func NewManager(path string, logger logs.Logger) (*Manager, error) {
	return NewManagerWithConfig(path, logger, nil)
}

func resolveDBSubdir(root, configured, fallback string) string {
	dir := strings.TrimSpace(configured)
	if dir == "" {
		dir = fallback
	}
	if filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(root, dir)
}

func openPebbleStore(path string, cfg *config.Config, readOnly bool, maxOpenFiles int) (*pebble.DB, error) {
	opts := &pebble.Options{
		ReadOnly:     readOnly,
		MaxOpenFiles: maxOpenFiles,
	}
	if cfg != nil && cfg.Database.TableCacheShards > 0 {
		opts.Experimental.TableCacheShards = cfg.Database.TableCacheShards
	}
	cacheSize := int64(32 << 20)
	if cfg != nil && cfg.Database.BlockCacheSizeDB > 0 {
		cacheSize = cfg.Database.BlockCacheSizeDB
	}
	opts.Cache = pebble.NewCache(cacheSize)
	defer opts.Cache.Unref()

	if cfg != nil && cfg.Database.MemTableSize > 0 {
		opts.MemTableSize = uint64(cfg.Database.MemTableSize)
	}
	numCompactors := 1
	if cfg != nil {
		numCompactors = cfg.Database.NumCompactors
	}
	if numCompactors <= 0 {
		logs.Warn("[DB] NumCompactors=%d is unsafe for write-heavy workloads; forcing to 1", numCompactors)
		numCompactors = 1
	}
	opts.MaxConcurrentCompactions = func() int { return numCompactors }

	if !readOnly {
		if err := os.MkdirAll(path, 0755); err != nil {
			return nil, fmt.Errorf("failed to create pebble dir %s: %w", path, err)
		}
	}
	return pebble.Open(path, opts)
}

func resolveStateDBDir(root string, stateCfg config.StateDBConfig) string {
	stateDir := strings.TrimSpace(stateCfg.DataDir)
	switch {
	case stateDir == "":
		return filepath.Join(root, "state")
	case !filepath.IsAbs(stateDir):
		return filepath.Join(root, stateDir)
	default:
		return stateDir
	}
}

                                                                    
func NewManagerWithConfig(path string, logger logs.Logger, cfg *config.Config) (*Manager, error) {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	if err := os.MkdirAll(path, 0755); err != nil {
		return nil, fmt.Errorf("failed to create db dir: %w", err)
	}
	runtimeDir := resolveDBSubdir(path, cfg.Database.RuntimeDBDir, "runtime")
	queryDir := resolveDBSubdir(path, cfg.Database.QueryDBDir, "query")
	archiveDir := resolveDBSubdir(path, cfg.Database.ArchiveDBDir, "archive")

	runtimeDB, err := openPebbleStore(runtimeDir, cfg, false, 500)
	if err != nil {
		return nil, fmt.Errorf("failed to open runtime pebble db: %w", err)
	}
	queryDB, err := openPebbleStore(queryDir, cfg, false, 500)
	if err != nil {
		_ = runtimeDB.Close()
		return nil, fmt.Errorf("failed to open query pebble db: %w", err)
	}
	archiveDB, err := openPebbleStore(archiveDir, cfg, false, 500)
	if err != nil {
		_ = queryDB.Close()
		_ = runtimeDB.Close()
		return nil, fmt.Errorf("failed to open archive pebble db: %w", err)
	}

	indexMgr, err := NewMinerIndexManager(runtimeDB, logger)
	if err != nil {
		_ = archiveDB.Close()
		_ = queryDB.Close()
		_ = runtimeDB.Close()
		return nil, fmt.Errorf("failed to create index manager: %w", err)
	}

	                          
	seqVal := uint64(0)
	if raw, closer, err := runtimeDB.Get([]byte("meta:seq_counter")); err == nil {
		if len(raw) >= 8 {
			seqVal = binary.BigEndian.Uint64(raw)
		}
		closer.Close()
	}
	stateCfg := cfg.Database.StateDB
	stateDir := resolveStateDBDir(path, stateCfg)
	tableCacheShards := stateCfg.TableCacheShards
	if tableCacheShards <= 0 {
		tableCacheShards = cfg.Database.TableCacheShards
	}
	stateStore, err := statedb.New(statedb.Config{
		Backend:          stateCfg.Backend,
		DataDir:          stateDir,
		ShardHexWidth:    stateCfg.ShardHexWidth,
		PageSize:         stateCfg.PageSize,
		TableCacheShards: tableCacheShards,
		BlockCacheSize:   cfg.Database.BlockCacheSizeDB,
		CheckpointKeep:   stateCfg.CheckpointKeep,
		CheckpointEvery:  stateCfg.CheckpointEvery,
	})
	if err != nil {
		_ = archiveDB.Close()
		_ = queryDB.Close()
		_ = runtimeDB.Close()
		return nil, fmt.Errorf("failed to init stateDB: %w", err)
	}

	manager := &Manager{
		Db:              runtimeDB,
		runtimeDB:       runtimeDB,
		queryDB:         queryDB,
		archiveDB:       archiveDB,
		IndexMgr:        indexMgr,
		seq:             seqVal,
		Logger:          logger,
		cfg:             cfg,
		dataDir:         path,
		stateDB:         stateStore,
		minerSampleRand: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
	manager.initializeFrostArchiveCoverage()
	manager.initializeBlockArchiveCoverage()
	if err := manager.RebuildReferralInviteeIndex(); err != nil {
		manager.Close()
		return nil, fmt.Errorf("rebuild referral query index: %w", err)
	}
	if err := manager.RebuildActiveRechargeQueryIndex(); err != nil {
		manager.Close()
		return nil, fmt.Errorf("rebuild active recharge query index: %w", err)
	}
	if err := manager.RebuildActiveFrostWithdrawQueryIndex(); err != nil {
		manager.Close()
		return nil, fmt.Errorf("rebuild active withdrawal query index: %w", err)
	}

	return manager, nil
}

                                               
func (m *Manager) NewSession() (interfaces.DBSession, error) {
	return &dbSession{manager: m}, nil
}

                                  
type dbSession struct {
	manager *Manager
}

func (s *dbSession) Get(key string) ([]byte, error) {
	return s.manager.Get(key)
}

func (s *dbSession) ApplyStateUpdate(height uint64, updates []interface{}) ([]byte, error) {
	return nil, nil
}

func (s *dbSession) Commit() error   { return nil }
func (s *dbSession) Rollback() error { return nil }
func (s *dbSession) Close() error    { return nil }

                                                                                   
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

func (manager *Manager) dbForStorageClass(class keys.StorageClass) (*pebble.DB, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	var db *pebble.DB
	switch class {
	case keys.StorageQuery:
		db = manager.queryDB
	case keys.StorageArchive:
		db = manager.archiveDB
	default:
		db = manager.runtimeDB
	}
	if db == nil {
		db = manager.Db
	}
	if db == nil {
		return nil, fmt.Errorf("%s database is not initialized or closed", class.String())
	}
	return db, nil
}

func (manager *Manager) dbForKVKey(key string) (*pebble.DB, error) {
	return manager.dbForStorageClass(keys.StorageClassForKey(key))
}

func (manager *Manager) dbForKVPrefix(prefix string) (*pebble.DB, error) {
	return manager.dbForStorageClass(keys.StorageClassForKey(prefix))
}

func (manager *Manager) readStateKey(key string) ([]byte, error) {
	manager.mu.RLock()
	stateStore := manager.stateDB
	manager.mu.RUnlock()
	if stateStore == nil {
		return nil, fmt.Errorf("stateDB is not initialized")
	}
	val, exists, err := stateStore.Get(key)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, statedb.ErrNotFound
	}
	return val, nil
}

func (manager *Manager) readKVKey(key string) ([]byte, error) {
	db, err := manager.dbForKVKey(key)
	if err != nil {
		return nil, err
	}
	raw, closer, err := db.Get([]byte(key))
	if err != nil {
		return nil, err
	}
	defer closer.Close()
	val := make([]byte, len(raw))
	copy(val, raw)
	return val, nil
}

func (manager *Manager) readStateKeys(keys []string) (map[string][]byte, error) {
	manager.mu.RLock()
	stateStore := manager.stateDB
	manager.mu.RUnlock()
	if stateStore == nil {
		return nil, fmt.Errorf("stateDB is not initialized")
	}
	return stateStore.GetMany(keys)
}

func (manager *Manager) readKVKeys(keyList []string) (out map[string][]byte, err error) {
	startAt := time.Now()
	mode := "seek"
	uniqCount := 0
	defer func() {
		cost := time.Since(startAt)
		if err == nil && cost < dbReadKVKeysProbeDebugThreshold {
			return
		}
		if err != nil || cost >= dbReadKVKeysProbeWarnThreshold {
			logs.Warn(
				"[DB][readKVKeys] mode=%s keys=%d uniq=%d hits=%d cost=%s err=%v",
				mode, len(keyList), uniqCount, len(out), cost, err,
			)
			return
		}
		logs.Debug(
			"[DB][readKVKeys] mode=%s keys=%d uniq=%d hits=%d cost=%s",
			mode, len(keyList), uniqCount, len(out), cost,
		)
	}()

	out = make(map[string][]byte, len(keyList))
	if len(keyList) == 0 {
		mode = "empty"
		return out, nil
	}

	sorted := make([]string, 0, len(keyList))
	seen := make(map[string]struct{}, len(keyList))
	for _, key := range keyList {
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		sorted = append(sorted, key)
	}
	if len(sorted) == 0 {
		mode = "empty_filtered"
		return out, nil
	}
	uniqCount = len(sorted)
	sort.Strings(sorted)

	grouped := make(map[keys.StorageClass][]string)
	for _, key := range sorted {
		class := keys.StorageClassForKey(key)
		grouped[class] = append(grouped[class], key)
	}
	classes := make([]keys.StorageClass, 0, len(grouped))
	for class := range grouped {
		classes = append(classes, class)
	}
	sort.Slice(classes, func(i, j int) bool { return classes[i] < classes[j] })
	for _, class := range classes {
		db, err := manager.dbForStorageClass(class)
		if err != nil {
			return nil, err
		}
		if err := readKVKeysFromPebble(db, grouped[class], out); err != nil {
			return nil, err
		}
	}
	if len(classes) > 1 {
		mode = "multi_db_seek"
	}
	return out, nil
}

func readKVKeysFromPebble(db *pebble.DB, sorted []string, out map[string][]byte) error {
	snap := db.NewSnapshot()
	defer snap.Close()
	iter, err := snap.NewIter(nil)
	if err != nil {
		return err
	}
	defer iter.Close()

	for _, key := range sorted {
		target := []byte(key)
		if !iter.SeekGE(target) {
			break
		}
		if !bytes.Equal(iter.Key(), target) {
			continue
		}
		raw := iter.Value()
		val := make([]byte, len(raw))
		copy(val, raw)
		out[key] = val
	}
	if err := iter.Error(); err != nil {
		return err
	}
	return nil
}

func (manager *Manager) scanStateByPrefix(prefix string, limit int, reverse bool) (map[string][]byte, error) {
	manager.mu.RLock()
	stateStore := manager.stateDB
	manager.mu.RUnlock()
	if stateStore == nil {
		return nil, fmt.Errorf("stateDB is not initialized")
	}

	items := make(map[string][]byte)
	if err := stateStore.IterateLatestByPrefix(prefix, func(key string, value []byte) error {
		items[key] = value
		return nil
	}); err != nil {
		return nil, err
	}

	if limit <= 0 || len(items) <= limit {
		return items, nil
	}

	keysSlice := make([]string, 0, len(items))
	for k := range items {
		keysSlice = append(keysSlice, k)
	}
	sort.Strings(keysSlice)
	if reverse {
		for i, j := 0, len(keysSlice)-1; i < j; i, j = i+1, j-1 {
			keysSlice[i], keysSlice[j] = keysSlice[j], keysSlice[i]
		}
	}

	out := make(map[string][]byte, limit)
	for i, k := range keysSlice {
		if i >= limit {
			break
		}
		out[k] = items[k]
	}
	return out, nil
}

func kvPairsToMap(pairs []interfaces.KVPair) map[string][]byte {
	result := make(map[string][]byte, len(pairs))
	for _, p := range pairs {
		val := make([]byte, len(p.Value))
		copy(val, p.Value)
		result[p.Key] = val
	}
	return result
}

func (manager *Manager) scanStateByPrefixOrdered(prefix string, limit int, reverse bool) ([]interfaces.KVPair, error) {
	return manager.scanStateByPrefixOrderedAfter(prefix, "", limit, reverse)
}

func (manager *Manager) scanStateByPrefixOrderedAfter(prefix, startAfter string, limit int, reverse bool) ([]interfaces.KVPair, error) {
	manager.mu.RLock()
	stateStore := manager.stateDB
	manager.mu.RUnlock()
	if stateStore == nil {
		return nil, fmt.Errorf("stateDB is not initialized")
	}
	updates, err := stateStore.IterateLatestByPrefixLimitAfter(prefix, startAfter, limit, reverse)
	if err != nil {
		return nil, err
	}
	out := make([]interfaces.KVPair, 0, len(updates))
	for _, u := range updates {
		out = append(out, interfaces.KVPair{Key: u.Key, Value: append([]byte(nil), u.Value...)})
	}
	return out, nil
}

func (manager *Manager) scanKVByPrefixOrdered(prefix string, limit int, reverse bool) ([]interfaces.KVPair, error) {
	return manager.scanKVByPrefixOrderedAfter(prefix, "", limit, reverse)
}

func (manager *Manager) scanKVKeysByPrefixOrderedAfter(prefix, startAfter string, limit int, reverse bool) ([]string, error) {
	db, err := manager.dbForKVPrefix(prefix)
	if err != nil {
		return nil, err
	}

	result := make([]string, 0)
	p := []byte(prefix)
	upper := prefixUpperBound(p)
	iter, err := db.NewIter(&pebble.IterOptions{LowerBound: p, UpperBound: upper})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	if reverse {
		ok := false
		if startAfter != "" {
			ok = iter.SeekLT([]byte(startAfter))
		} else if upper == nil {
			ok = iter.Last()
		} else {
			ok = iter.SeekLT(upper)
		}
		for ; ok; ok = iter.Prev() {
			if limit > 0 && len(result) >= limit {
				break
			}
			if !bytes.HasPrefix(iter.Key(), p) {
				break
			}
			if !orderedScanAfter(string(iter.Key()), startAfter, reverse) {
				continue
			}
			result = append(result, string(append([]byte(nil), iter.Key()...)))
		}
	} else {
		ok := false
		if startAfter != "" {
			ok = iter.SeekGE([]byte(startAfter))
			if ok && bytes.Equal(iter.Key(), []byte(startAfter)) {
				ok = iter.Next()
			}
		} else {
			ok = iter.SeekGE(p)
		}
		for ; ok; ok = iter.Next() {
			if limit > 0 && len(result) >= limit {
				break
			}
			if !bytes.HasPrefix(iter.Key(), p) {
				break
			}
			if !orderedScanAfter(string(iter.Key()), startAfter, reverse) {
				continue
			}
			result = append(result, string(append([]byte(nil), iter.Key()...)))
		}
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return result, nil
}

func (manager *Manager) scanKVByPrefixOrderedAfter(prefix, startAfter string, limit int, reverse bool) ([]interfaces.KVPair, error) {
	db, err := manager.dbForKVPrefix(prefix)
	if err != nil {
		return nil, err
	}

	result := make([]interfaces.KVPair, 0)
	p := []byte(prefix)
	upper := prefixUpperBound(p)
	iter, err := db.NewIter(&pebble.IterOptions{LowerBound: p, UpperBound: upper})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	if reverse {
		ok := false
		if startAfter != "" {
			ok = iter.SeekLT([]byte(startAfter))
		} else if upper == nil {
			ok = iter.Last()
		} else {
			ok = iter.SeekLT(upper)
		}
		for ; ok; ok = iter.Prev() {
			if limit > 0 && len(result) >= limit {
				break
			}
			if !bytes.HasPrefix(iter.Key(), p) {
				break
			}
			if !orderedScanAfter(string(iter.Key()), startAfter, reverse) {
				continue
			}
			k := make([]byte, len(iter.Key()))
			copy(k, iter.Key())
			v := make([]byte, len(iter.Value()))
			copy(v, iter.Value())
			result = append(result, interfaces.KVPair{Key: string(k), Value: v})
		}
	} else {
		ok := false
		if startAfter != "" {
			ok = iter.SeekGE([]byte(startAfter))
			if ok && bytes.Equal(iter.Key(), []byte(startAfter)) {
				ok = iter.Next()
			}
		} else {
			ok = iter.SeekGE(p)
		}
		for ; ok; ok = iter.Next() {
			if limit > 0 && len(result) >= limit {
				break
			}
			if !bytes.HasPrefix(iter.Key(), p) {
				break
			}
			if !orderedScanAfter(string(iter.Key()), startAfter, reverse) {
				continue
			}
			k := make([]byte, len(iter.Key()))
			copy(k, iter.Key())
			v := make([]byte, len(iter.Value()))
			copy(v, iter.Value())
			result = append(result, interfaces.KVPair{Key: string(k), Value: v})
		}
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return result, nil
}

func (manager *Manager) ScanKVWithLimitOrdered(prefix string, limit int, reverse bool) ([]interfaces.KVPair, error) {
	unlock, err := manager.rechargeQueryReadBarrier(prefix)
	if err != nil {
		return nil, err
	}
	defer unlock()
	unlockFrost, err := manager.frostWithdrawQueryReadBarrier(prefix)
	if err != nil {
		return nil, err
	}
	defer unlockFrost()
	if keys.IsStatefulKey(prefix) {
		return manager.scanStateByPrefixOrdered(prefix, limit, reverse)
	}
	return manager.scanKVByPrefixOrdered(prefix, limit, reverse)
}

func (manager *Manager) ScanKVWithLimitOrderedAfter(prefix, startAfter string, limit int, reverse bool) ([]interfaces.KVPair, error) {
	unlock, err := manager.rechargeQueryReadBarrier(prefix)
	if err != nil {
		return nil, err
	}
	defer unlock()
	unlockFrost, err := manager.frostWithdrawQueryReadBarrier(prefix)
	if err != nil {
		return nil, err
	}
	defer unlockFrost()
	if startAfter != "" && !strings.HasPrefix(startAfter, prefix) {
		return nil, fmt.Errorf("startAfter %q is outside prefix %q", startAfter, prefix)
	}
	if keys.IsStatefulKey(prefix) {
		return manager.scanStateByPrefixOrderedAfter(prefix, startAfter, limit, reverse)
	}
	return manager.scanKVByPrefixOrderedAfter(prefix, startAfter, limit, reverse)
}

func (manager *Manager) ScanKeysWithLimitOrdered(prefix string, limit int, reverse bool) ([]string, error) {
	return manager.ScanKeysWithLimitOrderedAfter(prefix, "", limit, reverse)
}

func (manager *Manager) ScanKeysWithLimitOrderedAfter(prefix, startAfter string, limit int, reverse bool) ([]string, error) {
	unlock, err := manager.rechargeQueryReadBarrier(prefix)
	if err != nil {
		return nil, err
	}
	defer unlock()
	unlockFrost, err := manager.frostWithdrawQueryReadBarrier(prefix)
	if err != nil {
		return nil, err
	}
	defer unlockFrost()
	if startAfter != "" && !strings.HasPrefix(startAfter, prefix) {
		return nil, fmt.Errorf("startAfter %q is outside prefix %q", startAfter, prefix)
	}
	if keys.IsStatefulKey(prefix) {
		manager.mu.RLock()
		stateStore := manager.stateDB
		manager.mu.RUnlock()
		if stateStore == nil {
			return nil, fmt.Errorf("stateDB is not initialized")
		}
		return stateStore.IterateLatestKeysByPrefixLimitAfter(prefix, startAfter, limit, reverse)
	}
	return manager.scanKVKeysByPrefixOrderedAfter(prefix, startAfter, limit, reverse)
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

                                                                                 
func (manager *Manager) Scan(prefix string) (map[string][]byte, error) {
	if keys.IsStatefulKey(prefix) {
		return manager.scanStateByPrefix(prefix, 0, false)
	}

	return manager.ScanKV(prefix)
}

                                                                                              
                                                       
func (manager *Manager) ScanKV(prefix string) (map[string][]byte, error) {
	unlock, err := manager.rechargeQueryReadBarrier(prefix)
	if err != nil {
		return nil, err
	}
	defer unlock()
	unlockFrost, err := manager.frostWithdrawQueryReadBarrier(prefix)
	if err != nil {
		return nil, err
	}
	defer unlockFrost()
	result := make(map[string][]byte)
	p := []byte(prefix)
	db, err := manager.dbForKVPrefix(prefix)
	if err != nil {
		return nil, err
	}
	iter, err := db.NewIter(&pebble.IterOptions{LowerBound: p, UpperBound: prefixUpperBound(p)})
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	for iter.SeekGE(p); iter.Valid(); iter.Next() {
		k := make([]byte, len(iter.Key()))
		copy(k, iter.Key())
		v := make([]byte, len(iter.Value()))
		copy(v, iter.Value())
		result[string(k)] = v
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return result, nil
}

                                                                                      
func (manager *Manager) ScanKVWithLimit(prefix string, limit int) (map[string][]byte, error) {
	pairs, err := manager.ScanKVWithLimitOrdered(prefix, limit, false)
	if err != nil {
		return nil, err
	}
	return kvPairsToMap(pairs), nil
}

                                                                                                   
func (manager *Manager) ScanKVWithLimitReverse(prefix string, limit int) (map[string][]byte, error) {
	pairs, err := manager.ScanKVWithLimitOrdered(prefix, limit, true)
	if err != nil {
		return nil, err
	}
	return kvPairsToMap(pairs), nil
}

                                                    
func (manager *Manager) ScanByPrefix(prefix string, limit int) (map[string]string, error) {
	if keys.IsStatefulKey(prefix) {
		stateItems, err := manager.scanStateByPrefix(prefix, limit, false)
		if err != nil {
			return nil, err
		}
		result := make(map[string]string, len(stateItems))
		for k, v := range stateItems {
			result[k] = string(v)
		}
		return result, nil
	}

	result := make(map[string]string)
	p := []byte(prefix)
	db, err := manager.dbForKVPrefix(prefix)
	if err != nil {
		return nil, err
	}
	iter, err := db.NewIter(&pebble.IterOptions{LowerBound: p, UpperBound: prefixUpperBound(p)})
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	count := 0
	for iter.SeekGE(p); iter.Valid(); iter.Next() {
		if limit > 0 && count >= limit {
			break
		}
		k := make([]byte, len(iter.Key()))
		copy(k, iter.Key())
		v := make([]byte, len(iter.Value()))
		copy(v, iter.Value())
		result[string(k)] = string(v)
		count++
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return result, nil
}

func (manager *Manager) View(fn func(txn *TransactionView) error) error {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	snap := manager.Db.NewSnapshot()
	defer snap.Close()
	return fn(&TransactionView{Snap: snap})
}

                                         
type TransactionView struct {
	Snap *pebble.Snapshot
}

func (tv *TransactionView) NewIterator() *pebble.Iterator {
	iter, _ := tv.Snap.NewIter(nil)
	return iter
}

func (manager *Manager) Close() {
	if err := manager.CloseWithError(); err != nil {
		logs.Error("[db.Close] %v", err)
	}
}

                                                                                
                                                                                
func (manager *Manager) CloseWithError() error {
	return manager.closeWithError(manager.ForceFlush, nil)
}

func (manager *Manager) closeWithError(flush func() error, closeStore func(string, func() error) error) error {
	manager.closeOnce.Do(func() { manager.closeErr = manager.closeResources(flush, closeStore) })
	return manager.closeErr
}

                                                                             
                                                                              
func (manager *Manager) closeResources(flush func() error, closeStore func(string, func() error) error) error {
	var failures []error
	record := func(stage string, err error) {
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", stage, err))
		}
	}
	closeResource := func(stage string, close func() error) {
		if closeStore == nil {
			record(stage, close())
			return
		}
		record(stage, closeStore(stage, close))
	}
	record("force flush", flush())
	                                      
	if manager.stopChan != nil {
		select {
		case <-manager.stopChan:
		default:
			close(manager.stopChan)
		}
	}
	                             
	manager.wg.Wait()
	manager.stopChan = nil
	manager.forceFlushChan = nil
	manager.coldForceFlushChan = nil
	manager.writeQueueChan = nil
	manager.coldWriteQueueChan = nil
	               
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.stateDB != nil {
		closeResource("stateDB close", manager.stateDB.Close)
		manager.stateDB = nil
	}
	if manager.Db != nil {
		closeResource("runtime close", manager.Db.Close)
		manager.Db = nil
	}
	if manager.queryDB != nil {
		closeResource("query close", manager.queryDB.Close)
		manager.queryDB = nil
	}
	if manager.archiveDB != nil {
		closeResource("archive close", manager.archiveDB.Close)
		manager.archiveDB = nil
	}
	manager.runtimeDB = nil
	return errors.Join(failures...)
}

                             
func (manager *Manager) Read(key string) (string, error) {
	raw, err := manager.Get(key)
	if err != nil {
		return "", err
	}
	val := string(raw)
	return val, nil
}

                                                 
func (manager *Manager) Get(key string) ([]byte, error) {
	if keys.IsStatefulKey(key) {
		return manager.readStateKey(key)
	}
	return manager.readKVKey(key)
}

                                                
func (manager *Manager) GetKV(key string) ([]byte, error) {
	return manager.readKVKey(key)
}

func (manager *Manager) GetKVs(keyList []string) (map[string][]byte, error) {
	result := make(map[string][]byte, len(keyList))
	if len(keyList) == 0 {
		return result, nil
	}

	stateKeys := make([]string, 0, len(keyList))
	kvKeys := make([]string, 0, len(keyList))
	for _, key := range keyList {
		if key == "" {
			continue
		}
		if keys.IsStatefulKey(key) {
			stateKeys = append(stateKeys, key)
		} else {
			kvKeys = append(kvKeys, key)
		}
	}

	if len(stateKeys) > 0 {
		stateValues, err := manager.readStateKeys(stateKeys)
		if err != nil {
			return nil, err
		}
		for k, v := range stateValues {
			result[k] = v
		}
	}

	if len(kvKeys) > 0 {
		kvValues, err := manager.readKVKeys(kvKeys)
		if err != nil {
			return nil, err
		}
		for k, v := range kvValues {
			result[k] = v
		}
	}

	return result, nil
}

                                           
func SerializeAllTransactions(txCopy []*pb.AnyTx) [][]byte {
	sort.Slice(txCopy, func(i, j int) bool { return txCopy[i].GetTxId() < txCopy[j].GetTxId() })
	var result [][]byte
	for _, tx := range txCopy {
		result = append(result, serializeTransaction(tx))
	}
	return result
}

func serializeTransaction(tx *pb.AnyTx) []byte {
	return []byte(tx.GetTxId() + "|" + tx.GetBase().FromAddress)
}

                                        
func (m *Manager) NextIndex() (uint64, error) {
	m.seqMu.Lock()
	defer m.seqMu.Unlock()
	m.seq++
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, m.seq)
	db, err := m.dbForStorageClass(keys.StorageRuntime)
	if err != nil {
		return 0, err
	}
	if err := db.Set([]byte("meta:seq_counter"), buf, pebble.Sync); err != nil {
		return 0, err
	}
	return m.seq, nil
}

                                                                
func (m *Manager) GetMinerByIndex(index uint64) (*pb.Account, error) {
	if m.IndexMgr == nil {
		return nil, fmt.Errorf("IndexMgr not initialized")
	}
	addrOrKey, err := m.IndexMgr.GetAddressByIndex(index)
	if err != nil {
		return nil, err
	}
	                                                                        
	                                         
	if keys.IsAccountKey(addrOrKey) {
		raw, err := m.Get(addrOrKey)
		if err != nil {
			return nil, err
		}
		account := &pb.Account{}
		if err := ProtoUnmarshal(raw, account); err != nil {
			return nil, err
		}
		return account, nil
	}
	return m.GetAccount(addrOrKey)
}

                                                                                                   
func (manager *Manager) CompactOrderIndexes() error {
	manager.mu.RLock()
	stateStore := manager.stateDB
	manager.mu.RUnlock()
	if stateStore == nil {
		return fmt.Errorf("stateDB is not initialized")
	}
	return stateStore.CompactBusinessPrefix(keys.KeyOrderBookPrefix())
}

                                                                                
                                             
func (manager *Manager) CompactStateOrderStates() error {
	manager.mu.RLock()
	stateStore := manager.stateDB
	manager.mu.RUnlock()
	if stateStore == nil {
		return fmt.Errorf("stateDB is not initialized")
	}
	return stateStore.CompactBusinessPrefix(keys.KeyOrderStatePrefix())
}
