package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"dex/frost/withdrawresult"
)

func frostQueryUint(raw string, bits int) (uint64, error) {
	n, err := strconv.ParseUint(raw, 10, bits)
	if err != nil || strconv.FormatUint(n, 10) != raw {
		return 0, strconv.ErrSyntax
	}
	return n, nil
}

func writeFrostReadonlyResult(w http.ResponseWriter, result any, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		status, message := http.StatusServiceUnavailable, "FROST result unavailable"
		var e *withdrawresult.Error
		if errors.As(err, &e) {
			status, message = e.Status, e.Message
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
		return
	}
	_ = json.NewEncoder(w).Encode(result)
}

func frostReadonlyGET(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet {
		return true
	}
	w.Header().Set("Allow", http.MethodGet)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	return false
}

func (hm *HandlerManager) HandleFrostHandoverPage(w http.ResponseWriter, r *http.Request) {
	if !frostReadonlyGET(w, r) {
		return
	}
	q := r.URL.Query()
	vault, err := frostQueryUint(q.Get("vault_id"), 32)
	from, fromErr := frostQueryUint(q.Get("from_epoch"), 64)
	target, targetErr := frostQueryUint(q.Get("to_epoch"), 64)
	if err != nil || fromErr != nil || targetErr != nil {
		http.Error(w, "invalid vault_id or epoch", http.StatusBadRequest)
		return
	}
	result, err := withdrawresult.ResolveHandoverPage(r.Context(), hm.frostResultGet, q.Get("chain"), uint32(vault), from, target)
	writeFrostReadonlyResult(w, result, err)
}

func (hm *HandlerManager) HandleFrostWithdrawBatch(w http.ResponseWriter, r *http.Request) {
	if !frostReadonlyGET(w, r) {
		return
	}
	q := r.URL.Query()
	vault, err := frostQueryUint(q.Get("vault_id"), 32)
	if err != nil {
		http.Error(w, "invalid vault_id", http.StatusBadRequest)
		return
	}
	result, err := withdrawresult.ResolveBatch(r.Context(), hm.frostResultGet, q.Get("chain"), uint32(vault), q.Get("batch_seq"))
	writeFrostReadonlyResult(w, result, err)
}
