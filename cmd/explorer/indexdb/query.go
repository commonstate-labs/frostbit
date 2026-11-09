package indexdb

import (
	"encoding/json"
	"sort"
)

const (
	                                                                             
	                                                                            
	                                                             
	defaultAddressHistoryPageSize = 100
	maxAddressHistoryPageSize     = 500
	maxAddressHistoryOffset       = 5000
	maxAddressHistoryTypes        = 4
	maxRecentAllTypeBlockScan     = 512
	maxRecentTxPageSize           = 200
	maxIndexedBlockTxs            = 5000
)

                                                                            
                                                                                                
func (idb *IndexDB) GetAddressTxHistory(address string, typeSet map[string]struct{}, limit int) ([]*TxRecord, error) {
	return idb.GetAddressTxHistoryPage(address, typeSet, limit, 0)
}

func (idb *IndexDB) GetAddressTxHistoryPage(address string, typeSet map[string]struct{}, limit, offset int) ([]*TxRecord, error) {
	if limit <= 0 {
		limit = defaultAddressHistoryPageSize
	}
	if limit > maxAddressHistoryPageSize {
		limit = maxAddressHistoryPageSize
	}
	if offset < 0 {
		offset = 0
	}
	if offset > maxAddressHistoryOffset {
		offset = maxAddressHistoryOffset
	}
	scanLimit := limit + offset

	var rows []addressTxIndexRow
	if len(typeSet) == 0 {
		part, err := idb.ScanPrefix(KeyAddressTxPrefix(address), scanLimit)
		if err != nil {
			return nil, err
		}
		rows = make([]addressTxIndexRow, 0, len(part))
		for _, kv := range part {
			height, txIndex, ok := ParseAddressTxKey(kv.Key, address)
			if !ok {
				continue
			}
			rows = append(rows, addressTxIndexRow{kv: kv, height: height, txIndex: txIndex})
		}
	} else {
		types := make([]string, 0, len(typeSet))
		for txType := range typeSet {
			types = append(types, txType)
		}
		sort.Strings(types)
		if len(types) > maxAddressHistoryTypes {
			types = types[:maxAddressHistoryTypes]
		}
		for _, txType := range types {
			part, err := idb.ScanPrefix(KeyAddressTypeTxPrefix(address, txType), scanLimit)
			if err != nil {
				return nil, err
			}
			for _, kv := range part {
				height, txIndex, ok := ParseAddressTypeTxKey(kv.Key, address, txType)
				if !ok {
					continue
				}
				rows = append(rows, addressTxIndexRow{kv: kv, txType: txType, height: height, txIndex: txIndex})
			}
		}
	}

	                                                                            
	                                                                              
	                                             
	rows = dedupeAddressTxIndexRows(rows)
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].height != rows[j].height {
			return rows[i].height > rows[j].height
		}
		if rows[i].txIndex != rows[j].txIndex {
			return rows[i].txIndex < rows[j].txIndex
		}
		return rows[i].kv.Value < rows[j].kv.Value
	})
	if offset > 0 {
		if offset >= len(rows) {
			return []*TxRecord{}, nil
		}
		rows = rows[offset:]
	}
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return idb.addressTxRecordsFromIndexRows(typeSet, rows), nil
}

type addressTxIndexRow struct {
	kv      KV
	txType  string
	height  uint64
	txIndex int
}

func dedupeAddressTxIndexRows(rows []addressTxIndexRow) []addressTxIndexRow {
	byTxID := make(map[string]addressTxIndexRow, len(rows))
	for _, row := range rows {
		existing, exists := byTxID[row.kv.Value]
		if !exists || row.height < existing.height ||
			(row.height == existing.height && row.txIndex < existing.txIndex) {
			byTxID[row.kv.Value] = row
		}
	}
	out := make([]addressTxIndexRow, 0, len(byTxID))
	for _, row := range byTxID {
		out = append(out, row)
	}
	return out
}

