package indexdb

import (
	"encoding/json"
	"fmt"
	"strconv"
	"sync"

	"github.com/cockroachdb/pebble"
)

                                                                        
                                                                             
                                                                 
type heightManifest struct {
	Keys                   []string         `json:"keys"`
	TxIDs                  []string         `json:"tx_ids,omitempty"`
	AddressDeltas          map[string]int   `json:"address_deltas,omitempty"`
	ReadModelSchemaVersion uint32           `json:"read_model_schema_version,omitempty"`
	ReadModelDigest        string           `json:"read_model_digest,omitempty"`
	OwnedKeys              []heightOwnedKey `json:"owned_keys,omitempty"`
	txIDSet                map[string]struct{}
}

                                                                         
                                                                               
                                                  
type heightOwnedKey struct {
	Key         string `json:"key"`
	OwnerHeight uint64 `json:"owner_height"`
}

func (m *heightManifest) add(key string) {
	if key != "" {
		m.Keys = append(m.Keys, key)
	}
}

func (m *heightManifest) addTxID(txID string) bool {
	if txID == "" {
		return false
	}
	if m.txIDSet == nil {
		m.txIDSet = make(map[string]struct{}, len(m.TxIDs)+1)
		for _, existing := range m.TxIDs {
			m.txIDSet[existing] = struct{}{}
		}
	}
	if _, exists := m.txIDSet[txID]; exists {
		return false
	}
	m.txIDSet[txID] = struct{}{}
	m.TxIDs = append(m.TxIDs, txID)
	return true
}

var retentionMu sync.Mutex

const defaultRetentionPruneHeightsPerRun = 100

                                                                             
                                                                               
                                                                               
                         
type RetentionResult struct {
	CurrentHeight    uint64
	KeepHeights      uint64
	TargetHeight     uint64
	CursorBefore     uint64
	CursorAfter      uint64
	ProcessedHeights int
	PrunedHeights    int
	PendingHeights   uint64
}

                                                                          
                                                                       
                                                                          
         
  
                                                                            
                                                                                
                                                                    
func (idb *IndexDB) PruneToKeep(currentHeight, keepHeights uint64) error {
	return idb.PruneToKeepLimited(currentHeight, keepHeights, defaultRetentionPruneHeightsPerRun)
}

                                                                            
                                                                            
                                                                                
func (idb *IndexDB) PruneToKeepLimited(currentHeight, keepHeights uint64, maxHeights int) error {
	_, err := idb.PruneToKeepLimitedResult(currentHeight, keepHeights, maxHeights)
	return err
}

                                                                            
                                                                           
                                                                             
func (idb *IndexDB) PruneToKeepLimitedResult(currentHeight, keepHeights uint64, maxHeights int) (RetentionResult, error) {
	result := RetentionResult{CurrentHeight: currentHeight, KeepHeights: keepHeights}
	if idb == nil || keepHeights == 0 || currentHeight <= keepHeights {
		return result, nil
	}
	if maxHeights <= 0 {
		maxHeights = defaultRetentionPruneHeightsPerRun
	}
	target := currentHeight - keepHeights
	result.TargetHeight = target
	retentionMu.Lock()
	defer retentionMu.Unlock()

	cursor, err := idb.retentionCursor()
	if err != nil {
		return result, err
	}
	result.CursorBefore = cursor
	if cursor == 0 {
		cursor = 1
		if history, historyErr := idb.GetHistoryMetadata(); historyErr == nil && history != nil && history.BaseHeight > cursor {
			cursor = history.BaseHeight
		}
		result.CursorBefore = cursor
		manifest, manifestErr := idb.Get(KeyHeightManifest(cursor))
		if manifestErr != nil {
			return result, manifestErr
		}
		if manifest == "" {
			                                                                    
			                                                                       
			                                                                    
			                  
			result.CursorAfter = currentHeight + 1
			if err := idb.Set(KeyRetentionCursor, strconv.FormatUint(result.CursorAfter, 10)); err != nil {
				return result, err
			}
			return result, nil
		}
	}
	result.CursorAfter = cursor
	for height := cursor; height <= target && result.ProcessedHeights < maxHeights; height++ {
		pruned, err := idb.pruneHeight(height)
		if err != nil {
			return result, err
		}
		result.ProcessedHeights++
		if pruned {
			result.PrunedHeights++
		}
		result.CursorAfter = height + 1
	}
	if result.CursorAfter <= target {
		result.PendingHeights = target - result.CursorAfter + 1
	}
	return result, nil
}

func (idb *IndexDB) retentionCursor() (uint64, error) {
	raw, err := idb.Get(KeyRetentionCursor)
	if err != nil || raw == "" {
		return 0, err
	}
	return strconv.ParseUint(raw, 10, 64)
}

