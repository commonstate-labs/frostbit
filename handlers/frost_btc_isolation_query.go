package handlers

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"dex/frost/chain"
	"dex/frost/withdrawresult"
	"dex/keys"
	"dex/pb"
	"dex/protocol"
	"google.golang.org/protobuf/proto"
)

type btcIsolationPolicy struct {
	ProtocolVersion string `json:"protocol_version"`
	Active          bool   `json:"active"`
	IndexReady      bool   `json:"index_ready"`
	MinRechargeSats string `json:"min_recharge_sats"`
	MaxParts        uint32 `json:"max_parts"`
}

func queryBTCIsolationPolicy(ctx context.Context, store frostUTXOStore) (*btcIsolationPolicy, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	values, err := store.GetKVs([]string{keys.KeyProtocolVersion(), keys.KeyProtocolUpgradeReceipt(3), keys.KeyFrostBtcUtxoAmountBuildState()})
	if err != nil {
		return nil, err
	}
	read := func(key string) ([]byte, bool, error) { raw, ok := values[key]; return raw, ok, nil }
	active, err := chain.BTCIsolationActive(read)
	if err != nil {
		return nil, err
	}
	version := uint64(1)
	if raw, ok := values[keys.KeyProtocolVersion()]; ok {
		version, err = protocol.ParseStateVersion(raw)
		if err != nil {
			return nil, err
		}
	}
	result := &btcIsolationPolicy{ProtocolVersion: strconv.FormatUint(version, 10), Active: active, MaxParts: protocol.BTCIsolationMaxParts, MinRechargeSats: "0"}
	if active {
		var index pb.FrostBtcIsolationIndexState
		raw, ok := values[keys.KeyFrostBtcUtxoAmountBuildState()]
		if !ok || len(raw) > 4096 || proto.Unmarshal(raw, &index) != nil {
			return nil, fmt.Errorf("BTC amount index state unavailable")
		}
		result.IndexReady = index.Done
		result.MinRechargeSats = strconv.FormatUint(protocol.MinBTCRechargeSats, 10)
	}
	return result, ctx.Err()
}

func (hm *HandlerManager) HandleFrostBTCPolicy(w http.ResponseWriter, r *http.Request) {
	if !frostReadonlyGET(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		http.Error(w, "unexpected query", http.StatusBadRequest)
		return
	}
	if hm.dbManager == nil {
		http.Error(w, "store unavailable", http.StatusServiceUnavailable)
		return
	}
	policy, err := queryBTCIsolationPolicy(r.Context(), hm.dbManager)
	writeFrostReadonlyResult(w, policy, err)
}

type btcPartRow struct {
	JobID       string `json:"job_id,omitempty"`
	MigrationID string `json:"migration_id,omitempty"`
	ParentID    string `json:"parent_id"`
	PartSeq     string `json:"part_seq"`
	Status      string `json:"status"`
	GrossAmount string `json:"gross_amount,omitempty"`
	FeeAmount   string `json:"fee_amount,omitempty"`
	OriginID    string `json:"origin_id,omitempty"`
	VaultID     uint32 `json:"vault_id"`
}
type btcPartPage struct {
	ParentID   string       `json:"parent_id"`
	Items      []btcPartRow `json:"items"`
	Limit      int          `json:"limit"`
	HasMore    bool         `json:"has_more"`
	NextCursor string       `json:"next_cursor,omitempty"`
}

                                                                             
                                                                    
func parseBTCPartQuery(raw string, migration bool) (string, int, string, error) {
	if len(raw) > 4096 {
		return "", 0, "", fmt.Errorf("query exceeds limit")
	}
	q, err := url.ParseQuery(raw)
	if err != nil {
		return "", 0, "", err
	}
	idKey := "withdraw_id"
	if migration {
		idKey = "migration_id"
	}
	for key, values := range q {
		if (key != idKey && key != "limit" && key != "cursor") || len(values) != 1 {
			return "", 0, "", fmt.Errorf("unknown or duplicate parameter")
		}
	}
	id := q.Get(idKey)
	if !withdrawresult.ValidID(id) {
		return "", 0, "", fmt.Errorf("invalid parent ID")
	}
	limit := 50
	if q.Has("limit") {
		n, e := frostQueryUint(q.Get("limit"), 16)
		if e != nil || n < 1 || n > 200 {
			return "", 0, "", fmt.Errorf("limit must be between 1 and 200")
		}
		limit = int(n)
	}
	prefix := keys.KeyFrostBtcWithdrawPartPrefix(id)
	if migration {
		prefix = keys.KeyFrostBtcMigrationChildPrefix(id)
	}
	after := ""
	if cursor := q.Get("cursor"); cursor != "" {
		if len(cursor) > 1024 {
			return "", 0, "", fmt.Errorf("cursor exceeds limit")
		}
		b, e := base64.RawURLEncoding.Strict().DecodeString(cursor)
		if e != nil || base64.RawURLEncoding.EncodeToString(b) != cursor {
			return "", 0, "", fmt.Errorf("invalid cursor")
		}
		after = string(b)
		if !strings.HasPrefix(after, prefix) {
			return "", 0, "", fmt.Errorf("cursor does not match parent")
		}
		seq := strings.TrimPrefix(after, prefix)
		n, e := strconv.ParseUint(seq, 10, 64)
		if e != nil || n == 0 || fmt.Sprintf("%020d", n) != seq {
			return "", 0, "", fmt.Errorf("invalid cursor sequence")
		}
	}
	return id, limit, after, nil
}

                                                                          
                                                                        
