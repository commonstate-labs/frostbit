package db

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"dex/keys"

	"github.com/cockroachdb/pebble"
)

const blockArchiveCoverageVersion uint32 = 1

                                                                          
                                                                               
                                                                             
                                                                    
type blockArchiveCoverageMarker struct {
	Version uint32 `json:"version"`
	From    uint64 `json:"from"`
	Through uint64 `json:"through"`
}

                                                                             
                                                                              
                                                                              
                                                                          
func (manager *Manager) initializeBlockArchiveCoverage() {
	if manager == nil {
		return
	}
	marker, markerErr := manager.loadBlockArchiveCoverage()
	latest := uint64(0)
	if raw, err := manager.GetKV(keys.KeyLatestHeight()); err == nil {
		latest, _ = strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
	}

	manager.blockArchiveCoverageMu.Lock()
	defer manager.blockArchiveCoverageMu.Unlock()
	manager.blockArchiveCoverageInitialized = true
	manager.blockArchiveCoverageFrom = 0
	manager.blockArchiveCoverageObservedThrough = 0
	manager.blockArchiveCoverageBootstrapLatest = latest
	if markerErr == nil {
		manager.blockArchiveCoverageFrom = marker.From
		manager.blockArchiveCoverageObservedThrough = marker.Through
		if marker.Through > manager.blockArchiveCoverageBootstrapLatest {
			manager.blockArchiveCoverageBootstrapLatest = marker.Through
		}
	}
	                                                                           
	                                                                       
	                                                                            
}

                                                                             
                                                                                
                                                                            
func (manager *Manager) ObserveBlockArchiveHeight(height uint64) {
	if manager == nil || height == 0 {
		return
	}
	manager.blockArchiveCoverageMu.Lock()
	defer manager.blockArchiveCoverageMu.Unlock()
	if !manager.blockArchiveCoverageInitialized {
		return
	}

	if manager.blockArchiveCoverageObservedThrough == 0 {
		                                                                         
		                                                                         
		                                                                          
		                        
		if height <= manager.blockArchiveCoverageBootstrapLatest {
			return
		}
		manager.blockArchiveCoverageFrom = height
		manager.blockArchiveCoverageObservedThrough = height
		return
	}
	if height <= manager.blockArchiveCoverageObservedThrough {
		return
	}
	if height != manager.blockArchiveCoverageObservedThrough+1 {
		                                                                          
		                                                                 
		manager.blockArchiveCoverageFrom = height
		manager.blockArchiveCoverageObservedThrough = height
		return
	}
	manager.blockArchiveCoverageObservedThrough = height
}

                                                                               
                                                                                
                                                                            
                                                                 
func (manager *Manager) CommitBlockArchiveCoverage() error {
	if manager == nil {
		return nil
	}
	manager.blockArchiveCoverageMu.Lock()
	defer manager.blockArchiveCoverageMu.Unlock()
	if !manager.blockArchiveCoverageInitialized || manager.blockArchiveCoverageFrom == 0 || manager.blockArchiveCoverageObservedThrough == 0 {
		return nil
	}
	from := manager.blockArchiveCoverageFrom
	through := manager.blockArchiveCoverageObservedThrough
	cursor, err := manager.blockPruneCursor()
	if err != nil {
		return err
	}
	if cursor > from {
		from = cursor
	}
	if from > through {
		if err := manager.deleteBlockArchiveCoverageLocked(); err != nil {
			return err
		}
		manager.blockArchiveCoverageFrom = 0
		manager.blockArchiveCoverageObservedThrough = 0
		manager.blockArchiveCoverageBootstrapLatest = through
		return nil
	}
	marker := blockArchiveCoverageMarker{Version: blockArchiveCoverageVersion, From: from, Through: through}
	raw, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	runtimeDB, err := manager.dbForStorageClass(keys.StorageRuntime)
	if err != nil {
		return err
	}
	if err := runtimeDB.Set([]byte(keys.KeyBlockArchiveCoverage()), raw, pebble.Sync); err != nil {
		return err
	}
	manager.blockArchiveCoverageFrom = from
	return nil
}

                                                                            
                                                                             
                                                                          
                                                                            
                                                                               
func (manager *Manager) PrepareBlockArchivePrune(nextFrom uint64) error {
	if manager == nil {
		return nil
	}
	if nextFrom < blockPruneFirstHeight {
		return fmt.Errorf("invalid block archive prune boundary %d", nextFrom)
	}
	manager.blockArchiveCoverageMu.Lock()
	defer manager.blockArchiveCoverageMu.Unlock()

	marker, err := manager.loadBlockArchiveCoverage()
	if errors.Is(err, pebble.ErrNotFound) {
		manager.raiseObservedArchiveFromLocked(nextFrom)
		return nil
	}
	if err != nil {
		                                                                        
		return fmt.Errorf("prepare block archive prune: %w", err)
	}
	if nextFrom <= marker.From {
		manager.raiseObservedArchiveFromLocked(marker.From)
		return nil
	}
	if nextFrom > marker.Through {
		if err := manager.deleteBlockArchiveCoverageLocked(); err != nil {
			return err
		}
		manager.raiseObservedArchiveFromLocked(nextFrom)
		if marker.Through > manager.blockArchiveCoverageBootstrapLatest {
			manager.blockArchiveCoverageBootstrapLatest = marker.Through
		}
		return nil
	}

	marker.From = nextFrom
	raw, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	runtimeDB, err := manager.dbForStorageClass(keys.StorageRuntime)
	if err != nil {
		return err
	}
	if err := runtimeDB.Set([]byte(keys.KeyBlockArchiveCoverage()), raw, pebble.Sync); err != nil {
		return err
	}
	manager.raiseObservedArchiveFromLocked(nextFrom)
	return nil
}

