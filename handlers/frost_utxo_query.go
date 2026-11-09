package handlers

import (
	"bytes"
	"context"
	btcadapter "dex/frost/chain/btc"
	"dex/keys"
	"dex/pb"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"google.golang.org/protobuf/proto"
)

const (
	defaultFrostUTXOLimit = 50
	maxFrostUTXOLimit     = 200
	maxFrostUTXOCursor    = 1024
	maxFrostUTXORecord    = 64 << 10
	maxFrostUTXOLabel     = 512
)

type frostUTXOStore interface {
	ScanKeysWithLimitOrderedAfter(string, string, int, bool) ([]string, error)
	GetKVs([]string) (map[string][]byte, error)
}

type frostVaultUTXOItem struct {
	Address        string `json:"address"`
	TxID           string `json:"txid"`
	Vout           uint32 `json:"vout"`
	Amount         string `json:"amount"`
	FinalizeHeight uint64 `json:"finalize_height"`
	RequestID      string `json:"request_id"`
	LockJobID      string `json:"lock_job_id,omitempty"`
	OriginID       string `json:"origin_id,omitempty"`
	OriginLegacy   bool   `json:"origin_legacy"`
}

type frostVaultUTXOPage struct {
	Chain      string               `json:"chain"`
	BTCNetwork string               `json:"btc_network"`
	VaultID    uint32               `json:"vault_id"`
	Items      []frostVaultUTXOItem `json:"items"`
	Limit      int                  `json:"limit"`
	NextCursor string               `json:"next_cursor"`
	HasMore    bool                 `json:"has_more"`
}

type frostUTXOCursor struct {
	Version int    `json:"v"`
	Chain   string `json:"chain"`
	VaultID uint32 `json:"vault_id"`
	Prefix  string `json:"prefix"`
	After   string `json:"after"`
}

func frostUTXOOutpoint(prefix, key string) (string, uint32, error) {
	if !strings.HasPrefix(key, prefix) {
		return "", 0, fmt.Errorf("UTXO key is outside vault")
	}
	parts := strings.Split(strings.TrimPrefix(key, prefix), "_")
	if len(parts) != 2 || len(parts[0]) != 64 || strings.ToLower(parts[0]) != parts[0] {
		return "", 0, fmt.Errorf("invalid UTXO outpoint")
	}
	if _, err := hex.DecodeString(parts[0]); err != nil {
		return "", 0, fmt.Errorf("invalid UTXO transaction ID")
	}
	vout, err := frostQueryUint(parts[1], 32)
	if err != nil {
		return "", 0, fmt.Errorf("invalid UTXO output index")
	}
	return parts[0], uint32(vout), nil
}

