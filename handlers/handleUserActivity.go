package handlers

import (
	"dex/keys"
	"encoding/json"
	"net/http"
	"strconv"
)

                                                                 
                                                                                                           
                                                                           
func (hm *HandlerManager) HandleUserActivity(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	q := r.URL.Query()
	addr := q.Get("address")
	if addr == "" {
		json.NewEncoder(w).Encode(map[string]string{"error": "missing address"})
		return
	}
	kindFilter := q.Get("kind")                
	offset := parseIntDefault(q.Get("offset"), 0, 0, 5_000)
	limit := parseIntDefault(q.Get("limit"), 50, 1, 500)
	scanBudget := offset + limit + 1

	type item struct {
		Key string          `json:"key"`
		Rec json.RawMessage `json:"record"`
	}
	page := make([]item, 0, limit)
	hasMore := false
	if kindFilter == "" {
		rows, err := hm.dbManager.ScanKVWithLimitOrdered(keys.KeyUserActivityPrefix(addr), scanBudget, true)
		if err != nil {
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		if offset > len(rows) {
			offset = len(rows)
		}
		rows = rows[offset:]
		if len(rows) > limit {
			hasMore = true
			rows = rows[:limit]
		}
		for _, row := range rows {
			page = append(page, item{Key: row.Key, Rec: row.Value})
		}
	} else {
		refs, err := hm.dbManager.ScanKVWithLimitOrdered(keys.KeyUserActivityKindIndexPrefix(addr, kindFilter), scanBudget, false)
		if err != nil {
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		if offset > len(refs) {
			offset = len(refs)
		}
		refs = refs[offset:]
		if len(refs) > limit {
			hasMore = true
			refs = refs[:limit]
		}
		for _, ref := range refs {
			canonicalKey := string(ref.Value)
			if canonicalKey == "" {
				continue
			}
			raw, err := hm.dbManager.Get(canonicalKey)
			if err == nil && len(raw) > 0 {
				page = append(page, item{Key: canonicalKey, Rec: raw})
			}
		}
	}
	total := offset + len(page)
	if hasMore {
		total++
	}

	json.NewEncoder(w).Encode(map[string]any{
		"address":  addr,
		"total":    total,
		"has_more": hasMore,
		"offset":   offset,
		"limit":    limit,
		"items":    page,
	})
}

func parseIntDefault(s string, def, min, max int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}
