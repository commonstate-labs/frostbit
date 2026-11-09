package handlers

import (
	"dex/consensus"
	"net/http"
)

type blockTimingResponse struct {
	Available bool                           `json:"available"`
	Timing    *consensus.BlockTimingSnapshot `json:"timing,omitempty"`
}

func (hm *HandlerManager) HandleBlockTiming(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	hm.Stats.RecordAPICall("HandleBlockTiming")
	if hm.consensusManager == nil {
		writeJSONData(w, blockTimingResponse{Available: false})
		return
	}
	timing, ok := hm.consensusManager.GetLastBlockTiming()
	if !ok {
		writeJSONData(w, blockTimingResponse{Available: false})
		return
	}
	writeJSONData(w, blockTimingResponse{Available: true, Timing: &timing})
}
