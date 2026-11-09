package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"dex/frost/withdrawresult"
	statedb "dex/stateDB"
	"github.com/cockroachdb/pebble"
)

func (hm *HandlerManager) frostResultGet(key string) ([]byte, bool, error) {
	raw, err := hm.dbManager.Get(key)
	if errors.Is(err, statedb.ErrNotFound) || errors.Is(err, pebble.ErrNotFound) {
		return nil, false, nil
	}
	return raw, err == nil, err
}

func (hm *HandlerManager) HandleFrostWithdrawResult(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	resolve := withdrawresult.Resolve
	if mode := r.URL.Query().Get("summary_only"); mode == "1" {
		resolve = withdrawresult.ResolveSummary
	} else if mode != "" {
		http.Error(w, "invalid summary_only", http.StatusBadRequest)
		return
	}
	result, err := resolve(r.Context(), hm.frostResultGet, r.URL.Query().Get("withdraw_id"), r.URL.Query().Get("request_tx_id"))
	if err != nil {
		status, message := http.StatusServiceUnavailable, "withdrawal result unavailable"
		var queryErr *withdrawresult.Error
		if errors.As(err, &queryErr) {
			status, message = queryErr.Status, queryErr.Message
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(result)
}
