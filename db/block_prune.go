package db

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"dex/keys"
	"dex/pb"
	"dex/utils/diskspace"

	"github.com/cockroachdb/pebble"
)

const (
	defaultBlockPruneStartFreePercent          = 50.0
	defaultBlockPruneAggressiveFreePercent     = 40.0
	defaultBlockPruneKeepRecentHeights         = uint64(100)
	defaultBlockPruneStartHeightsPerBlock      = 1
	defaultBlockPruneAggressiveHeightsPerBlock = 100
	blockPruneFirstHeight                      = uint64(1)
)

type blockPruneSettings struct {
	startFreePercent          float64
	aggressiveFreePercent     float64
	keepRecentHeights         uint64
	startHeightsPerBlock      int
	aggressiveHeightsPerBlock int
}

type BlockPruneResult struct {
	CurrentHeight  uint64
	MaxPruneHeight uint64
	StartCursor    uint64
	NextCursor     uint64
	ScannedHeights uint64
	PrunedHeights  uint64
	DeletedKeys    uint64
	DeletedBlocks  uint64
	DeletedTxRaw   uint64
	DiskPath       string
	DiskTotalBytes uint64
	DiskFreeBytes  uint64
	DiskFreePct    float64
}

type pruneHeightResult struct {
	found         bool
	deletedKeys   uint64
	deletedBlocks uint64
	deletedTxRaw  uint64
}

func (manager *Manager) PruneBlockDataUnderDiskPressure(currentHeight uint64) (BlockPruneResult, error) {
	usage, err := manager.DiskUsage()
	if err != nil {
		return BlockPruneResult{CurrentHeight: currentHeight, DiskPath: manager.diskPath()}, err
	}
	return manager.PruneBlockDataWithDiskUsage(currentHeight, usage)
}

                                                                            
                                          
func (manager *Manager) PruneBlockDataWithDiskUsage(currentHeight uint64, usage diskspace.Snapshot) (BlockPruneResult, error) {
	res := BlockPruneResult{
		CurrentHeight:  currentHeight,
		DiskPath:       usage.Path,
		DiskTotalBytes: usage.TotalBytes,
		DiskFreeBytes:  usage.FreeBytes,
		DiskFreePct:    usage.FreePercent,
	}
	if res.DiskPath == "" {
		res.DiskPath = manager.diskPath()
	}
	if res.DiskTotalBytes == 0 {
		return res, fmt.Errorf("disk total bytes is zero")
	}

	settings := manager.blockPruneSettings()
	heightsPerBlock := 0
	switch {
	case res.DiskFreePct <= settings.aggressiveFreePercent:
		heightsPerBlock = settings.aggressiveHeightsPerBlock
	case res.DiskFreePct <= settings.startFreePercent:
		heightsPerBlock = settings.startHeightsPerBlock
	default:
		return res, nil
	}

	pruned, err := manager.PruneOldBlockData(currentHeight, heightsPerBlock)
	pruned.DiskPath = res.DiskPath
	pruned.DiskTotalBytes = res.DiskTotalBytes
	pruned.DiskFreeBytes = res.DiskFreeBytes
	pruned.DiskFreePct = res.DiskFreePct
	return pruned, err
}