func (idb *IndexDB) pruneHeight(height uint64) (bool, error) {
	idb.writeMu.Lock()
	defer idb.writeMu.Unlock()

	raw, err := idb.Get(KeyHeightManifest(height))
	if err != nil {
		return false, err
	}
	if raw == "" {
		                                                                        
		                                           
		return false, idb.Set(KeyRetentionCursor, strconv.FormatUint(height+1, 10))
	}
	var manifest heightManifest
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return false, fmt.Errorf("decode retention manifest at height %d: %w", height, err)
	}

	batch := idb.db.NewBatch()
	defer batch.Close()
	for _, key := range manifest.Keys {
		if err := batch.Delete([]byte(key), pebble.NoSync); err != nil {
			return false, err
		}
	}
	for _, owned := range manifest.OwnedKeys {
		if owned.Key == "" {
			return false, fmt.Errorf("invalid read-model ownership in manifest at height %d", height)
		}
		ownerKey := KeyReadModelOwner(owned.Key)
		ownerRaw, err := idb.Get(ownerKey)
		if err != nil {
			return false, err
		}
		if ownerRaw == "" {
			continue
		}
		ownerHeight, err := strconv.ParseUint(ownerRaw, 10, 64)
		if err != nil {
			return false, fmt.Errorf("invalid read-model owner for %q: %w", owned.Key, err)
		}
		if ownerHeight != owned.OwnerHeight {
			continue
		}
		if err := batch.Delete([]byte(owned.Key), pebble.NoSync); err != nil {
			return false, err
		}
		if err := batch.Delete([]byte(ownerKey), pebble.NoSync); err != nil {
			return false, err
		}
	}
	for _, txID := range manifest.TxIDs {
		refKey := KeyTxDetailRef(txID)
		refRaw, err := idb.Get(refKey)
		if err != nil {
			return false, err
		}
		refs, err := strconv.Atoi(refRaw)
		if err != nil || refs <= 0 {
			                                                               
			                                                                
			                                         
			continue
		}
		if refs == 1 {
			if err := batch.Delete([]byte(refKey), pebble.NoSync); err != nil {
				return false, err
			}
			if err := batch.Delete([]byte(KeyTxDetail(txID)), pebble.NoSync); err != nil {
				return false, err
			}
			continue
		}
		if err := batch.Set([]byte(refKey), []byte(strconv.Itoa(refs-1)), pebble.NoSync); err != nil {
			return false, err
		}
	}
	for address, delta := range manifest.AddressDeltas {
		if delta <= 0 {
			continue
		}
		key := KeyAddressCount(address)
		currentRaw, err := idb.Get(key)
		if err != nil {
			return false, err
		}
		current, _ := strconv.Atoi(currentRaw)
		current -= delta
		if current <= 0 {
			if err := batch.Delete([]byte(key), pebble.NoSync); err != nil {
				return false, err
			}
		} else if err := batch.Set([]byte(key), []byte(strconv.Itoa(current)), pebble.NoSync); err != nil {
			return false, err
		}
	}
	if err := batch.Delete([]byte(KeyHeightManifest(height)), pebble.NoSync); err != nil {
		return false, err
	}
	if err := batch.Set([]byte(KeyRetentionCursor), []byte(strconv.FormatUint(height+1, 10)), pebble.NoSync); err != nil {
		return false, err
	}
	metadata := HistoryMetadata{
		Version:    HistoryMetadataVersion,
		BaseHeight: height + 1,
		Partial:    true,
		Reason:     "explorer retention window",
	}
	if existing, existingErr := idb.GetHistoryMetadata(); existingErr == nil && existing != nil && existing.BaseHeight > metadata.BaseHeight {
		metadata = *existing
	}
	metadataRaw, err := json.Marshal(metadata)
	if err != nil {
		return false, err
	}
	if err := batch.Set([]byte(KeyHistoryMetadata), metadataRaw, pebble.NoSync); err != nil {
		return false, err
	}
	if manifest.ReadModelSchemaVersion != 0 {
		baseRaw, err := idb.Get(KeyReadModelBaseHeight)
		if err != nil {
			return false, err
		}
		if baseRaw != "" {
			baseHeight, parseErr := strconv.ParseUint(baseRaw, 10, 64)
			if parseErr != nil {
				return false, fmt.Errorf("invalid read-model base height: %w", parseErr)
			}
			if baseHeight <= height {
				if err := batch.Set([]byte(KeyReadModelBaseHeight), []byte(strconv.FormatUint(height+1, 10)), pebble.NoSync); err != nil {
					return false, err
				}
			}
		}
	}
	if err := batch.Commit(pebble.NoSync); err != nil {
		return false, err
	}
	return true, nil
}
