package handlers

import (
	"dex/interfaces"
	"dex/keys"
	"dex/pb"
	"dex/utils"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"
)

const (
	defaultRechargeRewardLimit = 20
	maxRechargeRewardLimit     = 100
	maxRechargeRewardBatch     = 20
)

var rechargeRewardRequestID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

type rechargeRewardStore interface {
	ScanKVWithLimitOrderedAfter(string, string, int, bool) ([]interfaces.KVPair, error)
	GetKVs([]string) (map[string][]byte, error)
}

type rechargeRewardItem struct {
	RequestID             string `json:"request_id"`
	TokenAddress          string `json:"token_address"`
	ReceiverNetAmount     string `json:"receiver_net_amount,omitempty"`
	RequesterRewardAmount string `json:"requester_reward_amount,omitempty"`
	WitnessRewardAmount   string `json:"witness_reward_amount,omitempty"`
	FinalizeHeight        uint64 `json:"finalize_height"`
}

type rechargeRewardResponse struct {
	Owner             string               `json:"owner"`
	Items             []rechargeRewardItem `json:"items"`
	MissingRequestIDs []string             `json:"missing_request_ids,omitempty"`
	NextAfter         string               `json:"next_after,omitempty"`
}

func decodeRechargeReward(key, owner string, raw []byte) (rechargeRewardItem, error) {
	var entry pb.RechargeRewardAttribution
	if len(raw) == 0 || proto.Unmarshal(raw, &entry) != nil ||
		!rechargeRewardRequestID.MatchString(entry.RequestId) ||
		key != keys.KeyRechargeRewardOwner(owner, entry.RequestId) ||
		entry.TokenAddress == "" || entry.FinalizeHeight == 0 {
		return rechargeRewardItem{}, fmt.Errorf("invalid recharge reward attribution")
	}
	                                                                          
	                                                                        
	hasPositiveCredit := false
	for _, amount := range []string{entry.ReceiverNetAmount, entry.RequesterRewardAmount, entry.WitnessRewardAmount} {
		if amount != "" && !validRechargeRewardAmount(amount) {
			return rechargeRewardItem{}, fmt.Errorf("invalid recharge reward amount")
		}
		if amount != "" && amount != "0" {
			hasPositiveCredit = true
		}
	}
	if !hasPositiveCredit {
		return rechargeRewardItem{}, fmt.Errorf("empty recharge reward attribution")
	}
	return rechargeRewardItem{
		RequestID: entry.RequestId, TokenAddress: entry.TokenAddress,
		ReceiverNetAmount: entry.ReceiverNetAmount, RequesterRewardAmount: entry.RequesterRewardAmount,
		WitnessRewardAmount: entry.WitnessRewardAmount, FinalizeHeight: entry.FinalizeHeight,
	}, nil
}

func validRechargeRewardAmount(amount string) bool {
	if amount == "" || (len(amount) > 1 && amount[0] == '0') {
		return false
	}
	for _, c := range amount {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func queryRechargeRewards(store rechargeRewardStore, owner string, requestIDs []string, after string, limit int) (rechargeRewardResponse, error) {
	resp := rechargeRewardResponse{Owner: owner, Items: []rechargeRewardItem{}}
	if len(requestIDs) > 0 {
		keysToRead := make([]string, len(requestIDs))
		for i, id := range requestIDs {
			keysToRead[i] = keys.KeyRechargeRewardOwner(owner, id)
		}
		values, err := store.GetKVs(keysToRead)
		if err != nil {
			return resp, err
		}
		for i, key := range keysToRead {
			raw, found := values[key]
			if !found {
				resp.MissingRequestIDs = append(resp.MissingRequestIDs, requestIDs[i])
				continue
			}
			item, err := decodeRechargeReward(key, owner, raw)
			if err != nil {
				return resp, err
			}
			resp.Items = append(resp.Items, item)
		}
		return resp, nil
	}
	prefix, start := keys.KeyRechargeRewardOwnerPrefix(owner), ""
	if after != "" {
		start = keys.KeyRechargeRewardOwner(owner, after)
	}
	rows, err := store.ScanKVWithLimitOrderedAfter(prefix, start, limit+1, false)
	if err != nil {
		return resp, err
	}
	for i, row := range rows {
		if i == limit {
			resp.NextAfter = resp.Items[len(resp.Items)-1].RequestID
			break
		}
		item, err := decodeRechargeReward(row.Key, owner, row.Value)
		if err != nil {
			return resp, err
		}
		resp.Items = append(resp.Items, item)
	}
	return resp, nil
}

                                                                        
                                                                    
func (hm *HandlerManager) HandleWitnessRewards(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if len(r.URL.RawQuery) > 4096 {
		http.Error(w, "reward query too long", http.StatusBadRequest)
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		http.Error(w, "invalid reward query encoding", http.StatusBadRequest)
		return
	}
	if len(q["after"]) > 1 || len(q["limit"]) > 1 {
		http.Error(w, "duplicate reward pagination parameter", http.StatusBadRequest)
		return
	}
	for key := range q {
		if key != "owner" && key != "request_id" && key != "after" && key != "limit" {
			http.Error(w, "unknown reward query parameter", http.StatusBadRequest)
			return
		}
	}
	owner := q.Get("owner")
	if !utils.IsDexAddress(owner) || len(q["owner"]) != 1 {
		http.Error(w, "invalid owner", http.StatusBadRequest)
		return
	}
	requestIDs := q["request_id"]
	if len(requestIDs) > 0 && q.Has("after") {
		http.Error(w, "invalid reward batch", http.StatusBadRequest)
		return
	}
	seen := make(map[string]struct{}, len(requestIDs))
	unique := make([]string, 0, len(requestIDs))
	for _, id := range requestIDs {
		if !rechargeRewardRequestID.MatchString(id) {
			http.Error(w, "invalid request_id", http.StatusBadRequest)
			return
		}
		if _, exists := seen[id]; !exists {
			seen[id] = struct{}{}
			unique = append(unique, id)
			if len(unique) > maxRechargeRewardBatch {
				http.Error(w, "reward batch exceeds 20 distinct requests", http.StatusBadRequest)
				return
			}
		}
	}
	after := q.Get("after")
	if after != "" && !rechargeRewardRequestID.MatchString(after) || (q.Has("after") && after == "") {
		http.Error(w, "invalid after", http.StatusBadRequest)
		return
	}
	limit := defaultRechargeRewardLimit
	if q.Has("limit") {
		parsed, err := strconv.Atoi(strings.TrimSpace(q.Get("limit")))
		if err != nil || parsed < 1 || parsed > maxRechargeRewardLimit {
			http.Error(w, "invalid limit", http.StatusBadRequest)
			return
		}
		limit = parsed
	}
	if hm.dbManager == nil {
		http.Error(w, "reward store unavailable", http.StatusInternalServerError)
		return
	}
	resp, err := queryRechargeRewards(hm.dbManager, owner, unique, after, limit)
	if err != nil {
		http.Error(w, "recharge reward query failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