func (manager *Manager) raiseObservedArchiveFromLocked(nextFrom uint64) {
	manager.blockArchiveCoverageInitialized = true
	if manager.blockArchiveCoverageObservedThrough < nextFrom {
		if manager.blockArchiveCoverageObservedThrough > manager.blockArchiveCoverageBootstrapLatest {
			manager.blockArchiveCoverageBootstrapLatest = manager.blockArchiveCoverageObservedThrough
		}
		manager.blockArchiveCoverageFrom = 0
		manager.blockArchiveCoverageObservedThrough = 0
		return
	}
	if manager.blockArchiveCoverageFrom == 0 || manager.blockArchiveCoverageFrom < nextFrom {
		manager.blockArchiveCoverageFrom = nextFrom
	}
}

                                                                             
                                                                                
                                                                         
func (manager *Manager) ResetBlockArchiveCoverage(checkpointHeight uint64) error {
	if manager == nil {
		return nil
	}
	manager.blockArchiveCoverageMu.Lock()
	defer manager.blockArchiveCoverageMu.Unlock()
	if err := manager.deleteBlockArchiveCoverageLocked(); err != nil {
		return err
	}
	manager.blockArchiveCoverageInitialized = true
	manager.blockArchiveCoverageFrom = 0
	manager.blockArchiveCoverageObservedThrough = 0
	manager.blockArchiveCoverageBootstrapLatest = checkpointHeight
	return nil
}

func (manager *Manager) deleteBlockArchiveCoverageLocked() error {
	runtimeDB, err := manager.dbForStorageClass(keys.StorageRuntime)
	if err != nil {
		return err
	}
	return runtimeDB.Delete([]byte(keys.KeyBlockArchiveCoverage()), pebble.Sync)
}

func (manager *Manager) loadBlockArchiveCoverage() (blockArchiveCoverageMarker, error) {
	var marker blockArchiveCoverageMarker
	if manager == nil {
		return marker, fmt.Errorf("db manager is nil")
	}
	raw, err := manager.GetKV(keys.KeyBlockArchiveCoverage())
	if err != nil {
		return marker, err
	}
	if err := json.Unmarshal(raw, &marker); err != nil {
		return blockArchiveCoverageMarker{}, fmt.Errorf("invalid block archive coverage marker: %w", err)
	}
	if marker.Version != blockArchiveCoverageVersion || marker.From < blockPruneFirstHeight || marker.Through < marker.From {
		return blockArchiveCoverageMarker{}, fmt.Errorf("invalid block archive coverage marker: version=%d range=%d..%d", marker.Version, marker.From, marker.Through)
	}
	return marker, nil
}

                                                                            
                                                                             
                                                                                
                                                                      
func (manager *Manager) GetBlockArchiveRange() (fromHeight, toHeight uint64, err error) {
	if manager == nil {
		return 0, 0, fmt.Errorf("db manager is nil")
	}
	marker, err := manager.loadBlockArchiveCoverage()
	if errors.Is(err, pebble.ErrNotFound) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	fromHeight, toHeight = marker.From, marker.Through
	cursor, err := manager.blockPruneCursor()
	if err != nil {
		return 0, 0, err
	}
	if cursor > fromHeight {
		fromHeight = cursor
	}
	if fromHeight > toHeight {
		return 0, 0, nil
	}
	return fromHeight, toHeight, nil
}

                                                                              
                                                                            
                                                                             
func (manager *Manager) GetLatestStateCheckpointHeight() (uint64, error) {
	if manager == nil {
		return 0, fmt.Errorf("db manager is nil")
	}
	manager.mu.RLock()
	stateStore := manager.stateDB
	manager.mu.RUnlock()
	if stateStore == nil {
		return 0, fmt.Errorf("stateDB is not initialized")
	}
	return stateStore.LatestCheckpointHeight()
}
