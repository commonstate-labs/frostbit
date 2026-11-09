package statedb

import (
	"dex/blocktrace"
	"dex/logs"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type DB struct {
	conf           Config
	store          kvStore
	liveDir        string
	checkpointsDir string

	mu sync.RWMutex

	closeOnce sync.Once
	closeErr  error

	shards []string

	                                                                        
	                                                       
	liveFingerprints map[string]*ShardFingerprint

	cpRefMu sync.Mutex
	cpCache map[uint64]*cpCacheEntry

	driftAudit bool
}

                                                                      
func (s *DB) SetDriftAudit(enabled bool) {
	s.mu.Lock()
	s.driftAudit = enabled
	s.mu.Unlock()
}

const (
	stateDBDefaultCheckpointEvery       uint64 = 100
	stateDBCheckpointProbeWarnThreshold        = 100 * time.Millisecond
	stateDBCheckpointStageWarnThreshold        = 20 * time.Millisecond
	stateDBCheckpointProbeInfoThreshold        = 20 * time.Millisecond
)

func New(cfg Config) (*DB, error) {
	if cfg.Backend == "" {
		cfg.Backend = BackendPebble
	}
	if cfg.CheckpointEvery == 0 {
		cfg.CheckpointEvery = stateDBDefaultCheckpointEvery
	}
	if cfg.ShardHexWidth != 1 && cfg.ShardHexWidth != 2 {
		cfg.ShardHexWidth = 1
	}
	if cfg.PageSize <= 0 {
		cfg.PageSize = 1000
	}
	if cfg.BlockCacheSize <= 0 {
		cfg.BlockCacheSize = 32 << 20
	}
	if cfg.CheckpointKeep <= 0 {
		cfg.CheckpointKeep = 50
	}

	if cfg.ReadOnly {
		if st, err := os.Stat(cfg.DataDir); err != nil {
			return nil, err
		} else if !st.IsDir() {
			return nil, fmt.Errorf("stateDB path is not directory: %s", cfg.DataDir)
		}
	} else if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return nil, err
	}

	liveDir := filepath.Join(cfg.DataDir, "live")
	checkpointsDir := filepath.Join(cfg.DataDir, "checkpoints")
	if !cfg.ReadOnly {
		if err := os.MkdirAll(liveDir, 0o755); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(checkpointsDir, 0o755); err != nil {
			return nil, err
		}
	}

	var (
		store kvStore
		err   error
	)
	switch cfg.Backend {
	case BackendPebble:
		if cfg.ReadOnly {
			store, err = newPebbleStoreReadOnly(liveDir, cfg.TableCacheShards, cfg.BlockCacheSize)
		} else {
			store, err = newPebbleStore(liveDir, cfg.TableCacheShards, cfg.BlockCacheSize)
		}
	case BackendBadger:
		return nil, fmt.Errorf("stateDB backend %q is not available in this build", BackendBadger)
	default:
		return nil, fmt.Errorf("unsupported stateDB backend: %s", cfg.Backend)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to open %s store: %w", cfg.Backend, err)
	}

	var shards []string
	if cfg.ShardHexWidth == 1 {
		for i := 0; i < 16; i++ {
			shards = append(shards, fmt.Sprintf("%x", i))
		}
	} else {
		for i := 0; i < 256; i++ {
			shards = append(shards, fmt.Sprintf("%02x", i))
		}
	}

	db := &DB{
		conf:             cfg,
		store:            store,
		liveDir:          liveDir,
		checkpointsDir:   checkpointsDir,
		shards:           shards,
		liveFingerprints: make(map[string]*ShardFingerprint, len(shards)),
		cpCache:          make(map[uint64]*cpCacheEntry),
	}
	for _, sh := range shards {
		db.liveFingerprints[sh] = &ShardFingerprint{Shard: sh}
	}
	if err := db.rebuildLiveFingerprints(); err != nil {
		logs.Warn("[StateDB] rebuildLiveFingerprints failed (non-fatal): %v", err)
	}
	return db, nil
}