func parseFrostUTXOCursor(raw string, vaultID uint32) (string, error) {
	if raw == "" {
		return "", nil
	}
	if len(raw) > maxFrostUTXOCursor {
		return "", fmt.Errorf("cursor exceeds limit")
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil || base64.RawURLEncoding.EncodeToString(data) != raw {
		return "", fmt.Errorf("invalid cursor encoding")
	}
	var cursor frostUTXOCursor
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil || decoder.Decode(new(any)) != io.EOF {
		return "", fmt.Errorf("invalid cursor")
	}
	canonical, _ := json.Marshal(cursor)
	if !bytes.Equal(canonical, data) {
		return "", fmt.Errorf("non-canonical cursor")
	}
	prefix := keys.KeyFrostBtcUtxoPrefix(vaultID)
	if cursor.Version != 1 || cursor.Chain != "btc" || cursor.VaultID != vaultID || cursor.Prefix != prefix {
		return "", fmt.Errorf("cursor does not match vault")
	}
	if _, _, err := frostUTXOOutpoint(prefix, cursor.After); err != nil {
		return "", fmt.Errorf("invalid cursor position")
	}
	return cursor.After, nil
}

func encodeFrostUTXOCursor(vaultID uint32, after string) string {
	data, _ := json.Marshal(frostUTXOCursor{Version: 1, Chain: "btc", VaultID: vaultID, Prefix: keys.KeyFrostBtcUtxoPrefix(vaultID), After: after})
	return base64.RawURLEncoding.EncodeToString(data)
}

func parseFrostUTXOQuery(rawQuery string) (uint32, int, string, error) {
	if len(rawQuery) > 4096 {
		return 0, 0, "", fmt.Errorf("query exceeds limit")
	}
	q, err := url.ParseQuery(rawQuery)
	if err != nil {
		return 0, 0, "", fmt.Errorf("invalid query encoding")
	}
	for key, values := range q {
		if key != "chain" && key != "vault_id" && key != "limit" && key != "cursor" || len(values) != 1 {
			return 0, 0, "", fmt.Errorf("unknown or duplicate query parameter")
		}
	}
	if q.Get("chain") != "btc" {
		return 0, 0, "", fmt.Errorf("only chain=btc is supported")
	}
	vault, err := frostQueryUint(q.Get("vault_id"), 32)
	if err != nil {
		return 0, 0, "", fmt.Errorf("invalid vault_id")
	}
	limit := defaultFrostUTXOLimit
	if q.Has("limit") {
		n, err := frostQueryUint(q.Get("limit"), 16)
		if err != nil || n == 0 || n > maxFrostUTXOLimit {
			return 0, 0, "", fmt.Errorf("limit must be between 1 and 200")
		}
		limit = int(n)
	}
	after, err := parseFrostUTXOCursor(q.Get("cursor"), uint32(vault))
	return uint32(vault), limit, after, err
}

                                                                                
                                                                             
                                                     
                                                                               
                                                                             
                                                                               
func queryFrostVaultUTXOs(ctx context.Context, store frostUTXOStore, vaultID uint32, limit int, after string) (frostVaultUTXOPage, error) {
	page := frostVaultUTXOPage{Chain: "btc", VaultID: vaultID, Items: []frostVaultUTXOItem{}, Limit: limit}
	if limit < 1 || limit > maxFrostUTXOLimit {
		return page, fmt.Errorf("invalid page limit")
	}
	if err := ctx.Err(); err != nil {
		return page, err
	}
	prefix := keys.KeyFrostBtcUtxoPrefix(vaultID)
	if after != "" {
		if _, _, err := frostUTXOOutpoint(prefix, after); err != nil {
			return page, err
		}
	}
	rows, err := store.ScanKeysWithLimitOrderedAfter(prefix, after, limit+1, false)
	if err != nil {
		return page, err
	}
	if err := ctx.Err(); err != nil {
		return page, err
	}
	if len(rows) > limit+1 {
		return page, fmt.Errorf("UTXO scan exceeds page limit")
	}
	page.HasMore = len(rows) > limit
	if page.HasMore {
		rows = rows[:limit]
	}
	readKeys := make([]string, 0, len(rows)*2+1)
	lockKeys := make([]string, len(rows))
	previous := after
	for i, key := range rows {
		txid, vout, err := frostUTXOOutpoint(prefix, key)
		if err != nil || key <= previous {
			return page, fmt.Errorf("invalid UTXO scan order or key")
		}
		previous = key
		lockKeys[i] = keys.KeyFrostBtcLockedUtxo(vaultID, txid, vout)
		readKeys = append(readKeys, key, lockKeys[i])
	}
	if len(readKeys) == 0 {
		return page, nil
	}
	configKey := keys.KeyFrostVaultConfig("btc", 0)
	readKeys = append(readKeys, configKey)
	values, err := store.GetKVs(readKeys)
	if err != nil {
		return page, err
	}
	var network *chaincfg.Params
	if raw, exists := values[configKey]; exists {
		var config pb.FrostVaultConfig
		if len(raw) == 0 || len(raw) > pb.MaxTotalSize || proto.Unmarshal(raw, &config) != nil ||
			(config.Chain != "" && !strings.EqualFold(config.Chain, "btc")) {
			return page, fmt.Errorf("invalid BTC vault config")
		}
		                                                                        
		                                                                      
		if params, err := btcadapter.NetworkParams(config.BtcNetwork); err == nil {
			network = params
			page.BTCNetwork = params.Name
		}
	}
	for i, key := range rows {
		if err := ctx.Err(); err != nil {
			return page, err
		}
		raw, exists := values[key]
		if !exists {
			                                                                  
			continue
		}
		var utxo pb.FrostUtxo
		if len(raw) == 0 || len(raw) > maxFrostUTXORecord || proto.Unmarshal(raw, &utxo) != nil ||
			utxo.VaultId != vaultID || keys.KeyFrostBtcUtxo(vaultID, utxo.Txid, utxo.Vout) != key || len(utxo.RequestId) > maxFrostUTXOLabel || !utf8.ValidString(utxo.RequestId) {
			return page, fmt.Errorf("invalid UTXO record")
		}
		amount, amountErr := strconv.ParseUint(utxo.Amount, 10, 64)
		if amountErr != nil || amount == 0 || strconv.FormatUint(amount, 10) != utxo.Amount {
			return page, fmt.Errorf("invalid UTXO amount")
		}
		item := frostVaultUTXOItem{TxID: utxo.Txid, Vout: utxo.Vout, Amount: utxo.Amount, FinalizeHeight: utxo.FinalizeHeight, RequestID: utxo.RequestId, OriginID: hex.EncodeToString(utxo.OriginId), OriginLegacy: utxo.OriginLegacy}
		item.Address = frostUTXOAddress(utxo.ScriptPubkey, network)
		if lock, exists := values[lockKeys[i]]; exists {
			if len(lock) == 0 || len(lock) > maxFrostUTXOLabel || !utf8.Valid(lock) {
				return page, fmt.Errorf("invalid UTXO reservation")
			}
			item.LockJobID = string(lock)
		}
		page.Items = append(page.Items, item)
	}
	if page.HasMore {
		                                                                       
		                                                   
		page.NextCursor = encodeFrostUTXOCursor(vaultID, rows[len(rows)-1])
	}
	return page, nil
}

                                                                          
                                                                            
                                                                   
func frostUTXOAddress(script []byte, network *chaincfg.Params) string {
	if network == nil || len(script) == 0 {
		return ""
	}
	class, addresses, _, err := txscript.ExtractPkScriptAddrs(script, network)
	if err != nil || len(addresses) != 1 {
		return ""
	}
	switch class {
	case txscript.PubKeyHashTy, txscript.ScriptHashTy, txscript.WitnessV0PubKeyHashTy,
		txscript.WitnessV0ScriptHashTy, txscript.WitnessV1TaprootTy:
	default:
		return ""
	}
	roundtrip, err := txscript.PayToAddrScript(addresses[0])
	if err != nil || !bytes.Equal(roundtrip, script) {
		return ""
	}
	return addresses[0].EncodeAddress()
}

func (hm *HandlerManager) HandleFrostVaultUTXOs(w http.ResponseWriter, r *http.Request) {
	if !frostReadonlyGET(w, r) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	vaultID, limit, after, err := parseFrostUTXOQuery(r.URL.RawQuery)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	if hm.dbManager == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "UTXO store unavailable"})
		return
	}
	page, err := queryFrostVaultUTXOs(r.Context(), hm.dbManager, vaultID, limit, after)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "UTXO query unavailable: " + err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(page)
}
