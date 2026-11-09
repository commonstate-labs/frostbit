package handlers

import (
	"dex/keys"
	"dex/pb"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"
)

const (
	defaultReferralPageLimit = 50
	maxReferralPageLimit     = 100
	maxReferralAddressBytes  = 128
	maxReferralCursorBytes   = 2048
	maxReferralInfoInvitees  = 100
)

type referralInfo struct {
	Address      string   `json:"address"`
	Referrer     string   `json:"referrer"`
	InviteeCount int      `json:"invitee_count"`
	Invitees     []string `json:"invitees"`
	HasMore      bool     `json:"has_more,omitempty"`
}

                                                                           
                                                                               
func (hm *HandlerManager) HandleReferralInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	addr := strings.TrimSpace(r.URL.Query().Get("address"))
	if addr == "" || len(addr) > maxReferralAddressBytes {
		writeReferralError(w, http.StatusBadRequest, "invalid address")
		return
	}

	info := referralInfo{Address: addr, Invitees: []string{}}
	prefix := keys.KeyReferralInviteePrefix(addr)
	referrer, inviteeKeys, err := hm.dbManager.ReferralInfo(addr, maxReferralInfoInvitees+1)
	if err != nil {
		writeReferralError(w, http.StatusInternalServerError, "referral info query failed")
		return
	}
	info.Referrer = referrer
	info.HasMore = len(inviteeKeys) > maxReferralInfoInvitees
	if info.HasMore {
		inviteeKeys = inviteeKeys[:maxReferralInfoInvitees]
	}
	info.Invitees = make([]string, 0, len(inviteeKeys))
	for _, key := range inviteeKeys {
		info.Invitees = append(info.Invitees, strings.TrimPrefix(key, prefix))
	}
	info.InviteeCount = len(info.Invitees)
	_ = json.NewEncoder(w).Encode(info)
}

type rebateRecord struct {
	Pair           string `json:"pair"`
	Notional       string `json:"notional"`
	FullFee        string `json:"full_fee"`
	ActualFee      string `json:"actual_fee"`
	ReferrerRebate string `json:"referrer_rebate"`
	Height         uint64 `json:"height"`
	TxID           string `json:"tx_id"`
	Trader         string `json:"trader,omitempty"`
}

type rebateResponse struct {
	Address    string         `json:"address"`
	Kind       string         `json:"kind"`
	Records    []rebateRecord `json:"records"`
	NextCursor string         `json:"next_cursor,omitempty"`
	HasMore    bool           `json:"has_more"`
}

                                                                       
                                                                             
                                         
func (hm *HandlerManager) HandleReferralRebates(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	addr := strings.TrimSpace(r.URL.Query().Get("address"))
	if addr == "" || len(addr) > maxReferralAddressBytes {
		writeReferralError(w, http.StatusBadRequest, "invalid address")
		return
	}

	kind := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("kind")))
	var prefix string
	switch kind {
	case "discount":
		prefix = keys.KeyTradingFeePrefix(addr)
	case "rebate":
		prefix = keys.KeyReferralRebateRecordPrefix(addr)
	default:
		writeReferralError(w, http.StatusBadRequest, "kind must be discount or rebate")
		return
	}

	limit, err := referralPageLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeReferralError(w, http.StatusBadRequest, err.Error())
		return
	}
	cursor, err := decodeReferralPageCursor(r.URL.Query().Get("cursor"), prefix)
	if err != nil {
		writeReferralError(w, http.StatusBadRequest, err.Error())
		return
	}
	pairs, err := hm.dbManager.ScanKVWithLimitOrderedAfter(prefix, cursor, limit+1, true)
	if err != nil {
		writeReferralError(w, http.StatusInternalServerError, "history query failed")
		return
	}

	hasMore := len(pairs) > limit
	if hasMore {
		pairs = pairs[:limit]
	}
	records := make([]rebateRecord, 0, len(pairs))
	for _, pair := range pairs {
		if record, ok := decodeRebateRecord(pair.Value); ok {
			records = append(records, record)
		}
	}
	response := rebateResponse{Address: addr, Kind: kind, Records: records, HasMore: hasMore}
	if hasMore && len(pairs) > 0 {
		response.NextCursor = encodeReferralPageCursor(pairs[len(pairs)-1].Key)
	}
	_ = json.NewEncoder(w).Encode(response)
}

func referralPageLimit(raw string) (int, error) {
	if raw == "" {
		return defaultReferralPageLimit, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 {
		return 0, fmt.Errorf("invalid limit")
	}
	if limit > maxReferralPageLimit {
		limit = maxReferralPageLimit
	}
	return limit, nil
}

func encodeReferralPageCursor(key string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(key))
}

func decodeReferralPageCursor(raw, prefix string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if len(raw) > maxReferralCursorBytes {
		return "", fmt.Errorf("invalid cursor")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || !strings.HasPrefix(string(decoded), prefix) {
		return "", fmt.Errorf("invalid cursor")
	}
	return string(decoded), nil
}

func decodeRebateRecord(data []byte) (rebateRecord, bool) {
	var record pb.TradingFeeRecord
	if proto.Unmarshal(data, &record) != nil {
		return rebateRecord{}, false
	}
	return rebateRecord{
		Pair: record.Pair, Notional: record.Notional, FullFee: record.FullFee,
		ActualFee: record.ActualFee, ReferrerRebate: record.ReferrerRebate,
		Height: record.Height, TxID: record.TxId, Trader: record.Trader,
	}, true
}

func writeReferralError(w http.ResponseWriter, status int, message string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