func queryBTCParts(ctx context.Context, store frostUTXOStore, parent string, limit int, after string, migration bool) (*btcPartPage, error) {
	page := &btcPartPage{ParentID: parent, Items: []btcPartRow{}, Limit: limit}
	if limit < 1 || limit > 200 {
		return nil, fmt.Errorf("invalid limit")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	prefix := keys.KeyFrostBtcWithdrawPartPrefix(parent)
	if migration {
		prefix = keys.KeyFrostBtcMigrationChildPrefix(parent)
	}
	rows, err := store.ScanKeysWithLimitOrderedAfter(prefix, after, limit+1, false)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(rows) > limit+1 {
		return nil, fmt.Errorf("scan exceeded limit")
	}
	page.HasMore = len(rows) > limit
	if page.HasMore {
		rows = rows[:limit]
	}
	if len(rows) == 0 {
		return page, nil
	}
	refs, err := store.GetKVs(rows)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	readKeys := make([]string, 0, len(rows)*2)
	ids := make([]string, len(rows))
	previous := after
	for i, key := range rows {
		seq := strings.TrimPrefix(key, prefix)
		n, e := strconv.ParseUint(seq, 10, 64)
		if !strings.HasPrefix(key, prefix) || key <= previous || e != nil || n == 0 || fmt.Sprintf("%020d", n) != seq {
			return nil, fmt.Errorf("invalid part index order")
		}
		previous = key
		id := string(refs[key])
		if !withdrawresult.ValidID(id) {
			return nil, fmt.Errorf("invalid part reference")
		}
		ids[i] = id
		if migration {
			readKeys = append(readKeys, keys.KeyFrostBtcMigrationChildState(id))
		} else {
			readKeys = append(readKeys, keys.KeyFrostWithdrawJob(id), keys.KeyFrostPermanentWithdrawJob(id))
		}
	}
	values, err := store.GetKVs(readKeys)
	if err != nil {
		return nil, err
	}
	for i, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row := btcPartRow{ParentID: parent}
		if migration {
			var state pb.FrostVaultMigrationState
			if raw, ok := values[keys.KeyFrostBtcMigrationChildState(id)]; ok {
				if len(raw) > 64<<10 || proto.Unmarshal(raw, &state) != nil {
					return nil, fmt.Errorf("invalid migration child")
				}
			} else {
				return nil, fmt.Errorf("migration child unavailable")
			}
			if state.MigrationId != id || state.ParentMigrationId != parent || state.BtcProtocolVersion != 3 || keys.KeyFrostBtcMigrationChild(parent, state.BtcChildSeq) != rows[i] {
				return nil, fmt.Errorf("migration child identity mismatch")
			}
			row.MigrationID = id
			row.PartSeq = strconv.FormatUint(state.BtcChildSeq, 10)
			row.Status = state.Status
			row.VaultID = state.VaultId
			row.OriginID = hex.EncodeToString(state.BtcOriginId)
		} else {
			var job pb.FrostWithdrawJobState
			raw, ok := values[keys.KeyFrostWithdrawJob(id)]
			if !ok {
				raw, ok = values[keys.KeyFrostPermanentWithdrawJob(id)]
			}
			if !ok || len(raw) > 64<<10 || proto.Unmarshal(raw, &job) != nil {
				return nil, fmt.Errorf("withdrawal child unavailable")
			}
			if job.JobId != id || len(job.WithdrawIds) != 1 || job.WithdrawIds[0] != parent || job.BtcProtocolVersion != 3 || keys.KeyFrostBtcWithdrawPart(parent, job.BtcPartSeq) != rows[i] {
				return nil, fmt.Errorf("withdrawal child identity mismatch")
			}
			row.JobID = id
			row.PartSeq = strconv.FormatUint(job.BtcPartSeq, 10)
			row.Status = job.Status
			row.GrossAmount = job.BtcGrossAmount
			row.FeeAmount = job.BtcFeeAmount
			row.OriginID = hex.EncodeToString(job.BtcOriginId)
			row.VaultID = job.VaultId
		}
		page.Items = append(page.Items, row)
	}
	if page.HasMore {
		page.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(rows[len(rows)-1]))
	}
	return page, nil
}

func (hm *HandlerManager) handleBTCParts(w http.ResponseWriter, r *http.Request, migration bool) {
	if !frostReadonlyGET(w, r) {
		return
	}
	id, limit, after, err := parseBTCPartQuery(r.URL.RawQuery, migration)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if hm.dbManager == nil {
		http.Error(w, "store unavailable", http.StatusServiceUnavailable)
		return
	}
	page, err := queryBTCParts(r.Context(), hm.dbManager, id, limit, after, migration)
	writeFrostReadonlyResult(w, page, err)
}
func (hm *HandlerManager) HandleFrostWithdrawParts(w http.ResponseWriter, r *http.Request) {
	hm.handleBTCParts(w, r, false)
}
func (hm *HandlerManager) HandleFrostMigrationParts(w http.ResponseWriter, r *http.Request) {
	hm.handleBTCParts(w, r, true)
}
func (hm *HandlerManager) HandleFrostWithdrawPart(w http.ResponseWriter, r *http.Request) {
	if !frostReadonlyGET(w, r) {
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if len(r.URL.RawQuery) > 512 || err != nil || len(q) != 1 || len(q["job_id"]) != 1 || !withdrawresult.ValidID(q.Get("job_id")) {
		http.Error(w, "invalid job_id", http.StatusBadRequest)
		return
	}
	if hm.dbManager == nil {
		http.Error(w, "store unavailable", http.StatusServiceUnavailable)
		return
	}
	result, err := withdrawresult.ResolveBTCPart(r.Context(), hm.frostResultGet, q.Get("job_id"))
	writeFrostReadonlyResult(w, result, err)
}
