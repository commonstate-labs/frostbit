package db

import (
	"errors"
	"fmt"
	"strings"

	"dex/keys"
	"dex/pb"
	"dex/utils/diskspace"

	"github.com/cockroachdb/pebble"
	"google.golang.org/protobuf/proto"
)

const (
	frostPlanningLogPruneRowsPerHeight = 128
	frostPlanningLogPruneMaxRows       = 10_000
)

type FrostPlanningLogPruneResult struct {
	CurrentHeight uint64
	DiskFreePct   float64
	ScannedRows   int
	DeletedRows   int
	MalformedRows int
	NextCursor    string
	Wrapped       bool
	Compacted     bool
}

                                                                              
                                                                         
                                                                           
                                             
func (manager *Manager) PruneFrostPlanningLogsWithDiskUsage(currentHeight uint64, usage diskspace.Snapshot) (FrostPlanningLogPruneResult, error) {
	res := FrostPlanningLogPruneResult{CurrentHeight: currentHeight, DiskFreePct: usage.FreePercent}
	if usage.TotalBytes == 0 {
		return res, errors.New("disk total bytes is zero")
	}
	settings := manager.blockPruneSettings()
	heightBudget := 0
	aggressive := false
	switch {
	case usage.FreePercent <= settings.aggressiveFreePercent:
		heightBudget = settings.aggressiveHeightsPerBlock
		aggressive = true
	case usage.FreePercent <= settings.startFreePercent:
		heightBudget = settings.startHeightsPerBlock
	default:
		return res, nil
	}
	if heightBudget <= 0 {
		return res, nil
	}
	limit := heightBudget * frostPlanningLogPruneRowsPerHeight
	if limit > frostPlanningLogPruneMaxRows {
		limit = frostPlanningLogPruneMaxRows
	}

	cursor, err := manager.frostPlanningLogPruneCursor()
	if err != nil {
		return res, err
	}
	originalCursor := cursor
	prefix := keys.KeyFrostPlanningLogPrefix("")
	rows, err := manager.ScanKVWithLimitOrderedAfter(prefix, cursor, limit, false)
	if err != nil {
		return res, err
	}
	if len(rows) == 0 && cursor != "" {
		cursor = ""
		res.Wrapped = true
		rows, err = manager.ScanKVWithLimitOrdered(prefix, limit, false)
		if err != nil {
			return res, err
		}
	}
	res.ScannedRows = len(rows)

	queryDB, err := manager.dbForStorageClass(keys.StorageQuery)
	if err != nil {
		return res, err
	}
	batch := queryDB.NewBatch()
	defer batch.Close()
	var firstDeleted, lastDeleted string
	for _, row := range rows {
		var planning pb.FrostWithdrawPlanningLogTx
		if err := proto.Unmarshal(row.Value, &planning); err != nil || strings.TrimSpace(planning.WithdrawId) == "" {
			res.MalformedRows++
			continue
		}
		if !strings.HasPrefix(row.Key, keys.KeyFrostPlanningLogPrefix(planning.WithdrawId)) {
			res.MalformedRows++
			continue
		}
		terminal, err := manager.frostWithdrawPlanningLogTerminal(planning.WithdrawId)
		if err != nil {
			return res, err
		}
		if !terminal {
			continue
		}
		if err := batch.Delete([]byte(row.Key), pebble.NoSync); err != nil {
			return res, err
		}
		if firstDeleted == "" {
			firstDeleted = row.Key
		}
		lastDeleted = row.Key
		res.DeletedRows++
	}
	if res.DeletedRows > 0 {
		if err := batch.Commit(pebble.NoSync); err != nil {
			return res, err
		}
	}

	if len(rows) > 0 && len(rows) == limit {
		res.NextCursor = rows[len(rows)-1].Key
	} else {
		res.Wrapped = len(rows) > 0 || res.Wrapped
	}
	if res.NextCursor != originalCursor {
		if err := manager.setFrostPlanningLogPruneCursor(res.NextCursor); err != nil {
			return res, err
		}
	}
	if aggressive && res.DeletedRows > 0 {
		end := prefixUpperBound([]byte(lastDeleted))
		if end == nil {
			end = prefixUpperBound([]byte(prefix))
		}
		if end != nil {
			if err := queryDB.Compact([]byte(firstDeleted), end, true); err != nil {
				return res, err
			}
			res.Compacted = true
		}
	}
	return res, nil
}

func (manager *Manager) frostWithdrawPlanningLogTerminal(withdrawID string) (bool, error) {
	                                                                         
	                                                                             
	if raw, found, err := manager.GetState(keys.KeyFrostPermanentWithdraw(withdrawID)); err != nil {
		return false, err
	} else if found {
		var state pb.FrostWithdrawState
		if err := proto.Unmarshal(raw, &state); err != nil {
			return false, err
		}
		if state.WithdrawId != withdrawID || state.Status != "SIGNED" {
			return false, fmt.Errorf("invalid permanent withdrawal receipt: %s", withdrawID)
		}
		return true, nil
	}
	for _, key := range []string{keys.KeyFrostWithdraw(withdrawID), keys.KeyArchiveFrostWithdraw(withdrawID)} {
		raw, err := manager.GetKV(key)
		if errors.Is(err, pebble.ErrNotFound) {
			continue
		}
		if err != nil {
			return false, err
		}
		var state pb.FrostWithdrawState
		if err := proto.Unmarshal(raw, &state); err != nil {
			return false, err
		}
		return strings.EqualFold(state.Status, "SIGNED") || strings.EqualFold(state.Status, "CANCELLED"), nil
	}
	return false, nil
}

func (manager *Manager) frostPlanningLogPruneCursor() (string, error) {
	raw, err := manager.GetKV(keys.KeyFrostPlanningLogPruneCursor())
	if errors.Is(err, pebble.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	cursor := string(raw)
	if cursor != "" && !strings.HasPrefix(cursor, keys.KeyFrostPlanningLogPrefix("")) {
		return "", nil
	}
	return cursor, nil
}

func (manager *Manager) setFrostPlanningLogPruneCursor(cursor string) error {
	runtimeDB, err := manager.dbForStorageClass(keys.StorageRuntime)
	if err != nil {
		return err
	}
	if cursor == "" {
		return runtimeDB.Delete([]byte(keys.KeyFrostPlanningLogPruneCursor()), pebble.Sync)
	}
	return runtimeDB.Set([]byte(keys.KeyFrostPlanningLogPruneCursor()), []byte(cursor), pebble.Sync)
}
