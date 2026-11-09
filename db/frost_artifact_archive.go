package db

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"dex/frost/artifact"
	"dex/keys"
	statedb "dex/stateDB"
	"dex/utils/diskspace"

	"github.com/cockroachdb/pebble"
)

type FrostArtifactPruneResult struct {
	CurrentHeight  uint64
	MaxPruneHeight uint64
	StartCursor    uint64
	NextCursor     uint64
	ScannedHeights uint64
	DeletedRecords uint64
	DeletedKeys    uint64
	DiskFreePct    float64
	SkippedArchive bool
}

type FrostArchiveStatus struct {
	ArchiveNode     bool   `json:"archive_node"`
	Ready           bool   `json:"ready"`
	CompleteThrough uint64 `json:"complete_through"`
	LatestHeight    uint64 `json:"latest_height"`
}

func (manager *Manager) IsFrostArchiveNode() bool {
	return manager != nil && manager.cfg != nil && manager.cfg.Database.FrostArchiveNode
}

func (manager *Manager) initializeFrostArchiveCoverage() {
	if manager == nil || !manager.IsFrostArchiveNode() {
		return
	}
	coverage, _ := manager.frostArchiveCompleteThrough()
	latest := uint64(0)
	if raw, err := manager.GetKV(keys.KeyLatestHeight()); err == nil {
		latest, _ = strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
	}
	manager.frostArchiveCoverageMu.Lock()
	manager.frostArchiveCoverageInitialized = true
	manager.frostArchiveObservedThrough = coverage
	                                                                       
	                                                                            
	                                                             
	manager.frostArchiveCoverageContinuous = latest == 0 || coverage > 0
	manager.frostArchiveCoverageMu.Unlock()
}

                                                                               
                                                                                
func (manager *Manager) ObserveFrostArchiveHeight(height uint64) {
	if manager == nil || !manager.IsFrostArchiveNode() || height == 0 {
		return
	}
	manager.frostArchiveCoverageMu.Lock()
	defer manager.frostArchiveCoverageMu.Unlock()
	if !manager.frostArchiveCoverageInitialized || !manager.frostArchiveCoverageContinuous {
		return
	}
	if height <= manager.frostArchiveObservedThrough {
		return
	}
	if height != manager.frostArchiveObservedThrough+1 {
		manager.frostArchiveCoverageContinuous = false
		return
	}
	manager.frostArchiveObservedThrough = height
}

                                                                             
                                                                              
                                 
func (manager *Manager) CommitFrostArchiveCoverage() error {
	if manager == nil || !manager.IsFrostArchiveNode() {
		return nil
	}
	manager.frostArchiveCoverageMu.Lock()
	if !manager.frostArchiveCoverageContinuous {
		manager.frostArchiveCoverageMu.Unlock()
		return nil
	}
	height := manager.frostArchiveObservedThrough
	manager.frostArchiveCoverageMu.Unlock()
	if height == 0 {
		return nil
	}
	runtimeDB, err := manager.dbForStorageClass(keys.StorageRuntime)
	if err != nil {
		return err
	}
	return runtimeDB.Set([]byte(keys.KeyFrostArchiveCompleteThrough()), []byte(strconv.FormatUint(height, 10)), pebble.Sync)
}

func (manager *Manager) frostArchiveCompleteThrough() (uint64, error) {
	raw, err := manager.GetKV(keys.KeyFrostArchiveCompleteThrough())
	if errors.Is(err, pebble.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid FROST archive coverage marker: %w", err)
	}
	return value, nil
}

func (manager *Manager) FrostArchiveStatus() (FrostArchiveStatus, error) {
	status := FrostArchiveStatus{ArchiveNode: manager.IsFrostArchiveNode()}
	coverage, err := manager.frostArchiveCompleteThrough()
	if err != nil {
		return status, err
	}
	status.CompleteThrough = coverage
	if raw, readErr := manager.GetKV(keys.KeyLatestHeight()); readErr == nil {
		status.LatestHeight, _ = strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
	}
	status.Ready = status.ArchiveNode && coverage >= status.LatestHeight
	return status, nil
}

