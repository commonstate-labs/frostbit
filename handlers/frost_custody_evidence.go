package handlers

import (
	"encoding/json"
	"io"
	"net/http"

	"dex/frost/custodyevidence"
)

                                                                              
                                                                            
                                                                         
func (hm *HandlerManager) HandleFrostBTCInternalOutputs(w http.ResponseWriter, r *http.Request) {
	if !explorerServiceAuthorized(r, false) {
		http.Error(w, "explorer service authentication required", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		TxIDs []string `json:"txids"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		http.Error(w, "invalid Bitcoin evidence request", http.StatusBadRequest)
		return
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF || len(body.TxIDs) == 0 || len(body.TxIDs) > 256 {
		http.Error(w, "invalid Bitcoin evidence batch", http.StatusBadRequest)
		return
	}
	outputs, err := custodyevidence.InternalBTCOutputsMany(hm.frostResultGet, hm.dbManager.GetKVs, body.TxIDs)
	if err != nil {
		http.Error(w, "Bitcoin custody evidence unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(struct {
		Outputs []custodyevidence.BTCOutputs `json:"outputs"`
	}{outputs})
}
