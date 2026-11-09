package indexdb

import (
	"encoding/json"
	"fmt"

	"github.com/cockroachdb/pebble"
)

const HistoryMetadataVersion = 1

                                                                                    
                                                                                                     
type HistoryMetadata struct {
	Version    int    `json:"version"`
	BaseHeight uint64 `json:"base_height"`
	Partial    bool   `json:"partial"`
	Reason     string `json:"reason,omitempty"`
}

func (idb *IndexDB) GetHistoryMetadata() (*HistoryMetadata, error) {
	raw, err := idb.Get(KeyHistoryMetadata)
	if err != nil || raw == "" {
		return nil, err
	}
	var metadata HistoryMetadata
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		return nil, fmt.Errorf("invalid history metadata: %w", err)
	}
	if metadata.Version != HistoryMetadataVersion || metadata.BaseHeight == 0 {
		return nil, fmt.Errorf("invalid history metadata: version=%d base_height=%d", metadata.Version, metadata.BaseHeight)
	}
	return &metadata, nil
}

                                                                                                    
                                                                                                
func (idb *IndexDB) SetPartialHistoryBase(baseHeight uint64, reason string) error {
	if baseHeight <= 1 {
		return fmt.Errorf("partial history base height must be greater than 1: %d", baseHeight)
	}
	metadata := HistoryMetadata{Version: HistoryMetadataVersion, BaseHeight: baseHeight, Partial: true, Reason: reason}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	return idb.Set(KeyHistoryMetadata, string(raw))
}

                                                                                                      
                                                                           
func (idb *IndexDB) IsHistoryEmpty() (bool, error) {
	iter, err := idb.db.NewIter(nil)
	if err != nil {
		return false, err
	}
	defer iter.Close()
	for iter.First(); iter.Valid(); iter.Next() {
		switch string(iter.Key()) {
		case KeySyncNode, KeyHistoryMetadata:
			continue
		case KeySyncHeight:
			if string(iter.Value()) == "0" {
				continue
			}
			return false, nil
		default:
			return false, nil
		}
	}
	if err := iter.Error(); err != nil && err != pebble.ErrNotFound {
		return false, err
	}
	return true, nil
}
