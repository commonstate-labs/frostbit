package db

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"

	"dex/keys"

	"github.com/cockroachdb/pebble"
)

const (
	queryPruneManifestRecordKey          byte = 1
	queryPruneManifestRecordReceipt      byte = 2
	queryPruneManifestRecordNonceReceipt byte = 3
	queryPruneManifestRecordArchiveKey   byte = 4
)

var queryPruneManifestTargetBytes = 1 << 20

                                                                                               
                                                                             
                                                                         
func (manager *Manager) EnqueueQueryPruneManifest(height uint64, queryKeys, receiptTxIDs, nonceReceiptTxIDs []string) {
	if manager == nil || height == 0 {
		return
	}

	chunk := make([]byte, 0, queryPruneManifestTargetBytes)
	chunkIndex := uint32(0)
	flush := func() {
		if len(chunk) == 0 {
			return
		}
		manager.EnqueueSet(keys.KeyQueryPruneManifestChunk(height, chunkIndex), string(chunk))
		chunkIndex++
		chunk = make([]byte, 0, queryPruneManifestTargetBytes)
	}
	appendRecord := func(kind byte, value string) {
		if value == "" {
			return
		}
		recordBytes := 1 + binary.MaxVarintLen64 + len(value)
		if len(chunk) > 0 && len(chunk)+recordBytes > queryPruneManifestTargetBytes {
			flush()
		}
		chunk = append(chunk, kind)
		chunk = binary.AppendUvarint(chunk, uint64(len(value)))
		chunk = append(chunk, value...)
	}
	for _, key := range queryKeys {
		kind := queryPruneManifestRecordKey
		if keys.PrunableArchiveHistoryPrefix(key) != "" {
			kind = queryPruneManifestRecordArchiveKey
		}
		appendRecord(kind, key)
	}
	nonceReceipts := make(map[string]struct{}, len(nonceReceiptTxIDs))
	for _, txID := range nonceReceiptTxIDs {
		nonceReceipts[txID] = struct{}{}
	}
	for _, txID := range receiptTxIDs {
		kind := queryPruneManifestRecordReceipt
		if _, ok := nonceReceipts[txID]; ok {
			kind = queryPruneManifestRecordNonceReceipt
		}
		appendRecord(kind, txID)
	}
	flush()
	manager.EnqueueSet(keys.KeyQueryPruneManifestDone(height), strconv.FormatUint(uint64(chunkIndex), 10))
}

func queryPruneManifestChunkCount(db *pebble.DB, height uint64) (uint32, bool, error) {
	raw, closer, err := db.Get([]byte(keys.KeyQueryPruneManifestDone(height)))
	if errors.Is(err, pebble.ErrNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	defer closer.Close()
	count, err := strconv.ParseUint(string(raw), 10, 32)
	if err != nil {
		return 0, false, fmt.Errorf("invalid query prune manifest chunk count at height %d: %w", height, err)
	}
	return uint32(count), true, nil
}

func queryPruneManifestChunk(db *pebble.DB, height uint64, chunk uint32) ([]byte, bool, error) {
	raw, closer, err := db.Get([]byte(keys.KeyQueryPruneManifestChunk(height, chunk)))
	if errors.Is(err, pebble.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer closer.Close()
	out := append([]byte(nil), raw...)
	return out, true, nil
}

func visitQueryPruneManifest(raw []byte, visit func(kind byte, value string) error) error {
	for len(raw) > 0 {
		kind := raw[0]
		raw = raw[1:]
		length, n := binary.Uvarint(raw)
		if n <= 0 {
			return fmt.Errorf("invalid query prune manifest record length")
		}
		raw = raw[n:]
		if length == 0 || length > uint64(len(raw)) {
			return fmt.Errorf("invalid query prune manifest record size %d", length)
		}
		if err := visit(kind, string(raw[:length])); err != nil {
			return err
		}
		raw = raw[length:]
	}
	return nil
}

func parseQueryPruneHeight(raw []byte, key string) (uint64, error) {
	height, err := strconv.ParseUint(string(raw), 10, 64)
	if err != nil || height == 0 {
		return 0, fmt.Errorf("invalid %s value %q", key, string(raw))
	}
	return height, nil
}