func (manager *Manager) PruneOldBlockData(currentHeight uint64, heightsPerBlock int) (BlockPruneResult, error) {
	res := BlockPruneResult{CurrentHeight: currentHeight}
	settings := manager.blockPruneSettings()
	if heightsPerBlock <= 0 || currentHeight <= settings.keepRecentHeights {
		return res, nil
	}
	res.MaxPruneHeight = currentHeight - settings.keepRecentHeights

	cursor, err := manager.blockPruneCursor()
	if err != nil {
		return res, err
	}
	if cursor < blockPruneFirstHeight {
		cursor = blockPruneFirstHeight
	}
	res.StartCursor = cursor
	res.NextCursor = cursor
	if cursor > res.MaxPruneHeight {
		return res, nil
	}
	plannedHeights := uint64(heightsPerBlock)
	remainingHeights := res.MaxPruneHeight - cursor + 1
	if plannedHeights > remainingHeights {
		plannedHeights = remainingHeights
	}
	plannedNextCursor := cursor + plannedHeights
	                                                                             
	                                                                          
	                                                                        
	if err := manager.PrepareBlockArchivePrune(plannedNextCursor); err != nil {
		return res, err
	}

	for i := 0; i < heightsPerBlock && cursor <= res.MaxPruneHeight; i++ {
		pruned, err := manager.pruneBlockHeight(cursor)
		if err != nil {
			return res, err
		}
		res.ScannedHeights++
		if pruned.found {
			res.PrunedHeights++
		}
		res.DeletedKeys += pruned.deletedKeys
		res.DeletedBlocks += pruned.deletedBlocks
		res.DeletedTxRaw += pruned.deletedTxRaw
		cursor++
	}
	res.NextCursor = cursor
	if res.ScannedHeights > 0 {
		if err := manager.setBlockPruneCursor(cursor); err != nil {
			return res, err
		}
	}
	return res, nil
}

func (manager *Manager) blockPruneSettings() blockPruneSettings {
	settings := blockPruneSettings{
		startFreePercent:          defaultBlockPruneStartFreePercent,
		aggressiveFreePercent:     defaultBlockPruneAggressiveFreePercent,
		keepRecentHeights:         defaultBlockPruneKeepRecentHeights,
		startHeightsPerBlock:      defaultBlockPruneStartHeightsPerBlock,
		aggressiveHeightsPerBlock: defaultBlockPruneAggressiveHeightsPerBlock,
	}
	if manager != nil && manager.cfg != nil {
		if manager.cfg.Database.BlockPruneStartFreePercent > 0 {
			settings.startFreePercent = manager.cfg.Database.BlockPruneStartFreePercent
		}
		if manager.cfg.Database.BlockPruneAggressiveFreePercent > 0 {
			settings.aggressiveFreePercent = manager.cfg.Database.BlockPruneAggressiveFreePercent
		}
		if manager.cfg.Database.BlockPruneKeepRecentHeights > 0 {
			settings.keepRecentHeights = manager.cfg.Database.BlockPruneKeepRecentHeights
		}
		if manager.cfg.Database.BlockPruneStartHeightsPerBlock > 0 {
			settings.startHeightsPerBlock = manager.cfg.Database.BlockPruneStartHeightsPerBlock
		}
		if manager.cfg.Database.BlockPruneAggressiveHeightsPerBlock > 0 {
			settings.aggressiveHeightsPerBlock = manager.cfg.Database.BlockPruneAggressiveHeightsPerBlock
		}
	}
	if settings.aggressiveFreePercent > settings.startFreePercent {
		settings.aggressiveFreePercent = settings.startFreePercent
	}
	return settings
}

func (manager *Manager) blockPruneCursor() (uint64, error) {
	raw, err := manager.GetKV(keys.KeyBlockPruneCursor())
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return blockPruneFirstHeight, nil
		}
		return 0, err
	}
	cursor, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil || cursor < blockPruneFirstHeight {
		return blockPruneFirstHeight, nil
	}
	return cursor, nil
}

func (manager *Manager) setBlockPruneCursor(cursor uint64) error {
	db, err := manager.dbForStorageClass(keys.StorageRuntime)
	if err != nil {
		return err
	}
	return db.Set([]byte(keys.KeyBlockPruneCursor()), []byte(strconv.FormatUint(cursor, 10)), pebble.Sync)
}

