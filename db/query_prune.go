package db

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"dex/keys"
	"dex/logs"
	"dex/utils/diskspace"

	"github.com/cockroachdb/pebble"
)

const (
	queryPruneBatchCommitOps           = 1000
	queryPruneMaxNewRecoveryGapsPerRun = 1
)

                                                                         
                                                                         
                                                              
const queryPruneMissingManifestGapPrefix = "v1:recovery:missing_manifest_after_cold_flush:"

type queryPruneGap struct {
	resumeHeight uint64
	persistent   bool
}

var queryPrunePrefixes = struct {
	userActivity, spotAccountOrder, futureAccountOrder, rechargeQuery, frostWithdraw string
	trade, futureTrade, futurePosition, tradingFee, referralRebate                   string
}{
	queryUserActivityPrefix(), querySpotAccountOrderPrefix(), queryFutureAccountOrderPrefix(), keys.KeyRechargeQueryRecentPrefix(), queryFrostWithdrawPrefix(),
	queryTradePrefix(), queryFutureTradePrefix(), keys.KeyFuturePositionHistoryPrefix(), queryTradingFeePrefix(), queryReferralRebatePrefix(),
}

type QueryPruneResult struct {
	CurrentHeight     uint64
	MaxPruneHeight    uint64
	StartCursor       uint64
	NextCursor        uint64
	ScannedHeights    uint64
	PrunedHeights     uint64
	SkippedHeights    uint64
	ArchiveAligned    bool
	ScannedKeys       uint64
	DeletedKeys       uint64
	DeletedByFamily   map[string]uint64
	CompactedFamilies []string
	DiskPath          string
	DiskTotalBytes    uint64
	DiskFreeBytes     uint64
	DiskFreePct       float64
}

func (res QueryPruneResult) FamilySummary() string {
	if len(res.DeletedByFamily) == 0 {
		return ""
	}
	families := make([]string, 0, len(res.DeletedByFamily))
	for family := range res.DeletedByFamily {
		families = append(families, family)
	}
	sort.Strings(families)
	parts := make([]string, 0, len(families))
	for _, family := range families {
		parts = append(parts, fmt.Sprintf("%s=%d", family, res.DeletedByFamily[family]))
	}
	return strings.Join(parts, ",")
}

func (manager *Manager) PruneQueryDataUnderDiskPressure(currentHeight uint64) (QueryPruneResult, error) {
	usage, err := manager.DiskUsage()
	if err != nil {
		return QueryPruneResult{CurrentHeight: currentHeight, DeletedByFamily: make(map[string]uint64), DiskPath: manager.diskPath()}, err
	}
	return manager.PruneQueryDataWithDiskUsage(currentHeight, usage)
}

                                                                            
                                          
func (manager *Manager) PruneQueryDataWithDiskUsage(currentHeight uint64, usage diskspace.Snapshot) (QueryPruneResult, error) {
	res := QueryPruneResult{
		CurrentHeight:   currentHeight,
		DeletedByFamily: make(map[string]uint64),
		DiskPath:        usage.Path,
		DiskTotalBytes:  usage.TotalBytes,
		DiskFreeBytes:   usage.FreeBytes,
		DiskFreePct:     usage.FreePercent,
	}
	if res.DiskPath == "" {
		res.DiskPath = manager.diskPath()
	}
	if res.DiskTotalBytes == 0 {
		return res, fmt.Errorf("disk total bytes is zero")
	}

	settings := manager.blockPruneSettings()
	forceCompact := false
	switch {
	case res.DiskFreePct <= settings.aggressiveFreePercent:
		forceCompact = true
	case res.DiskFreePct <= settings.startFreePercent:
	default:
		return res, nil
	}
	if currentHeight <= settings.keepRecentHeights {
		return res, nil
	}

	archiveCursor, err := manager.blockPruneCursor()
	if err != nil {
		return res, err
	}
	if archiveCursor <= blockPruneFirstHeight {
		return res, nil
	}
	res.MaxPruneHeight = archiveCursor - 1
	if safeMax := currentHeight - settings.keepRecentHeights; res.MaxPruneHeight > safeMax {
		res.MaxPruneHeight = safeMax
	}
	if res.MaxPruneHeight == 0 {
		return res, nil
	}

	pruned, err := manager.pruneQueryDataThroughHeight(currentHeight, res.MaxPruneHeight, forceCompact)
	pruned.DiskPath = res.DiskPath
	pruned.DiskTotalBytes = res.DiskTotalBytes
	pruned.DiskFreeBytes = res.DiskFreeBytes
	pruned.DiskFreePct = res.DiskFreePct
	return pruned, err
}

