package indexdb

import (
	"encoding/json"

	"dex/keys"
)

const (
	defaultReadModelActivityLimit = 50
	defaultReadModelOrderLimit    = 100
	maxReadModelQueryLimit        = 500
	maxReadModelActivityOffset    = 5000
)

type UserActivityItem struct {
	Key    string          `json:"key"`
	Record json.RawMessage `json:"record"`
}

type UserActivityPage struct {
	Items   []UserActivityItem `json:"items"`
	Total   int                `json:"total"`
	Offset  int                `json:"offset"`
	Limit   int                `json:"limit"`
	HasMore bool               `json:"has_more"`
}

                                                                          
                                                                           
func (idb *IndexDB) GetUserActivities(address, kind string, limit, offset int) (*UserActivityPage, error) {
	limit = boundedReadModelLimit(limit, defaultReadModelActivityLimit)
	if offset < 0 {
		offset = 0
	}
	if offset > maxReadModelActivityOffset {
		offset = maxReadModelActivityOffset
	}
	scanLimit := offset + limit + 1
	page := &UserActivityPage{Items: make([]UserActivityItem, 0, limit), Offset: offset, Limit: limit}

	if kind == "" {
		rows, err := idb.ScanPrefixReverse(keys.KeyUserActivityPrefix(address), scanLimit)
		if err != nil {
			return nil, err
		}
		rows = offsetRows(rows, offset)
		if len(rows) > limit {
			page.HasMore = true
			rows = rows[:limit]
		}
		for _, row := range rows {
			page.Items = append(page.Items, UserActivityItem{Key: row.Key, Record: json.RawMessage([]byte(row.Value))})
		}
	} else {
		refs, err := idb.ScanPrefix(keys.KeyUserActivityKindIndexPrefix(address, kind), scanLimit)
		if err != nil {
			return nil, err
		}
		refs = offsetRows(refs, offset)
		if len(refs) > limit {
			page.HasMore = true
			refs = refs[:limit]
		}
		for _, ref := range refs {
			if ref.Value == "" {
				continue
			}
			raw, err := idb.Get(ref.Value)
			if err != nil {
				return nil, err
			}
			if raw == "" {
				continue
			}
			page.Items = append(page.Items, UserActivityItem{Key: ref.Value, Record: json.RawMessage([]byte(raw))})
		}
	}
	page.Total = page.Offset + len(page.Items)
	if page.HasMore {
		page.Total++
	}
	return page, nil
}

                                                                           
                                                                               
                                             
func (idb *IndexDB) ScanSpotAccountOrders(address, pair, status string, limit int) (map[string][]byte, error) {
	limit = boundedReadModelLimit(limit, defaultReadModelOrderLimit)
	statuses := keys.SpotAccountOrderStatusKeys()
	if status != "" && status != "all" {
		statuses = []string{status}
	}
	out := make(map[string][]byte)
	for _, statusKey := range statuses {
		if pair == "" {
			refs, err := idb.ScanPrefix(keys.KeySpotAccountOrderRecentPrefix(address, statusKey), limit)
			if err != nil {
				return nil, err
			}
			if err := idb.hydrateOrderPointers(out, refs); err != nil {
				return nil, err
			}
			continue
		}
		rows, err := idb.ScanPrefix(keys.KeySpotAccountOrderPrefix(address, pair, statusKey), limit)
		if err != nil {
			return nil, err
		}
		appendRawRows(out, rows)
	}
	return out, nil
}

                                                        
                                                                      
func (idb *IndexDB) ScanFutureAccountOrders(address, pair, status string, limit int) (map[string][]byte, error) {
	limit = boundedReadModelLimit(limit, defaultReadModelOrderLimit)
	statuses := keys.FutureAccountOrderStatusKeys()
	if status != "" && status != "all" {
		statuses = []string{status}
	}
	out := make(map[string][]byte)
	for _, statusKey := range statuses {
		if pair == "" {
			refs, err := idb.ScanPrefix(keys.KeyFutureAccountOrderRecentPrefix(address, statusKey), limit)
			if err != nil {
				return nil, err
			}
			if err := idb.hydrateOrderPointers(out, refs); err != nil {
				return nil, err
			}
			continue
		}
		rows, err := idb.ScanPrefix(keys.KeyFutureAccountOrderPrefix(address, pair, statusKey), limit)
		if err != nil {
			return nil, err
		}
		appendRawRows(out, rows)
	}
	return out, nil
}

func (idb *IndexDB) hydrateOrderPointers(out map[string][]byte, refs []KV) error {
	for _, ref := range refs {
		if ref.Value == "" {
			continue
		}
		raw, err := idb.Get(ref.Value)
		if err != nil {
			return err
		}
		if raw != "" {
			out[ref.Key] = []byte(raw)
		}
	}
	return nil
}

func appendRawRows(out map[string][]byte, rows []KV) {
	for _, row := range rows {
		out[row.Key] = []byte(row.Value)
	}
}

func offsetRows(rows []KV, offset int) []KV {
	if offset >= len(rows) {
		return rows[len(rows):]
	}
	return rows[offset:]
}

func boundedReadModelLimit(limit, fallback int) int {
	if limit <= 0 {
		return fallback
	}
	if limit > maxReadModelQueryLimit {
		return maxReadModelQueryLimit
	}
	return limit
}