func (manager *Manager) PruneFrostArtifactsWithDiskUsage(currentHeight uint64, usage diskspace.Snapshot) (FrostArtifactPruneResult, error) {
	res := FrostArtifactPruneResult{CurrentHeight: currentHeight, DiskFreePct: usage.FreePercent}
	if manager.IsFrostArchiveNode() {
		res.SkippedArchive = true
		return res, nil
	}
	if usage.TotalBytes == 0 {
		return res, fmt.Errorf("disk total bytes is zero")
	}
	settings := manager.blockPruneSettings()
	limit := 0
	switch {
	case usage.FreePercent <= settings.aggressiveFreePercent:
		limit = settings.aggressiveHeightsPerBlock
	case usage.FreePercent <= settings.startFreePercent:
		limit = settings.startHeightsPerBlock
	default:
		return res, nil
	}
	if currentHeight <= settings.keepRecentHeights || limit <= 0 {
		return res, nil
	}
	res.MaxPruneHeight = currentHeight - settings.keepRecentHeights
	cursor, err := manager.frostArtifactPruneCursor()
	if err != nil {
		return res, err
	}
	res.StartCursor, res.NextCursor = cursor, cursor
	                                                                        
	                                                                             
	if err := manager.ForceFlush(); err != nil {
		return res, err
	}
	archiveDB, err := manager.dbForStorageClass(keys.StorageArchive)
	if err != nil {
		return res, err
	}
	for i := 0; i < limit && cursor <= res.MaxPruneHeight; i++ {
		rows, scanErr := manager.ScanKV(keys.KeyArchiveFrostArtifactHeightPrefix(cursor))
		if scanErr != nil {
			return res, scanErr
		}
		batch := archiveDB.NewBatch()
		for indexKey, artifactIDRaw := range rows {
			artifactID := strings.TrimSpace(string(artifactIDRaw))
			if artifactID == "" {
				artifactID = strings.TrimPrefix(indexKey, keys.KeyArchiveFrostArtifactHeightPrefix(cursor))
			}
			recordKey := keys.KeyArchiveFrostArtifact(artifactID)
			recordRaw, readErr := manager.GetKV(recordKey)
			if errors.Is(readErr, pebble.ErrNotFound) {
				continue
			}
			if readErr != nil {
				_ = batch.Close()
				return res, readErr
			}
			record, parseErr := artifact.UnmarshalRecord(recordRaw)
			if parseErr != nil || record.Metadata.ArtifactID != artifactID {
				continue
			}
			backed, readErr := manager.permanentFrostCopyMatches(keys.KeyFrostPermanentArtifact(artifactID), recordRaw)
			if readErr != nil {
				_ = batch.Close()
				return res, readErr
			}
			if !backed {
				                                                                  
				                                                                 
				                                                               
				continue
			}
			deleted, deleteErr := manager.addFrostArtifactRelatedDeletes(batch, record)
			if deleteErr != nil {
				_ = batch.Close()
				return res, deleteErr
			}
			if err := batch.Delete([]byte(recordKey), pebble.NoSync); err != nil {
				_ = batch.Close()
				return res, err
			}
			if err := batch.Delete([]byte(indexKey), pebble.NoSync); err != nil {
				_ = batch.Close()
				return res, err
			}
			res.DeletedRecords++
			res.DeletedKeys += 2 + deleted
		}
		                                                                       
		if err := batch.Commit(pebble.Sync); err != nil {
			_ = batch.Close()
			return res, err
		}
		_ = batch.Close()
		cursor++
		res.ScannedHeights++
	}
	res.NextCursor = cursor
	if res.ScannedHeights > 0 {
		if err := manager.setFrostArtifactPruneCursor(cursor); err != nil {
			return res, err
		}
	}
	return res, nil
}

func (manager *Manager) permanentFrostCopyMatches(key string, archiveRaw []byte) (bool, error) {
	raw, err := manager.Get(key)
	if errors.Is(err, statedb.ErrNotFound) || errors.Is(err, pebble.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return len(raw) > 0 && bytes.Equal(raw, archiveRaw), nil
}

func (manager *Manager) addFrostArtifactRelatedDeletes(batch *pebble.Batch, record *artifact.Record) (uint64, error) {
	if batch == nil || record == nil {
		return 0, nil
	}
	type copyPair struct{ archive, permanent string }
	var pairs []copyPair
	switch record.Metadata.Kind {
	case artifact.KindMigration:
		if record.Metadata.SourceID != "" {
			pairs = append(pairs, copyPair{keys.KeyArchiveFrostMigration(record.Metadata.SourceID), keys.KeyFrostPermanentMigration(record.Metadata.SourceID)})
		}
	case artifact.KindWithdraw:
		jobID := record.Metadata.SourceID
		if jobID == "" {
			return 0, nil
		}
		pairs = append(pairs,
			copyPair{keys.KeyArchiveFrostSignedPackage(jobID, 0), keys.KeyFrostPermanentSignedPackage(jobID, 0)},
			copyPair{keys.KeyArchiveFrostWithdrawJob(jobID), keys.KeyFrostPermanentWithdrawJob(jobID)})
	}
	var deleted uint64
	for _, pair := range pairs {
		raw, err := manager.GetKV(pair.archive)
		if errors.Is(err, pebble.ErrNotFound) {
			continue
		}
		if err != nil {
			return deleted, err
		}
		backed, err := manager.permanentFrostCopyMatches(pair.permanent, raw)
		if err != nil {
			return deleted, err
		}
		if !backed {
			continue
		}
		if err := batch.Delete([]byte(pair.archive), pebble.NoSync); err != nil {
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}

func (manager *Manager) frostArtifactPruneCursor() (uint64, error) {
	raw, err := manager.GetKV(keys.KeyFrostArtifactPruneCursor())
	if errors.Is(err, pebble.ErrNotFound) {
		return 1, nil
	}
	if err != nil {
		return 0, err
	}
	cursor, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil || cursor == 0 {
		return 1, nil
	}
	return cursor, nil
}

func (manager *Manager) setFrostArtifactPruneCursor(cursor uint64) error {
	runtimeDB, err := manager.dbForStorageClass(keys.StorageRuntime)
	if err != nil {
		return err
	}
	return runtimeDB.Set([]byte(keys.KeyFrostArtifactPruneCursor()), []byte(strconv.FormatUint(cursor, 10)), pebble.Sync)
}