func (manager *Manager) pruneQueryDataThroughHeight(currentHeight, maxPruneHeight uint64, forceCompact bool) (res QueryPruneResult, err error) {
	res = QueryPruneResult{
		CurrentHeight:   currentHeight,
		MaxPruneHeight:  maxPruneHeight,
		DeletedByFamily: make(map[string]uint64),
	}
	queryDB, err := manager.dbForStorageClass(keys.StorageQuery)
	if err != nil {
		return res, err
	}
	runtimeDB, err := manager.dbForStorageClass(keys.StorageRuntime)
	if err != nil {
		return res, err
	}
	archiveDB, err := manager.dbForStorageClass(keys.StorageArchive)
	if err != nil {
		return res, err
	}
	run := newQueryPruneDeleteRun(queryDB, runtimeDB, archiveDB, &res)
	defer func() {
		if closeErr := run.close(); err == nil && closeErr != nil {
			err = closeErr
		}
		if err == nil && forceCompact && res.DeletedKeys > 0 {
			if err = compactQueryPruneRanges(queryDB, run.compactRanges, &res); err == nil {
				err = compactQueryPruneRanges(runtimeDB, run.runtimeCompactRanges, &res)
			}
			if err == nil {
				err = compactQueryPruneRanges(archiveDB, run.archiveCompactRanges, &res)
			}
		}
	}()

	cursor, hasCursor, err := manager.queryPruneHeightCursor()
	if err != nil {
		return res, err
	}
	if !hasCursor {
		cursor = blockPruneFirstHeight
		if err = manager.setQueryPruneHeightCursor(cursor); err != nil {
			return res, err
		}
	}
	res.StartCursor = cursor
	res.NextCursor = cursor
	if cursor > maxPruneHeight {
		res.ArchiveAligned = true
		return res, nil
	}
	                                                                          
	                                                                       
	                                                                         
	if err = manager.flushColdWritesForQueryPrune(); err != nil {
		return res, err
	}

	newRecoveryGaps := 0
	for cursor <= maxPruneHeight {
		chunkCount, found, readErr := queryPruneManifestChunkCount(queryDB, cursor)
		if readErr != nil {
			return res, readErr
		}
		if !found {
			gap, gapFound, gapErr := manager.queryPruneSyncGap(cursor)
			if gapErr != nil {
				return res, gapErr
			}
			if gapFound {
				if gap.resumeHeight <= cursor {
					return res, fmt.Errorf("invalid query prune sync gap at height %d: resume=%d", cursor, gap.resumeHeight)
				}
				if err = manager.consumeQueryPruneSyncGap(cursor, gap); err != nil {
					return res, err
				}
				res.SkippedHeights += gap.resumeHeight - cursor
				cursor = gap.resumeHeight
				res.NextCursor = cursor
				continue
			}
			if err = manager.flushColdWritesForQueryPrune(); err != nil {
				return res, err
			}
			chunkCount, found, readErr = queryPruneManifestChunkCount(queryDB, cursor)
			if readErr != nil {
				return res, readErr
			}
		}
		if !found {
			if newRecoveryGaps >= queryPruneMaxNewRecoveryGapsPerRun {
				if manager.Logger != nil {
					manager.Logger.Error("[DB] query prune recovery gap limit reached; stopping current run without advancing cursor: height=%d quarantined=%d", cursor, newRecoveryGaps)
				} else {
					logs.Error("[DB] query prune recovery gap limit reached; stopping current run without advancing cursor: height=%d quarantined=%d", cursor, newRecoveryGaps)
				}
				                                                                
				                                                                 
				                                                                
				return res, nil
			}
			next, quarantineErr := manager.quarantineMissingQueryPruneManifest(cursor)
			if quarantineErr != nil {
				return res, quarantineErr
			}
			if manager.Logger != nil {
				manager.Logger.Warn("[DB] query prune quarantined missing manifest after cold flush: height=%d resume=%d", cursor, next)
			} else {
				logs.Warn("[DB] query prune quarantined missing manifest after cold flush: height=%d resume=%d", cursor, next)
			}
			newRecoveryGaps++
			res.SkippedHeights++
			cursor = next
			res.NextCursor = cursor
			continue
		}

		if err = manager.pruneQueryManifestHeight(run, cursor, chunkCount); err != nil {
			return res, err
		}
		next := cursor + 1
		if err = manager.setQueryPruneHeightCursor(next); err != nil {
			return res, err
		}
		if err = queryDB.Delete([]byte(keys.KeyQueryPruneManifestDone(cursor)), pebble.NoSync); err != nil {
			return res, err
		}
		res.ScannedHeights++
		res.PrunedHeights++
		cursor = next
		res.NextCursor = cursor
	}
	res.ArchiveAligned = cursor > maxPruneHeight
	return res, nil
}