func (manager *Manager) pruneBlockHeight(height uint64) (pruneHeightResult, error) {
	var res pruneHeightResult
	heightKey := keys.KeyHeightBlocks(height)
	rawIDs, err := manager.GetKV(heightKey)
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			                                                                    
			                                                                    
			                                                     
			if err := manager.pruneBlockRuntimeHeight(height, nil, &res); err != nil {
				return res, err
			}
			return res, nil
		}
		return res, err
	}
	res.found = true

	var blockIDs []string
	if err := json.Unmarshal(rawIDs, &blockIDs); err != nil {
		blockIDs = nil
	}
	blockIDs = uniqueSortedStrings(blockIDs)
	txIDs := make(map[string]struct{})
	for _, blockID := range blockIDs {
		rawBlock, err := manager.GetKV(keys.KeyBlockData(blockID))
		if err != nil {
			continue
		}
		var block pb.Block
		if err := ProtoUnmarshal(rawBlock, &block); err != nil {
			continue
		}
		for _, tx := range block.Body {
			if tx == nil {
				continue
			}
			if txID := tx.GetTxId(); txID != "" {
				txIDs[txID] = struct{}{}
			}
		}
	}

	archiveDB, err := manager.dbForStorageClass(keys.StorageArchive)
	if err != nil {
		return res, err
	}
	batch := archiveDB.NewBatch()
	defer batch.Close()
	del := func(key string) error {
		if key == "" {
			return nil
		}
		if err := batch.Delete([]byte(key), pebble.NoSync); err != nil {
			return err
		}
		res.deletedKeys++
		return nil
	}
	if err := del(heightKey); err != nil {
		return res, err
	}
	for _, blockID := range blockIDs {
		if err := del(keys.KeyBlockData(blockID)); err != nil {
			return res, err
		}
		if err := del(keys.KeyBlockIDToHeight(blockID)); err != nil {
			return res, err
		}
		res.deletedBlocks++
	}
	for _, txID := range sortedMapKeys(txIDs) {
		if err := del(keys.KeyTxRaw(txID)); err != nil {
			return res, err
		}
		res.deletedTxRaw++
	}
	if err := manager.pruneBlockRuntimeHeight(height, blockIDs, &res); err != nil {
		return res, err
	}
	                                                                            
	                                                                       
	if err := batch.Commit(pebble.Sync); err != nil {
		return res, err
	}
	manager.dropCachedBlocks(blockIDs)
	return res, nil
}

func (manager *Manager) pruneBlockRuntimeHeight(height uint64, blockIDs []string, res *pruneHeightResult) error {
	                                                                        
	                                                                           
	                                                             
	                                                                
	runtimeDB, err := manager.dbForStorageClass(keys.StorageRuntime)
	if err != nil {
		return err
	}
	runtimeBatch := runtimeDB.NewBatch()
	defer runtimeBatch.Close()
	for _, blockID := range blockIDs {
		if err := runtimeBatch.Delete([]byte(keys.KeyBlockReadModel(blockID)), pebble.NoSync); err != nil {
			return err
		}
		if res != nil {
			res.deletedKeys++
		}
	}
	if err := runtimeBatch.Delete([]byte(fmt.Sprintf("finalization_chits_%d", height)), pebble.NoSync); err != nil {
		return err
	}
	if res != nil {
		res.deletedKeys++
	}
	if err := runtimeBatch.Commit(pebble.Sync); err != nil {
		return err
	}
	return nil
}

func (manager *Manager) dropCachedBlocks(blockIDs []string) {
	if len(blockIDs) == 0 {
		return
	}
	remove := make(map[string]struct{}, len(blockIDs))
	for _, id := range blockIDs {
		remove[id] = struct{}{}
	}
	manager.cachedBlocksMu.Lock()
	defer manager.cachedBlocksMu.Unlock()
	kept := manager.cachedBlocks[:0]
	for _, block := range manager.cachedBlocks {
		if block == nil {
			continue
		}
		if _, ok := remove[block.BlockHash]; ok {
			continue
		}
		kept = append(kept, block)
	}
	manager.cachedBlocks = kept
}

func uniqueSortedStrings(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}

func sortedMapKeys(m map[string]struct{}) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