func (idb *IndexDB) addressTxRecordsFromIndexRows(typeSet map[string]struct{}, rows []addressTxIndexRow) []*TxRecord {
	results := make([]*TxRecord, 0, len(rows))
	for _, row := range rows {
		record, err := idb.GetTxRecord(row.kv.Value)
		if err != nil || record == nil {
			continue
		}
		if len(typeSet) > 0 {
			if _, ok := typeSet[record.TxType]; !ok {
				continue
			}
		}
		normalized := *record
		normalized.Height = row.height
		normalized.TxIndex = row.txIndex
		results = append(results, &normalized)
	}
	return results
}

                                             
func (idb *IndexDB) GetTxRecord(txID string) (*TxRecord, error) {
	val, err := idb.Get(KeyTxDetail(txID))
	if err != nil {
		return nil, err
	}
	if val == "" {
		return nil, nil
	}

	var record TxRecord
	if err := json.Unmarshal([]byte(val), &record); err != nil {
		return nil, err
	}
	return &record, nil
}

                                              
func (idb *IndexDB) GetBlockTxs(height uint64) ([]*TxRecord, error) {
	prefix := KeyBlockTxPrefix(height)
	                                                                        
	                                                                              
	kvs, err := idb.ScanPrefix(prefix, maxIndexedBlockTxs)
	if err != nil {
		return nil, err
	}

	var results []*TxRecord
	for _, kv := range kvs {
		txID := kv.Value
		record, err := idb.GetTxRecord(txID)
		if err != nil {
			continue
		}
		if record != nil {
			normalized := *record
			if h, txIndex, ok := ParseBlockTxKey(kv.Key); ok {
				normalized.Height = h
				normalized.TxIndex = txIndex
			}
			results = append(results, &normalized)
		}
	}
	return results, nil
}

                                                                             
                                                                            
                                                                              
                        
func (idb *IndexDB) GetRecentBlockTxs(height uint64, limit int) ([]*TxRecord, error) {
	if limit <= 0 {
		limit = maxRecentTxPageSize
	}
	if limit > maxRecentTxPageSize {
		limit = maxRecentTxPageSize
	}
	prefix := KeyBlockTxPrefix(height)
	kvs, err := idb.ScanPrefixReverse(prefix, limit)
	if err != nil {
		return nil, err
	}
	results := make([]*TxRecord, 0, len(kvs))
	for _, kv := range kvs {
		record, err := idb.GetTxRecord(kv.Value)
		if err != nil || record == nil {
			continue
		}
		normalized := *record
		if h, txIndex, ok := ParseBlockTxKey(kv.Key); ok {
			normalized.Height = h
			normalized.TxIndex = txIndex
		}
		results = append(results, &normalized)
	}
	return results, nil
}

                                                                                                     
func (idb *IndexDB) GetRecentTxs(txType string, limit int) ([]*TxRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > maxRecentTxPageSize {
		limit = maxRecentTxPageSize
	}
	if txType != "" && txType != "all" {
		return idb.getRecentTxsByTypeIndex(txType, limit)
	}
	return idb.getRecentTxsByBlockScan(txType, limit)
}

func (idb *IndexDB) getRecentTxsByTypeIndex(txType string, limit int) ([]*TxRecord, error) {
	kvs, err := idb.ScanPrefix(KeyTxTypeTxPrefix(txType), limit)
	if err != nil {
		return nil, err
	}
	results := make([]*TxRecord, 0, len(kvs))
	for _, kv := range kvs {
		record, err := idb.GetTxRecord(kv.Value)
		if err != nil || record == nil {
			continue
		}
		normalized := *record
		if h, txIndex, ok := ParseTxTypeTxKey(kv.Key, txType); ok {
			normalized.Height = h
			normalized.TxIndex = txIndex
		}
		results = append(results, &normalized)
	}
	return results, nil
}

                                                                                           
func (idb *IndexDB) getRecentTxsByBlockScan(txType string, limit int) ([]*TxRecord, error) {
	syncH, err := idb.GetSyncHeight()
	if err != nil || syncH == 0 {
		return nil, err
	}

	filterAll := txType == "" || txType == "all"
	var results []*TxRecord
	baseHeight := uint64(1)
	if metadata, metadataErr := idb.GetHistoryMetadata(); metadataErr != nil {
		return nil, metadataErr
	} else if metadata != nil && metadata.BaseHeight > baseHeight {
		baseHeight = metadata.BaseHeight
	}
	if syncH < baseHeight {
		return []*TxRecord{}, nil
	}

	                                                                                                
	                                                                                  
	for h, scanned := syncH, 0; h >= baseHeight && len(results) < limit && scanned < maxRecentAllTypeBlockScan; h-- {
		scanned++
		txs, err := idb.GetRecentBlockTxs(h, limit-len(results))
		if err != nil {
			continue
		}
		for _, rec := range txs {
			if filterAll || rec.TxType == txType {
				results = append(results, rec)
			}
		}
		if h == baseHeight {
			break
		}
	}
	return results, nil
}
