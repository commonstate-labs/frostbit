package handlers

import (
	"encoding/json"
	"io"
	"net/http"
)

                                                                      
func (hm *HandlerManager) HandlePause(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if hm.Stats != nil {
		hm.Stats.RecordAPICall("HandlePause")
	}

	type req struct {
		Paused bool `json:"paused"`
	}
	type resp struct {
		OK     bool `json:"ok"`
		Paused bool `json:"paused"`
	}

	if r.Method == http.MethodGet {
		                         
		paused := hm.consensusManager != nil && hm.consensusManager.IsPaused()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp{OK: true, Paused: paused})
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body req
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	if hm.consensusManager == nil {
		http.Error(w, "consensus not available", http.StatusServiceUnavailable)
		return
	}

	hm.consensusManager.SetPaused(body.Paused)
	if hm.Logger != nil {
		hm.Logger.Info("[HandlePause] Node paused=%v", body.Paused)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp{OK: true, Paused: body.Paused})
}