func (s *DB) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.store != nil {
			s.closeErr = s.store.Close()
			s.store = nil
		}
	})
	return s.closeErr
}

                                                             
func (s *DB) Config() Config {
	return s.conf
}

func checkpointOf(height uint64, every uint64) bool {
	return every > 0 && height%every == 0
}

func (s *DB) checkpointEvery() uint64 {
	return s.conf.CheckpointEvery
}

func (s *DB) checkpointPath(height uint64) string {
	return filepath.Join(s.checkpointsDir, fmt.Sprintf("h_%020d", height))
}

func (s *DB) parseCheckpointHeight(name string) (uint64, bool) {
	if !strings.HasPrefix(name, "h_") {
		return 0, false
	}
	n, err := strconv.ParseUint(strings.TrimPrefix(name, "h_"), 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func (s *DB) listCheckpointHeightsOnDisk() ([]uint64, error) {
	entries, err := os.ReadDir(s.checkpointsDir)
	if err != nil {
		return nil, err
	}
	heights := make([]uint64, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if h, ok := s.parseCheckpointHeight(entry.Name()); ok {
			heights = append(heights, h)
		}
	}
	sort.Slice(heights, func(i, j int) bool { return heights[i] < heights[j] })
	return heights, nil
}

func (s *DB) pruneOldCheckpointsLocked() error {
	if s.conf.CheckpointKeep <= 0 {
		return nil
	}
	heights, err := s.listCheckpointHeightsOnDisk()
	if err != nil {
		return err
	}
	if len(heights) <= s.conf.CheckpointKeep {
		return nil
	}

	candidates := heights[:len(heights)-s.conf.CheckpointKeep]
	s.cpRefMu.Lock()
	toDelete := make([]uint64, 0, len(candidates))
	for _, h := range candidates {
		if entry, ok := s.cpCache[h]; !ok || entry.refCount <= 0 {
			toDelete = append(toDelete, h)
		}
	}
	s.cpRefMu.Unlock()
	for _, h := range toDelete {
		if err := s.removeCheckpointFiles(h); err != nil {
			return err
		}
	}

	return s.store.Update(func(tx kvWriter) error {
		for _, h := range toDelete {
			if err := tx.Delete(kCpIndex(h)); err != nil && !s.store.IsNotFound(err) {
				return err
			}
		}
		if len(heights) > 0 {
			if err := tx.Set(kCpLatest(), u64ToBytes(heights[len(heights)-1])); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *DB) lastAppliedHeightLocked() (uint64, bool, error) {
	var height uint64
	err := s.store.View(func(tx kvReader) error {
		raw, err := tx.Get(kMetaLastAppliedHeight())
		if err != nil {
			if s.store.IsNotFound(err) {
				return nil
			}
			return err
		}
		if len(raw) < 8 {
			return fmt.Errorf("invalid last applied height")
		}
		height = bytesToU64(raw)
		return nil
	})
	if err != nil {
		return 0, false, err
	}
	return height, height > 0, nil
}

                                                                           
                                                                          
                                                                        
func (s *DB) LastAppliedHeight() (uint64, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastAppliedHeightLocked()
}

                                                                          
                                                                   
func (s *DB) AppliedBlockID(height uint64) (string, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var blockID string
	err := s.store.View(func(tx kvReader) error {
		raw, err := tx.Get(kMetaAppliedBlock(height))
		if err != nil {
			if s.store.IsNotFound(err) {
				return nil
			}
			return err
		}
		blockID = string(raw)
		return nil
	})
	if err != nil {
		return "", false, err
	}
	return blockID, blockID != "", nil
}

func (s *DB) checkpointInUseLocked(height uint64) bool {
	s.cpRefMu.Lock()
	defer s.cpRefMu.Unlock()
	entry, ok := s.cpCache[height]
	return ok && entry.refCount > 0
}

func (s *DB) recordCheckpointMetaLocked(height uint64) error {
	return s.store.Update(func(tx kvWriter) error {
		latest := height
		if latestRaw, getErr := tx.Get(kCpLatest()); getErr == nil {
			if len(latestRaw) >= 8 {
				if h := bytesToU64(latestRaw); h > latest {
					latest = h
				}
			}
		} else if !s.store.IsNotFound(getErr) {
			return getErr
		}
		if err := tx.Set(kCpLatest(), u64ToBytes(latest)); err != nil {
			return err
		}
		return tx.Set(kCpIndex(height), u64ToBytes(height))
	})
}

func (s *DB) createCheckpointLocked(height uint64, force bool) (err error) {
	if height == 0 {
		return nil
	}
	startAt := time.Now()
	var (
		durRemove     time.Duration
		durMkdir      time.Duration
		durCheckpoint time.Duration
		durMeta       time.Duration
		durPrune      time.Duration
	)
	cpPath := s.checkpointPath(height)
	liveHeight, hasLiveHeight, err := s.lastAppliedHeightLocked()
	if err != nil {
		return err
	}
	liveAtRequestedHeight := hasLiveHeight && liveHeight == height
	defer func() {
		total := time.Since(startAt)
		blocktrace.Record(height, "", "StateDB.createCheckpoint", total,
			blocktrace.F("remove", durRemove), blocktrace.F("mkdir", durMkdir),
			blocktrace.F("checkpoint", durCheckpoint), blocktrace.F("meta", durMeta),
			blocktrace.F("prune", durPrune), blocktrace.F("force", force),
			blocktrace.F("path", cpPath), blocktrace.F("err", err),
		)
		if err == nil &&
			total < stateDBCheckpointProbeInfoThreshold &&
			durCheckpoint < stateDBCheckpointStageWarnThreshold &&
			durPrune < stateDBCheckpointStageWarnThreshold {
			return
		}
		if err != nil ||
			total >= stateDBCheckpointProbeWarnThreshold ||
			durCheckpoint >= stateDBCheckpointStageWarnThreshold ||
			durPrune >= stateDBCheckpointStageWarnThreshold {
			logs.Warn(
				"[StateDB][Checkpoint] height=%d total=%s remove=%s mkdir=%s checkpoint=%s meta=%s prune=%s path=%s err=%v",
				height, total, durRemove, durMkdir, durCheckpoint, durMeta, durPrune, cpPath, err,
			)
		}
	}()

	existingCheckpointInvalid := false
	if info, statErr := os.Stat(cpPath); statErr == nil {
		if !info.IsDir() {
			return fmt.Errorf("checkpoint path is not directory: %s", cpPath)
		}
		if force {
			if !liveAtRequestedHeight {
				if !hasLiveHeight {
					return fmt.Errorf("checkpoint %d cannot be force-recreated before any live height is applied", height)
				}
				return fmt.Errorf("checkpoint %d cannot be force-recreated while live state is at height %d", height, liveHeight)
			}
			logs.Debug("[StateDB][Checkpoint] height=%d force recreate existing checkpoint path=%s", height, cpPath)
		} else if manifestErr := s.validateCheckpointManifestFastLocked(height, liveAtRequestedHeight); manifestErr == nil {
			metaAt := time.Now()
			err = s.recordCheckpointMetaLocked(height)
			durMeta = time.Since(metaAt)
			if err != nil {
				return err
			}
			logs.Debug("[StateDB][Checkpoint] height=%d reuse existing checkpoint path=%s", height, cpPath)
			return nil
		} else {
			if !liveAtRequestedHeight {
				return fmt.Errorf("checkpoint %d exists but failed fast validation while live height is %d: %w", height, liveHeight, manifestErr)
			}
			existingCheckpointInvalid = true
			logs.Warn("[StateDB][Checkpoint] height=%d existing checkpoint manifest invalid, recreate path=%s err=%v", height, cpPath, manifestErr)
		}
	} else if !os.IsNotExist(statErr) {
		return statErr
	}

	if !liveAtRequestedHeight {
		if !hasLiveHeight {
			return fmt.Errorf("checkpoint %d cannot be created before any live height is applied", height)
		}
		return fmt.Errorf("checkpoint %d missing and live state is already at height %d; cannot create historical checkpoint from current live DB", height, liveHeight)
	}

	if s.checkpointInUseLocked(height) {
		if force {
			return fmt.Errorf("checkpoint %d cannot be force-recreated while checkpoint is in use", height)
		}
		if existingCheckpointInvalid {
			return fmt.Errorf("checkpoint %d manifest invalid while checkpoint is in use", height)
		}
		return nil
	}

	removeAt := time.Now()
	if err = s.removeCheckpointFiles(height); err != nil {
		durRemove = time.Since(removeAt)
		return err
	}
	durRemove = time.Since(removeAt)

	mkdirAt := time.Now()
	if err = os.MkdirAll(s.checkpointsDir, 0o755); err != nil {
		durMkdir = time.Since(mkdirAt)
		return err
	}
	durMkdir = time.Since(mkdirAt)

	var preFP uint64
	for _, fp := range s.liveFingerprints {
		preFP ^= fp.XorHash
	}
	checkpointAt := time.Now()
	if err = s.store.Checkpoint(cpPath, true); err != nil {
		durCheckpoint = time.Since(checkpointAt)
		return err
	}
	durCheckpoint = time.Since(checkpointAt)

	var postFP uint64
	for _, fp := range s.liveFingerprints {
		postFP ^= fp.XorHash
	}
	if preFP != postFP {
		logs.Warn("[StateDB][Checkpoint] height=%d live fingerprint changed during checkpoint pre=%016x post=%016x", height, preFP, postFP)
	}

	if err = s.writeCheckpointManifestLocked(height, time.Now().UnixNano()); err != nil {
		return err
	}

	metaAt := time.Now()
	err = s.recordCheckpointMetaLocked(height)
	durMeta = time.Since(metaAt)
	if err != nil {
		return err
	}

	pruneAt := time.Now()
	err = s.pruneOldCheckpointsLocked()
	durPrune = time.Since(pruneAt)
	if err != nil {
		return err
	}
	return nil
}

func (s *DB) openCheckpointStore(height uint64) (kvStore, error) {
	s.cpRefMu.Lock()
	defer s.cpRefMu.Unlock()

	if entry, ok := s.cpCache[height]; ok {
		entry.refCount++
		return &refCountedStore{entry: entry, db: s, height: height}, nil
	}

	path := s.checkpointPath(height)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("checkpoint path is not directory: %s", path)
	}
	inner, err := newPebbleStoreReadOnly(path, s.conf.TableCacheShards, s.conf.BlockCacheSize)
	if err != nil {
		return nil, err
	}
	entry := &cpCacheEntry{store: inner, refCount: 1}
	s.cpCache[height] = entry
	return &refCountedStore{entry: entry, db: s, height: height}, nil
}

type cpCacheEntry struct {
	store    kvStore
	refCount int32
}

type refCountedStore struct {
	entry  *cpCacheEntry
	db     *DB
	height uint64
	once   sync.Once
}

func (r *refCountedStore) Close() error {
	var err error
	r.once.Do(func() {
		r.db.cpRefMu.Lock()
		r.entry.refCount--
		if r.entry.refCount <= 0 {
			err = r.entry.store.Close()
			delete(r.db.cpCache, r.height)
		}
		r.db.cpRefMu.Unlock()
	})
	return err
}

func (r *refCountedStore) Get(key []byte) ([]byte, error)       { return r.entry.store.Get(key) }
func (r *refCountedStore) View(fn func(kvReader) error) error   { return r.entry.store.View(fn) }
func (r *refCountedStore) Update(fn func(kvWriter) error) error { return r.entry.store.Update(fn) }
func (r *refCountedStore) Checkpoint(dst string, restrict bool) error {
	return r.entry.store.Checkpoint(dst, restrict)
}
func (r *refCountedStore) IsNotFound(err error) bool { return r.entry.store.IsNotFound(err) }