func (manager *Manager) flushColdWritesForQueryPrune() error {
	return manager.forceFlushOn(manager.coldForceFlushChan)
}

func (manager *Manager) queryPruneHeightCursor() (uint64, bool, error) {
	raw, err := manager.GetKV(keys.KeyQueryPruneHeightCursor())
	if errors.Is(err, pebble.ErrNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	height, err := parseQueryPruneHeight(raw, keys.KeyQueryPruneHeightCursor())
	return height, err == nil, err
}

func (manager *Manager) setQueryPruneHeightCursor(height uint64) error {
	return manager.setQueryPruneHeightCursorWithOptions(height, pebble.Sync)
}

func (manager *Manager) setQueryPruneHeightCursorWithOptions(height uint64, options *pebble.WriteOptions) error {
	if height == 0 {
		return fmt.Errorf("query prune height cursor cannot be zero")
	}
	db, err := manager.dbForStorageClass(keys.StorageRuntime)
	if err != nil {
		return err
	}
	return db.Set([]byte(keys.KeyQueryPruneHeightCursor()), []byte(strconv.FormatUint(height, 10)), options)
}

func (manager *Manager) queryPruneSyncGap(startHeight uint64) (queryPruneGap, bool, error) {
	key := keys.KeyQueryPruneSyncGap(startHeight)
	raw, err := manager.GetKV(key)
	if errors.Is(err, pebble.ErrNotFound) {
		return queryPruneGap{}, false, nil
	}
	if err != nil {
		return queryPruneGap{}, false, err
	}
	if strings.HasPrefix(string(raw), queryPruneMissingManifestGapPrefix) {
		resumeHeight, parseErr := parseQueryPruneHeight(raw[len(queryPruneMissingManifestGapPrefix):], key)
		return queryPruneGap{resumeHeight: resumeHeight, persistent: true}, parseErr == nil, parseErr
	}
	resumeHeight, err := parseQueryPruneHeight(raw, key)
	return queryPruneGap{resumeHeight: resumeHeight}, err == nil, err
}

func (manager *Manager) consumeQueryPruneSyncGap(startHeight uint64, gap queryPruneGap) error {
	if gap.persistent {
		                                                                        
		                                                                        
		return manager.setQueryPruneHeightCursorWithOptions(gap.resumeHeight, pebble.Sync)
	}
	db, err := manager.dbForStorageClass(keys.StorageRuntime)
	if err != nil {
		return err
	}
	batch := db.NewBatch()
	defer batch.Close()
	if err := batch.Set([]byte(keys.KeyQueryPruneHeightCursor()), []byte(strconv.FormatUint(gap.resumeHeight, 10)), pebble.NoSync); err != nil {
		return err
	}
	if err := batch.Delete([]byte(keys.KeyQueryPruneSyncGap(startHeight)), pebble.NoSync); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

func (manager *Manager) quarantineMissingQueryPruneManifest(height uint64) (uint64, error) {
	if height == ^uint64(0) {
		return 0, fmt.Errorf("query prune recovery gap height overflows: %d", height)
	}
	resumeHeight := height + 1
	db, err := manager.dbForStorageClass(keys.StorageRuntime)
	if err != nil {
		return 0, err
	}
	batch := db.NewBatch()
	defer batch.Close()
	gapValue := queryPruneMissingManifestGapPrefix + strconv.FormatUint(resumeHeight, 10)
	if err := batch.Set([]byte(keys.KeyQueryPruneSyncGap(height)), []byte(gapValue), pebble.NoSync); err != nil {
		return 0, err
	}
	if err := batch.Set([]byte(keys.KeyQueryPruneHeightCursor()), []byte(strconv.FormatUint(resumeHeight, 10)), pebble.NoSync); err != nil {
		return 0, err
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return 0, err
	}
	return resumeHeight, nil
}

func (manager *Manager) recordQueryPruneStateSync(previousHeight, checkpointHeight uint64) error {
	if checkpointHeight <= previousHeight {
		return fmt.Errorf("query prune state sync height must advance: previous=%d checkpoint=%d", previousHeight, checkpointHeight)
	}
	if checkpointHeight == ^uint64(0) {
		return fmt.Errorf("query prune state sync height overflows: %d", checkpointHeight)
	}
	resumeHeight := checkpointHeight + 1
	cursor, hasCursor, err := manager.queryPruneHeightCursor()
	if err != nil {
		return err
	}
	if !hasCursor && previousHeight == 0 {
		return manager.setQueryPruneHeightCursorWithOptions(resumeHeight, pebble.Sync)
	}
	if hasCursor && cursor >= resumeHeight {
		return nil
	}
	startHeight := previousHeight + 1
	if hasCursor && cursor > startHeight {
		return fmt.Errorf("query prune cursor %d is inside state sync gap %d-%d", cursor, startHeight, checkpointHeight)
	}
	db, err := manager.dbForStorageClass(keys.StorageRuntime)
	if err != nil {
		return err
	}
	return db.Set([]byte(keys.KeyQueryPruneSyncGap(startHeight)), []byte(strconv.FormatUint(resumeHeight, 10)), pebble.Sync)
}

func (manager *Manager) pruneQueryManifestHeight(run *queryPruneDeleteRun, height uint64, chunkCount uint32) error {
	for chunk := uint32(0); chunk < chunkCount; chunk++ {
		raw, found, err := queryPruneManifestChunk(run.db, height, chunk)
		if err != nil {
			return err
		}
		if !found {
			                                                                    
			                                                                 
			continue
		}
		if err := visitQueryPruneManifest(raw, func(kind byte, value string) error {
			run.res.ScannedKeys++
			switch kind {
			case queryPruneManifestRecordKey:
				family, prefix, ok := queryPruneKeyFamily(value)
				if !ok || !keys.IsPrunableQueryHistoryKey(value) {
					return fmt.Errorf("unsafe query prune manifest key %q at height %d", queryPruneLogKey(value), height)
				}
				return run.deleteKey(value, family, prefix)
			case queryPruneManifestRecordArchiveKey:
				prefix := keys.PrunableArchiveHistoryPrefix(value)
				if prefix == "" || keys.IsStatefulKey(value) || keys.StorageClassForKey(value) != keys.StorageArchive {
					return fmt.Errorf("unsafe archive prune manifest key %q at height %d", queryPruneLogKey(value), height)
				}
				return run.deleteArchiveKey(value, prefix)
			case queryPruneManifestRecordReceipt:
				return run.deleteReceipt(value)
			case queryPruneManifestRecordNonceReceipt:
				if err := run.deleteReceipt(value); err != nil {
					return err
				}
				return run.deleteNonceAppliedTx(value)
			default:
				return fmt.Errorf("unknown query prune manifest record type %d at height %d", kind, height)
			}
		}); err != nil {
			return err
		}
		if err := run.deleteMetadataKey(keys.KeyQueryPruneManifestChunk(height, chunk)); err != nil {
			return err
		}
		if err := run.flush(); err != nil {
			return err
		}
	}
	                                                                            
	                                                                         
	                                                                          
	                                 
	if err := run.deleteKey(keys.KeyBlockReward(height), "block_reward", keys.KeyBlockRewardPrefix()); err != nil {
		return err
	}
	return run.flush()
}

type queryPruneDeleteRun struct {
	db                   *pebble.DB
	runtimeDB            *pebble.DB
	archiveDB            *pebble.DB
	batch                *pebble.Batch
	runtimeBatch         *pebble.Batch
	archiveBatch         *pebble.Batch
	res                  *QueryPruneResult
	pendingOps           int
	runtimePendingOps    int
	archivePendingOps    int
	compactRanges        map[string]queryPruneCompactRange
	runtimeCompactRanges map[string]queryPruneCompactRange
	archiveCompactRanges map[string]queryPruneCompactRange
}

type queryPruneCompactRange struct {
	prefix string
	start  string
	end    string
}

func newQueryPruneDeleteRun(db, runtimeDB, archiveDB *pebble.DB, res *QueryPruneResult) *queryPruneDeleteRun {
	return &queryPruneDeleteRun{
		db:                   db,
		runtimeDB:            runtimeDB,
		archiveDB:            archiveDB,
		batch:                db.NewBatch(),
		runtimeBatch:         runtimeDB.NewBatch(),
		archiveBatch:         archiveDB.NewBatch(),
		res:                  res,
		compactRanges:        make(map[string]queryPruneCompactRange),
		runtimeCompactRanges: make(map[string]queryPruneCompactRange),
		archiveCompactRanges: make(map[string]queryPruneCompactRange),
	}
}

func (run *queryPruneDeleteRun) deleteKey(key, family, prefix string) error {
	if key == "" {
		return nil
	}
	if err := run.batch.Delete([]byte(key), pebble.NoSync); err != nil {
		return err
	}
	run.pendingOps++
	run.res.DeletedKeys++
	run.res.DeletedByFamily[family]++
	run.recordCompactRange(family, prefix, key)
	if run.pendingOps+run.runtimePendingOps+run.archivePendingOps >= queryPruneBatchCommitOps {
		return run.flush()
	}
	return nil
}

func (run *queryPruneDeleteRun) deleteReceipt(txID string) error {
	if txID == "" {
		return nil
	}
	for _, item := range []struct {
		key    string
		family string
		prefix string
	}{
		{keys.KeyVMTxHeight(txID), "vm_tx_height", keys.KeyVMTxHeight("")},
		{keys.KeyVMTxFee(txID), "vm_tx_fee", keys.KeyVMTxFee("")},
		{keys.KeyVMTxFBBalance(txID), "vmtx_fbbal", keys.KeyVMTxFBBalance("")},
		{keys.KeyVMTxError(txID), "vm_tx_error", keys.KeyVMTxError("")},
		{keys.KeyTransferHistory(txID), "transfer_history", keys.KeyTransferHistory("")},
	} {
		if err := run.deleteKey(item.key, item.family, item.prefix); err != nil {
			return err
		}
	}
	return nil
}

func (run *queryPruneDeleteRun) deleteArchiveKey(key, prefix string) error {
	if err := run.archiveBatch.Delete([]byte(key), pebble.NoSync); err != nil {
		return err
	}
	run.archivePendingOps++
	run.res.DeletedKeys++
	family := strings.TrimSuffix(prefix, "_")
	run.res.DeletedByFamily[family]++
	recordQueryPruneCompactRange(run.archiveCompactRanges, family, prefix, key)
	if run.pendingOps+run.runtimePendingOps+run.archivePendingOps >= queryPruneBatchCommitOps {
		return run.flush()
	}
	return nil
}

func (run *queryPruneDeleteRun) deleteNonceAppliedTx(txID string) error {
	if txID == "" {
		return nil
	}
	key := keys.KeyVMAppliedTx(txID)
	if err := run.runtimeBatch.Delete([]byte(key), pebble.NoSync); err != nil {
		return err
	}
	run.runtimePendingOps++
	run.res.DeletedKeys++
	run.res.DeletedByFamily["vm_applied_tx"]++
	recordQueryPruneCompactRange(run.runtimeCompactRanges, "vm_applied_tx", keys.KeyVMAppliedTx(""), key)
	if run.pendingOps+run.runtimePendingOps+run.archivePendingOps >= queryPruneBatchCommitOps {
		return run.flush()
	}
	return nil
}

func (run *queryPruneDeleteRun) deleteMetadataKey(key string) error {
	if err := run.batch.Delete([]byte(key), pebble.NoSync); err != nil {
		return err
	}
	run.pendingOps++
	run.recordCompactRange("query_prune_manifest", keys.KeyQueryPruneManifestPrefix(), key)
	if run.pendingOps+run.runtimePendingOps+run.archivePendingOps >= queryPruneBatchCommitOps {
		return run.flush()
	}
	return nil
}

func (run *queryPruneDeleteRun) flush() error {
	                                                                           
	                                                                      
	if run.archivePendingOps > 0 {
		if err := run.archiveBatch.Commit(pebble.Sync); err != nil {
			return err
		}
		if err := run.archiveBatch.Close(); err != nil {
			return err
		}
		run.archiveBatch = run.archiveDB.NewBatch()
		run.archivePendingOps = 0
	}
	if run.runtimePendingOps > 0 {
		if err := run.runtimeBatch.Commit(pebble.Sync); err != nil {
			return err
		}
		if err := run.runtimeBatch.Close(); err != nil {
			return err
		}
		run.runtimeBatch = run.runtimeDB.NewBatch()
		run.runtimePendingOps = 0
	}
	if run.pendingOps > 0 {
		if err := run.batch.Commit(pebble.Sync); err != nil {
			return err
		}
		if err := run.batch.Close(); err != nil {
			return err
		}
		run.batch = run.db.NewBatch()
		run.pendingOps = 0
	}
	return nil
}

func (run *queryPruneDeleteRun) close() error {
	err := run.flush()
	closeErr := run.batch.Close()
	runtimeCloseErr := run.runtimeBatch.Close()
	archiveCloseErr := run.archiveBatch.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if runtimeCloseErr != nil {
		return runtimeCloseErr
	}
	return archiveCloseErr
}

func (run *queryPruneDeleteRun) recordCompactRange(family, prefix, key string) {
	recordQueryPruneCompactRange(run.compactRanges, family, prefix, key)
}

func recordQueryPruneCompactRange(ranges map[string]queryPruneCompactRange, family, prefix, key string) {
	if family == "" || prefix == "" || key == "" {
		return
	}
	next := key + "\x00"
	r, ok := ranges[family]
	if !ok {
		ranges[family] = queryPruneCompactRange{prefix: prefix, start: key, end: next}
		return
	}
	if key < r.start {
		r.start = key
	}
	if next > r.end {
		r.end = next
	}
	ranges[family] = r
}

func queryPruneKeyFamily(key string) (string, string, bool) {
	switch {
	case strings.HasPrefix(key, queryPrunePrefixes.userActivity):
		return "user_act", queryPrunePrefixes.userActivity, true
	case strings.HasPrefix(key, queryPrunePrefixes.spotAccountOrder):
		return "spot_acc_order", queryPrunePrefixes.spotAccountOrder, true
	case strings.HasPrefix(key, queryPrunePrefixes.futureAccountOrder):
		return "future_acc_order", queryPrunePrefixes.futureAccountOrder, true
	case strings.HasPrefix(key, queryPrunePrefixes.rechargeQuery):
		return "recharge_query_recent", queryPrunePrefixes.rechargeQuery, true
	case strings.HasPrefix(key, queryPrunePrefixes.frostWithdraw):
		return "frost_withdraw_query", queryPrunePrefixes.frostWithdraw, true
	case strings.HasPrefix(key, queryPrunePrefixes.trade):
		return "trade", queryPrunePrefixes.trade, true
	case strings.HasPrefix(key, queryPrunePrefixes.futureTrade):
		return "future_trade", queryPrunePrefixes.futureTrade, true
	case strings.HasPrefix(key, queryPrunePrefixes.futurePosition):
		return "future_pos_history", queryPrunePrefixes.futurePosition, true
	case strings.HasPrefix(key, queryPrunePrefixes.tradingFee):
		return "trading_fee", queryPrunePrefixes.tradingFee, true
	case strings.HasPrefix(key, queryPrunePrefixes.referralRebate):
		return "referral_rebate_record", queryPrunePrefixes.referralRebate, true
	default:
		return "", "", false
	}
}

func compactQueryPruneRanges(db *pebble.DB, ranges map[string]queryPruneCompactRange, res *QueryPruneResult) error {
	families := make([]string, 0, len(ranges))
	for family := range ranges {
		families = append(families, family)
	}
	sort.Strings(families)
	for _, family := range families {
		r := ranges[family]
		start := []byte(r.start)
		end := []byte(r.end)
		if prefixEnd := prefixUpperBound([]byte(r.prefix)); prefixEnd != nil && bytes.Compare(end, prefixEnd) > 0 {
			end = prefixEnd
		}
		if len(start) == 0 || len(end) == 0 || bytes.Compare(start, end) >= 0 {
			continue
		}
		if err := db.Compact(start, end, true); err != nil {
			return err
		}
		res.CompactedFamilies = append(res.CompactedFamilies, family)
	}
	return nil
}

func queryPruneLogKey(key string) string {
	const maxLen = 160
	if len(key) > maxLen {
		key = key[:maxLen] + "..."
	}
	return strings.ReplaceAll(key, "\x00", "\\x00")
}

func queryUserActivityPrefix() string {
	return strings.TrimSuffix(keys.KeyUserActivityPrefix(""), "_")
}

func queryTradingFeePrefix() string {
	return strings.TrimSuffix(keys.KeyTradingFeePrefix(""), "_")
}

func queryReferralRebatePrefix() string {
	return strings.TrimSuffix(keys.KeyReferralRebateRecordPrefix(""), "_")
}

func queryFrostWithdrawPrefix() string {
	return strings.TrimSuffix(keys.KeyFrostWithdrawQueryGlobalPrefix(), "global|")
}

func querySpotAccountOrderPrefix() string {
	return strings.TrimSuffix(keys.KeySpotAccountOrderOwnerPrefix(""), "owner:|")
}

func queryFutureAccountOrderPrefix() string {
	return strings.TrimSuffix(keys.KeyFutureAccountOrderOwnerPrefix(""), "owner:|")
}

func queryTradePrefix() string {
	return strings.TrimSuffix(keys.KeyTradeRecordPrefix(""), "_")
}

func queryFutureTradePrefix() string {
	return strings.TrimSuffix(keys.KeyFutureTradeRecordPrefix(""), "_")
}
